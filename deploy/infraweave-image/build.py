#!/usr/bin/env python3
"""Build, check and describe the infraweave image and the worker chart.

The infraweave image is the static infraweave binary, built with
GOFIPS140=certified for linux/amd64 and linux/arm64, on the distroless base
the Dockerfile pins. The worker release publishes it as
ghcr.io/weaveplatform/infraweave-worker, the image of both the worker
controller and each run Pod's bootstrap init container, beside the
infraweave-workers chart, whose default values pin that image's digest.
worker-artifacts.json records what a release published: the image and chart
digests, the binaries' SHA-256, the runner images the release's server pins
and the identity that signed them.

Subcommands:
  binaries   build the binaries and their SHA256SUMS into a context directory
  build      build the image from a context with buildx, loading or pushing it
  smoke      run an image's version command and check its build and FIPS mode
  verify     check a pushed image's platforms, SBOM, provenance and anonymous pull
  chart      package the chart with the image's digest as its default
  manifest   write worker-artifacts.json
"""

import argparse
import hashlib
import importlib.util
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent.parent
CHART = ROOT / "deploy/helm/infraweave-workers"
LOCK = ROOT / "deploy/images/images.lock.json"
MODULE = "github.com/weaveplatform/infraweave"
PLATFORMS = ("linux/amd64", "linux/arm64")
USER = "1983:1983"
ISSUER = "https://token.actions.githubusercontent.com"
SIGNER = "https://github.com/weaveplatform/infraweave/.github/workflows/worker-artifacts.yml@refs/heads/main"
# The image block of the chart's values.yaml that chart() pins.
VALUES_IMAGE = '  image:\n    repository: ghcr.io/weaveplatform/infraweave-worker\n    digest: ""\n'
DIGEST = re.compile(r"^sha256:[0-9a-f]{64}$")


def run(*args, capture=False, env=None, cwd=ROOT):
    result = subprocess.run(args, cwd=cwd, env=env, check=True, text=True,
                            stdout=subprocess.PIPE if capture else None)
    return result.stdout if capture else None


def imagectl():
    """The runner image pipeline, whose registry checks this one reuses."""
    spec = importlib.util.spec_from_file_location("imagectl", ROOT / "deploy/images/imagectl.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def sha256(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def revision():
    return run("git", "rev-parse", "HEAD", capture=True).strip()


def binaries(args):
    """Build infraweave for each platform into args.out, as release builds are:
    static, trimmed, certified FIPS module, stamped with version, commit and
    the commit's time, so the same commit builds the same bytes."""
    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    commit = revision()
    built = run("git", "show", "-s", "--format=%cd", "--date=format-local:%Y-%m-%dT%H:%M:%SZ", commit,
                capture=True).strip()
    flags = " ".join(["-s", "-w"] + [f"-X {MODULE}/internal/platform/buildinfo.{k}={v}"
                                     for k, v in (("version", args.version), ("commit", commit), ("buildDate", built))])
    sums = []
    for platform in PLATFORMS:
        arch = platform.split("/")[1]
        name = "infraweave-linux-" + arch
        env = dict(os.environ, GOWORK="off", CGO_ENABLED="0", GOOS="linux", GOARCH=arch, GOFIPS140="certified", TZ="UTC")
        run("go", "build", "-trimpath", "-ldflags", flags, "-o", str(out / name), "./cmd/infraweave", env=env)
        sums.append(f"{sha256(out / name)}  {name}\n")
    (out / "SHA256SUMS").write_text("".join(sums))


def recipe(context):
    """The image's recipe: its Dockerfile and the binaries' sums together."""
    digest = hashlib.sha256()
    digest.update((HERE / "Dockerfile").read_bytes())
    digest.update((Path(context) / "SHA256SUMS").read_bytes())
    return "sha256:" + digest.hexdigest()


def build(args):
    labels = {
        "org.opencontainers.image.title": args.repository.rsplit("/", 1)[-1],
        "org.opencontainers.image.source": "https://github.com/weaveplatform/infraweave",
        "org.opencontainers.image.licenses": "MIT",
        "org.opencontainers.image.version": args.version,
        "org.opencontainers.image.revision": revision(),
        "io.infraweave.recipe": recipe(args.context),
    }
    command = ["docker", "buildx", "build", "--file", str(HERE / "Dockerfile"), "--platform", args.platform,
               "--tag", args.repository + ":" + args.tag]
    for key, value in labels.items():
        command += ["--label", key + "=" + value]
    if args.push:
        command += ["--push", "--provenance=mode=max", "--sbom=true"]
    else:
        if "," in args.platform:
            raise ValueError("load one platform into the daemon at a time")
        command += ["--load"]
    if args.metadata:
        command += ["--metadata-file", args.metadata]
    run(*command, args.context)


def check_version(output, version, commit):
    """Refuse a version report of another build, or one without the
    certified module in FIPS 140-3 mode."""
    lines = output.strip().splitlines()
    if len(lines) != 2:
        raise ValueError("unexpected version output: " + output)
    if not lines[0].startswith(f"infraweave {version} (commit {commit}, built "):
        raise ValueError("the image holds another build: " + lines[0])
    match = re.fullmatch(r"FIPS 140-3 mode (on|only), Go Cryptographic Module (\S+)", lines[1])
    if not match or match.group(2) == "latest":
        raise ValueError("the image's binary is not in FIPS 140-3 mode on a certified module: " + lines[1])
    return match.group(2)


def smoke(args):
    config = json.loads(run("docker", "image", "inspect", args.reference, capture=True))[0]["Config"]
    if config.get("User") != USER or config.get("Entrypoint") != ["/usr/local/bin/infraweave"]:
        raise ValueError(f"image user/entrypoint contract: {config.get('User')} {config.get('Entrypoint')}")
    output = run("docker", "run", "--rm", "--platform", args.platform, args.reference, "version", capture=True)
    module = check_version(output, args.version, args.revision or revision())
    run("docker", "run", "--rm", "--platform", args.platform, args.reference, "worker-controller", "--help", capture=True)
    print(f"{args.reference} ({args.platform}): infraweave {args.version}, Go Cryptographic Module {module}")


def platforms(reference):
    index = imagectl().inspect(reference)
    found = {m["platform"]["os"] + "/" + m["platform"]["architecture"]: m["digest"]
             for m in index.get("manifests", []) if m["platform"]["os"] != "unknown"}
    if set(found) != set(PLATFORMS):
        raise ValueError(f"{reference}: platforms {sorted(found)}")
    return found


def verify(args):
    ctl = imagectl()
    platforms(args.reference)
    ctl.attestations(args.reference, {"recipe": recipe(args.context), "source": args.revision or revision()})
    for platform in PLATFORMS:
        ctl.anonymous(args.reference, platform)


def pin_values(text, reference):
    """values.yaml text with controller.image pinned to reference."""
    repository, _, digest = reference.partition("@")
    if not DIGEST.match(digest):
        raise ValueError("the chart's image must be pinned by digest: " + reference)
    if text.count(VALUES_IMAGE) != 1:
        raise ValueError("values.yaml no longer holds the controller image block chart() pins")
    return text.replace(VALUES_IMAGE, f'  image:\n    repository: {repository}\n    digest: "{digest}"\n')


def chart(args):
    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="infraweave-workers-chart-") as directory:
        copy = Path(directory) / "infraweave-workers"
        shutil.copytree(CHART, copy)
        values = copy / "values.yaml"
        values.write_text(pin_values(values.read_text(), args.image))
        run("helm", "package", str(copy), "--version", args.version, "--app-version", args.version,
            "--destination", str(out))
    package = out / f"infraweave-workers-{args.version}.tgz"
    print(package)


