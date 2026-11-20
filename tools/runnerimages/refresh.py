#!/usr/bin/env python3
"""Discover stable upstream releases without backfilling historical images."""

import importlib.util
import http.client
import json
import os
from pathlib import Path
import argparse
import re
import subprocess
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
HERE = ROOT / "deploy/images"
spec = importlib.util.spec_from_file_location("imagectl", HERE / "imagectl.py")
ctl = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ctl)
REPOS = {"terraform": "hashicorp/terraform", "opentofu": "opentofu/opentofu",
         "terragrunt": "gruntwork-io/terragrunt", "pulumi": "pulumi/pulumi"}


def fetch(url, payload=None):
    headers = {"User-Agent": "infraweave-runner-image-updater"}
    if url.startswith("https://api.github.com/") and os.environ.get("GH_TOKEN"):
        headers["Authorization"] = "Bearer " + os.environ["GH_TOKEN"]
    body = None
    if payload:
        headers["Content-Type"] = "application/json"
        body = json.dumps(payload).encode()
    for attempt in range(3):
        try:
            with urllib.request.urlopen(urllib.request.Request(url, headers=headers, data=body), timeout=30) as response:
                return response.read()
        except urllib.error.HTTPError as error:
            if error.code not in (429, 500, 502, 503, 504) or attempt == 2:
                raise
        except (urllib.error.URLError, http.client.HTTPException, TimeoutError):
            if attempt == 2:
                raise
        time.sleep(2 ** attempt)


