#!/usr/bin/env python3
"""Build inputs, immutable publication records and real runner contracts."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import urllib.request

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent.parent
KINDS = ("terraform", "opentofu", "pulumi", "ansible", "test-support")
PLATFORMS = ("linux/amd64", "linux/arm64")
REGISTRY = "ghcr.io/weaveplatform/infraweave-runner-"
GENERATED = ROOT / "internal/platform/runnerimages/images.gen.go"


def read(path):
    return json.loads(Path(path).read_text())


def write(path, value):
    Path(path).write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def run(*args, capture=False, **kwargs):
    return subprocess.run(args, check=True, text=True, stdout=subprocess.PIPE if capture else None,
                          **kwargs).stdout


def inputs():
    data = read(HERE / "versions.json")
    if data["schema"] != 1 or data["revision"] < 1:
        raise ValueError("unsupported input schema or revision")
    if not re.fullmatch(r"alpine:[0-9.]+@sha256:[0-9a-f]{64}", data["base"]):
        raise ValueError("base must have a version and digest")
    for tool, entry in data["tools"].items():
        if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", entry["version"]):
            raise ValueError(f"{tool}: not a stable version")
        for digest in entry.get("sha256", {}).values():
            if not re.fullmatch(r"[a-f0-9]{64}", digest):
                raise ValueError(f"{tool}: invalid SHA-256")
    return data


def recipe(base=None):
    # Output pins, release records and documentation never cause rebuilds.
    digest = hashlib.sha256()
    def included(p):
        return (
        p.name in ("versions.json", "imagectl.py", "contract.sh", "Dockerfile") or p.suffix == ".lock"
        or p.suffix in (".py", ".sh", ".yml", ".js") and "tests" in p.parts
        or "rootfs" in p.parts or "callback_plugins" in p.parts
        ) and "__pycache__" not in p.parts
    if base:
        names = run("git", "ls-tree", "-r", "--name-only", base, "deploy/images", capture=True).splitlines()
        for name in sorted(names):
            path = Path(name).relative_to("deploy/images")
            if included(path):
                content = subprocess.check_output(["git", "show", base + ":" + name])
                digest.update(str(path).encode() + b"\0" + content + b"\0")
    else:
        for path in sorted(p for p in HERE.rglob("*") if p.is_file() and included(p)):
            digest.update(str(path.relative_to(HERE)).encode() + b"\0" + path.read_bytes() + b"\0")
    return digest.hexdigest()


def tag(kind, version=None):
    data = inputs()
    revision = f"r{data['revision']}"
    if kind == "test-support":
        return revision
    prefix = "tested-" if kind in ("terraform", "opentofu") else ""
    return f"{prefix}{version or data['tools'][kind]['version']}-{revision}"


def matrix():
    data = inputs()
    entries = [{"kind": k, "tag": tag(k), "id": k, "version": data["tools"].get(k, {}).get("version", "")}
               for k in KINDS]
    for kind, cohorts in data.get("cohorts", {}).items():
        if kind == "terragrunt":
            continue
        for entry in cohorts:
            version = entry["version"]
            entries.append({"kind": kind, "version": version, "tag": tag(kind, version), "id": kind + "-" + version})
    return entries


def render(kind, before, data, tool):
    after = before
    if kind != "test-support":
        after = re.sub(r"(?m)^FROM alpine:[^\n]+", "FROM " + data["base"], after)
    if kind == "pulumi":
        after = re.sub(r"pulumi-v[0-9.]+-linux", f"pulumi-v{tool['version']}-linux", after)
        after = re.sub(r"/download/v[0-9.]+/", f"/download/v{tool['version']}/", after)
        for arch, upstream in (("amd64", "x64"), ("arm64", "arm64")):
            checksum = tool["sha256"][f"pulumi-v{tool['version']}-linux-{upstream}.tar.gz"]
            after = re.sub(rf"({arch}\) arch={upstream}; sum=)[a-f0-9]+", rf"\g<1>{checksum}", after)
    return after


def generate(check=False):
    data = inputs()
    changed = []
    for kind in KINDS:
        path = HERE / f"runner-{kind}/Dockerfile"
        before = path.read_text()
        after = render(kind, before, data, data["tools"].get(kind))
        if before != after:
            changed.append(str(path.relative_to(ROOT)))
            if not check:
                path.write_text(after)
    ansible = (HERE / "runner-ansible/requirements.lock").read_text()
    if f"ansible-core=={data['tools']['ansible']['version']} " not in ansible:
        raise ValueError("refresh Ansible's dependency lock for the selected ansible-core")
    lock_path = HERE / "images.lock.json"
    if lock_path.exists():
        lock = read(lock_path)
        if lock["schema"] != 1 or set(lock["images"]) != set(KINDS):
            raise ValueError("incomplete image lock")
        lines = ["// Code generated by deploy/images/imagectl.py generate. DO NOT EDIT.",
                 "", "// Package runnerimages names the immutable environments shipped with this revision.",
                 "package runnerimages", "", "const ("]
        for kind in KINDS:
            entry = lock["images"][kind]
            ref = entry["reference"]
            if not re.fullmatch(re.escape(REGISTRY + kind) + r"@sha256:[0-9a-f]{64}", ref):
                raise ValueError(f"invalid locked reference: {ref}")
            pattern = {"terraform": r"tested-[0-9]+\.[0-9]+\.[0-9]+-r[1-9][0-9]*",
                       "opentofu": r"tested-[0-9]+\.[0-9]+\.[0-9]+-r[1-9][0-9]*",
                       "test-support": r"r[1-9][0-9]*"}.get(kind, r"[0-9]+\.[0-9]+\.[0-9]+-r[1-9][0-9]*")
            if not re.fullmatch(pattern, entry["tag"]) or set(entry["platforms"]) != set(PLATFORMS):
                raise ValueError(f"invalid release tag/platforms for {kind}")
            name = {"opentofu": "OpenTofu", "test-support": "TestSupport"}.get(kind, kind.title())
            lines += [f"\t// {name} is the {kind} environment, {entry['tag']}.", f'\t{name} = "{ref}"']
        lines += [")", ""]
        content = run("gofmt", capture=True, input="\n".join(lines))
        if not GENERATED.exists() or GENERATED.read_text() != content:
            changed.append(str(GENERATED.relative_to(ROOT)))
            if not check:
                GENERATED.parent.mkdir(parents=True, exist_ok=True)
                GENERATED.write_text(content)
    if check and changed:
        raise ValueError("generated files differ: " + ", ".join(changed))


def build(args):
    generate(check=True)
    command = ["docker", "buildx", "build", "--platform", args.platform,
               "--tag", args.tag, "--label", f"io.infraweave.recipe={recipe()}",
               "--label", "org.opencontainers.image.revision=" + run("git", "rev-parse", "HEAD", capture=True).strip()]
    if args.push:
        command += ["--push", "--provenance=mode=max", "--sbom=true"]
    else:
        if "," in args.platform:
            raise ValueError("load one platform into the test daemon at a time")
        command += ["--load"]
    if args.metadata:
        command += ["--metadata-file", args.metadata]
    data = inputs()
    selected = next((e for e in data.get("cohorts", {}).get(args.kind, []) if e["version"] == args.version), None)
    if args.version and args.version != data["tools"].get(args.kind, {}).get("version") and selected is None:
        raise ValueError("version is not an approved build input")
    if selected is None:
        run(*command, str(HERE / f"runner-{args.kind}"))
    else:
        with tempfile.TemporaryDirectory(prefix="infraweave-image-context-") as directory:
            context = Path(directory) / "context"
            shutil.copytree(HERE / f"runner-{args.kind}", context, ignore=shutil.ignore_patterns("__pycache__", "*.pyc"))
            dockerfile = context / "Dockerfile"
            dockerfile.write_text(render(args.kind, dockerfile.read_text(), data, selected))
            if args.kind == "ansible":
                shutil.copyfile(context / f"cohorts/{args.version}.lock", context / "requirements.lock")
            run(*command, str(context))


def inspect(reference):
    return json.loads(run("docker", "buildx", "imagetools", "inspect", reference, "--raw", capture=True))


def record(args):
    images = {}
    releases = []
    for candidate in matrix():
        kind = candidate["kind"]
        meta = read(Path(args.directory) / (candidate["id"] + ".json"))
        digest = meta["containerimage.digest"]
        reference = REGISTRY + kind + "@" + digest
        manifest = inspect(reference)
        platforms = {m["platform"]["os"] + "/" + m["platform"]["architecture"]: m["digest"]
                     for m in manifest["manifests"] if m["platform"]["os"] != "unknown"}
        if set(platforms) != set(PLATFORMS):
            raise ValueError(f"{kind}: missing platform: {platforms}")
        entry = {"reference": reference, "tag": candidate["tag"], "platforms": platforms,
                 "kind": kind, "version": candidate["version"], "recipe": recipe(),
                 "source": run("git", "rev-parse", "HEAD", capture=True).strip()}
        if candidate["id"] == kind:
            images[kind] = entry
        else:
            releases.append(entry)
    write(args.output, {"schema": 1, "images": images, "releases": releases})


def anonymous(reference, platform):
    with tempfile.TemporaryDirectory(prefix="infraweave-anonymous-") as config:
        # No inherited auth store/helper, even on a maintainer's machine.
        run("docker", "--config", config, "pull", "--platform", platform, reference)


def attestations(reference, expected=None):
    for field in ("SBOM", "Provenance"):
        value = json.loads(run("docker", "buildx", "imagetools", "inspect", reference,
                              "--format", "{{json ." + field + "}}", capture=True))
        if not value or not all(platform in value for platform in PLATFORMS):
            raise ValueError(f"{reference}: missing {field} for both platforms")
        for platform in PLATFORMS:
            document = value[platform]
            if field == "SBOM":
                spdx = document.get("SPDX", {})
                if not spdx.get("spdxVersion", "").startswith("SPDX-") or not spdx.get("SPDXID"):
                    raise ValueError(f"{reference}: not an SPDX SBOM on {platform}")
            else:
                # Buildx unwraps the in-toto envelope into its SLSA predicate.
                slsa = document.get("SLSA", {})
                if not slsa.get("buildDefinition") or not slsa.get("runDetails", {}).get("builder"):
                    raise ValueError(f"{reference}: no SLSA build definition/builder on {platform}")
                if expected:
                    labels = slsa["buildDefinition"]["externalParameters"]["request"]["args"]
                    if labels.get("label:io.infraweave.recipe") != expected["recipe"] or labels.get("label:org.opencontainers.image.revision") != expected["source"]:
                        raise ValueError(f"{reference}: provenance does not match approved recipe/source on {platform}")


def smoke(args):
    if args.anonymous:
        anonymous(args.reference, args.platform)
    if args.kind == "test-support":
        config = json.loads(run("docker", "image", "inspect", args.reference, capture=True))[0]["Config"]
        if config["User"] != "1983:1983" or config["WorkingDir"] != "/mnt/workspace":
            raise ValueError("test-support user/workspace contract")
        result = subprocess.run(["docker", "run", "--rm", "--platform", args.platform, args.reference, "/bin/sh"],
                                capture_output=True, text=True)
        if result.returncode == 0 or "no such file" not in result.stderr.lower():
            raise ValueError("test-support must have no shell: " + result.stderr)
        return
    run("docker", "run", "--rm", "--platform", args.platform,
        "--mount", f"type=bind,src={HERE},dst=/opt/image-tests,readonly",
        "--env", "RUNNER_KIND=" + args.kind,
        "--env", "RUNNER_TOOL_VERSION=" + (args.version or ""),
        args.reference, "sh", "/opt/image-tests/tests/contract.sh")


def registry_tag(reference):
    result = subprocess.run(["docker", "buildx", "imagetools", "inspect", reference, "--format", "{{json .Manifest}}"],
                            capture_output=True, text=True)
    if result.returncode == 0:
        return json.loads(result.stdout)["digest"]
    # Authentication/transport failures must never be treated as absence.
    if "not found" in result.stderr.lower() or "manifest_unknown" in result.stderr.lower():
        return None
    raise ValueError(result.stderr)


def promote(args):
    lock = read(args.lock)
    entries = [(k, e) for k, e in lock["images"].items()] + [(e["kind"], e) for e in lock.get("releases", [])]
    allowed = {(e["kind"], e["tag"]) for e in matrix()}
    for kind, entry in entries:
        if entry["recipe"] != recipe() or (kind, entry["tag"]) not in allowed:
            raise ValueError(f"{kind}: unapproved recipe/tag")
        attestations(entry["reference"], entry)
        for platform in PLATFORMS:
            anonymous(entry["reference"], platform)
        target = REGISTRY + kind + ":" + entry["tag"]
        existing = registry_tag(target)
        if existing and existing != entry["reference"].split("@", 1)[1]:
            raise ValueError(f"immutable tag already exists at another digest: {target}")
    # Complete all checks before writing any readable release tag.
    for kind, entry in entries:
        run("docker", "buildx", "imagetools", "create", "--prefer-index=false", "--tag",
            REGISTRY + kind + ":" + entry["tag"], entry["reference"])


def main():
    parser = argparse.ArgumentParser(__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    generate_parser = sub.add_parser("generate")
    generate_parser.add_argument("--check", action="store_true")
    sub.add_parser("recipe")
    sub.add_parser("matrix")
    revision_parser = sub.add_parser("validate-revision")
    revision_parser.add_argument("--base", required=True)
    b = sub.add_parser("build")
    b.add_argument("kind", choices=KINDS)
    b.add_argument("--platform", required=True)
    b.add_argument("--tag", required=True)
    b.add_argument("--push", action="store_true")
    b.add_argument("--metadata")
    b.add_argument("--version")
    s = sub.add_parser("smoke")
    s.add_argument("kind", choices=KINDS)
    s.add_argument("reference")
    s.add_argument("--platform", required=True, choices=PLATFORMS)
    s.add_argument("--anonymous", action="store_true")
    s.add_argument("--version")
    r = sub.add_parser("record")
    r.add_argument("directory")
    r.add_argument("--output", required=True)
    p = sub.add_parser("promote")
    p.add_argument("lock")
    args = parser.parse_args()
    if args.command == "generate":
        generate(args.check)
    elif args.command == "recipe":
        print(recipe())
    elif args.command == "matrix":
        print(json.dumps({"include": matrix()}))
    elif args.command == "validate-revision":
        previous = subprocess.run(["git", "show", args.base + ":deploy/images/images.lock.json"],
                                  capture_output=True, text=True)
        if previous.returncode == 0:
            if recipe(args.base) != recipe():
                before = json.loads(run("git", "show", args.base + ":deploy/images/versions.json", capture=True))
                if inputs()["revision"] <= before["revision"]:
                    raise ValueError("a changed recipe must advance versions.json revision; released tags are immutable")
    elif args.command == "build":
        build(args)
    elif args.command == "smoke":
        smoke(args)
    elif args.command == "record":
        record(args)
    elif args.command == "promote":
        promote(args)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, KeyError, subprocess.CalledProcessError) as error:
        sys.exit(str(error))