def runner_images(lock):
    return {kind: entry["reference"] for kind, entry in json.loads(Path(lock).read_text())["images"].items()}


def describe(version, commit, image, image_platforms, context, chart_source, chart_digest, chart_package, lock):
    """The worker artifact contract of one release."""
    if not DIGEST.match(image.partition("@")[2]) or not DIGEST.match(chart_digest):
        raise ValueError("artifacts must be pinned by digest")
    sums = {}
    for line in (Path(context) / "SHA256SUMS").read_text().splitlines():
        digest, name = line.split()
        sums["linux/" + name.rsplit("-", 1)[1]] = "sha256:" + digest
    return {
        "schema": 1, "kind": "worker-artifacts", "version": version, "revision": commit,
        "controller_image": image, "bootstrap_image": image, "platforms": image_platforms, "binaries": sums,
        "fips": {"build": "GOFIPS140=certified", "default_mode": "on"},
        "chart": {"source": chart_source, "version": version, "digest": chart_digest,
                  "sha256": "sha256:" + sha256(chart_package)},
        "runner_images": runner_images(lock),
        "signature": {"identity": SIGNER, "issuer": ISSUER},
    }


def manifest(args):
    value = describe(args.version, args.revision or revision(), args.image, platforms(args.image), args.context,
                     args.chart_source, args.chart_digest, args.chart_package, LOCK)
    Path(args.out).write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    commands = parser.add_subparsers(dest="command", required=True)
    cmd = commands.add_parser("binaries")
    cmd.add_argument("--version", required=True)
    cmd.add_argument("--out", required=True)
    cmd.set_defaults(func=binaries)
    cmd = commands.add_parser("build")
    cmd.add_argument("--repository", required=True)
    cmd.add_argument("--tag", required=True)
    cmd.add_argument("--version", required=True)
    cmd.add_argument("--context", required=True)
    cmd.add_argument("--platform", default=",".join(PLATFORMS))
    cmd.add_argument("--push", action="store_true")
    cmd.add_argument("--metadata")
    cmd.set_defaults(func=build)
    cmd = commands.add_parser("smoke")
    cmd.add_argument("reference")
    cmd.add_argument("--platform", required=True)
    cmd.add_argument("--version", required=True)
    cmd.add_argument("--revision")
    cmd.set_defaults(func=smoke)
    cmd = commands.add_parser("verify")
    cmd.add_argument("reference")
    cmd.add_argument("--context", required=True)
    cmd.add_argument("--revision")
    cmd.set_defaults(func=verify)
    cmd = commands.add_parser("chart")
    cmd.add_argument("--version", required=True)
    cmd.add_argument("--image", required=True)
    cmd.add_argument("--out", required=True)
    cmd.set_defaults(func=chart)
    cmd = commands.add_parser("manifest")
    cmd.add_argument("--version", required=True)
    cmd.add_argument("--revision")
    cmd.add_argument("--image", required=True)
    cmd.add_argument("--context", required=True)
    cmd.add_argument("--chart-source", required=True)
    cmd.add_argument("--chart-digest", required=True)
    cmd.add_argument("--chart-package", required=True)
    cmd.add_argument("--out", required=True)
    cmd.set_defaults(func=manifest)
    args = parser.parse_args(argv)
    try:
        args.func(args)
    except (ValueError, subprocess.CalledProcessError) as error:
        print(f"build.py {args.command}: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