def stable(version):
    return bool(re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", version))


def version_key(version):
    return tuple(map(int, version.split(".")))


def releases(tool, since):
    if tool == "ansible":
        document = json.loads(fetch("https://pypi.org/pypi/ansible-core/json"))
        return [v for v, files in document["releases"].items() if stable(v) and files and
                any(not f.get("yanked") and f["upload_time_iso_8601"] > since for f in files)]
    versions, cursor = [], None
    owner, name = REPOS[tool].split("/", 1)
    query = """query($owner:String!,$name:String!,$after:String){
      repository(owner:$owner,name:$name){ releases(first:100,after:$after){
        nodes{tagName isDraft isPrerelease publishedAt}
        pageInfo{hasNextPage endCursor}
      }}
    }"""
    while True:
        document = json.loads(fetch("https://api.github.com/graphql", {
            "query": query, "variables": {"owner": owner, "name": name, "after": cursor}}))
        if document.get("errors"):
            raise ValueError("GitHub release discovery failed: " + json.dumps(document["errors"]))
        releases = document["data"]["repository"]["releases"]
        for record in releases["nodes"]:
            version = record["tagName"].removeprefix("v")
            if stable(version) and not record["isDraft"] and not record["isPrerelease"] and record["publishedAt"] > since:
                versions.append(version)
        # Read only release metadata, not every historical release's assets.
        # Exhaust pagination so late publications of older backports count.
        page = releases["pageInfo"]
        if not page["hasNextPage"]:
            break
        if page["endCursor"] == cursor:
            raise ValueError("GitHub release pagination made no progress")
        cursor = page["endCursor"]
    return versions


def checksums(tool, version):
    if tool == "terraform":
        url = f"https://releases.hashicorp.com/terraform/{version}/terraform_{version}_SHA256SUMS"
        names = [f"terraform_{version}_linux_{a}.zip" for a in ("amd64", "arm64")]
    elif tool == "opentofu":
        url = f"https://github.com/opentofu/opentofu/releases/download/v{version}/tofu_{version}_SHA256SUMS"
        names = [f"tofu_{version}_linux_{a}.zip" for a in ("amd64", "arm64")]
    elif tool == "terragrunt":
        url = f"https://github.com/gruntwork-io/terragrunt/releases/download/v{version}/SHA256SUMS"
        names = [f"terragrunt_linux_{a}" for a in ("amd64", "arm64")]
    else:
        url = f"https://github.com/pulumi/pulumi/releases/download/v{version}/pulumi-{version}-checksums.txt"
        names = [f"pulumi-v{version}-linux-{a}.tar.gz" for a in ("x64", "arm64")]
    sums = {parts[1].lstrip("*"): parts[0] for line in fetch(url).decode().splitlines()
            if len(parts := line.split()) == 2}
    selected = {name: sums[name] for name in names}
    if not all(re.fullmatch(r"[a-f0-9]{64}", digest) for digest in selected.values()):
        raise ValueError(f"{tool} {version}: invalid upstream checksum")
    return selected


def ansible_lock(version, base):
    # Resolve on the target Python/Alpine, not the updater host's interpreter.
    script = """apk add --no-cache python3 py3-pip >&2
python3 -m venv /tmp/lock
/tmp/lock/bin/pip download --only-binary=:all: \"ansible-core==$VERSION\" -d /tmp/wheels >&2
/tmp/lock/bin/python - <<'PY'
import glob,json,zipfile
from email.parser import BytesParser
packages={}
for path in glob.glob('/tmp/wheels/*'):
 with zipfile.ZipFile(path) as archive:
  metadata=BytesParser().parsebytes(archive.read(next(n for n in archive.namelist() if n.endswith('.dist-info/METADATA'))))
  packages[metadata['Name']]=metadata['Version']
print(json.dumps(packages))
PY
"""
    packages = json.loads(subprocess.check_output(
        ["docker", "run", "--rm", "-i", "--env", "VERSION=" + version, base, "sh", "-e"],
        input=script, text=True))
    lines = ["# Alpine-resolved ansible-core and dependencies; official hashes for both architectures."]
    for name, pin in sorted(packages.items(), key=lambda item: item[0].lower()):
        document = json.loads(fetch(f"https://pypi.org/pypi/{name}/{pin}/json"))
        sums = sorted({f["digests"]["sha256"] for f in document["urls"] if f["packagetype"] == "bdist_wheel" and not f.get("yanked")})
        if not sums:
            raise ValueError(f"{name} {pin}: no non-yanked wheels")
        lines.append(name + "==" + pin + " \\\n" + " \\\n".join("    --hash=sha256:" + value for value in sums))
    return "\n".join(lines) + "\n"


def refresh(rebuild=False):
    data = ctl.inputs()
    state = ctl.read(HERE / "upstream.json")
    lock_path = HERE / "images.lock.json"
    if lock_path.exists() and any(e["recipe"] != ctl.recipe() for e in ctl.read(lock_path)["images"].values()):
        print("Previous recipe is awaiting successful publication and its pin PR; no overlapping update.")
        return
    updates = {}
    cohorts = {}
    pending_files = {}
    for tool in ("terraform", "opentofu", "terragrunt", "pulumi", "ansible"):
        known = set(state["seen"][tool])
        versions = sorted(set(releases(tool, state["since"])) - known, key=version_key)
        if not versions:
            continue
        entries = []
        for version in versions:
            entry = {"version": version}
            if tool != "ansible":
                entry["sha256"] = checksums(tool, version)
            else:
                pending_files[HERE / f"runner-ansible/cohorts/{version}.lock"] = ansible_lock(version, data["base"])
            entries.append(entry)
        latest = max([data["tools"][tool]] + entries, key=lambda entry: version_key(entry["version"]))
        if latest is not data["tools"][tool]:
            # Preserve the separately tested historical cohort.
            if "previous" in data["tools"][tool]:
                latest["previous"] = data["tools"][tool]["previous"]
            data["tools"][tool] = latest
        cohorts[tool] = [entry for entry in entries if entry["version"] != latest["version"]]
        state["seen"][tool] = sorted(known | set(versions), key=version_key)
        updates[tool] = versions
    if not updates and not rebuild:
        print("No new stable upstream releases; no PR or image rebuild.")
        return
    if rebuild:
        # Refresh the current pinned Alpine line and the actual index digest.
        repository = json.loads(fetch("https://hub.docker.com/v2/repositories/library/alpine/tags?page_size=100&ordering=last_updated"))
        tags = [tag["name"] for tag in repository["results"] if stable(tag["name"])]
        newest = max(tags, key=version_key)
        manifest = json.loads(ctl.run("docker", "buildx", "imagetools", "inspect", "alpine:" + newest,
                                      "--format", "{{json .Manifest}}", capture=True))
        data["base"] = "alpine:" + newest + "@" + manifest["digest"]
    data["revision"] += 1
    data["cohorts"] = cohorts
    if "ansible" in updates or rebuild:
        selected = data["tools"]["ansible"]["version"]
        path = HERE / f"runner-ansible/cohorts/{selected}.lock"
        pending_files[HERE / "runner-ansible/requirements.lock"] = pending_files.get(path) or ansible_lock(selected, data["base"])
    # All upstream lookups/resolution finish before changing build inputs.
    for path, content in pending_files.items():
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)
    ctl.write(HERE / "versions.json", data)
    ctl.write(HERE / "upstream.json", state)
    ctl.generate()
    print(json.dumps({"new_releases": updates, "revision": data["revision"], "rebuild": rebuild}))


def verify_upstreams():
    data = ctl.inputs()
    state = ctl.read(HERE / "upstream.json")
    for tool in ("terraform", "opentofu", "terragrunt", "pulumi", "ansible"):
        versions = releases(tool, state["since"])
        if tool != "ansible" and checksums(tool, data["tools"][tool]["version"]) != data["tools"][tool]["sha256"]:
            raise ValueError(f"{tool}: official checksums differ from the selected build inputs")
        print(json.dumps({"tool": tool, "discovered_stable_releases": versions,
                          "selected_version": data["tools"][tool]["version"], "verified": True}), flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(__doc__)
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--rebuild", action="store_true")
    mode.add_argument("--verify-upstreams", action="store_true", help="Read official indexes/checksums without changing inputs")
    options = parser.parse_args()
    if options.verify_upstreams:
        verify_upstreams()
    else:
        refresh(options.rebuild)
