#!/usr/bin/env python3
"""Build, check and describe the infraweave images and the worker chart.

Both infraweave images hold the static infraweave binary, built with
GOFIPS140=certified for linux/amd64 and linux/arm64, on the distroless base
their Dockerfiles pin; they differ in user and entrypoint. The worker image
(Dockerfile) is ghcr.io/weaveplatform/infraweave-worker, the image of both
the worker controller and each run Pod's bootstrap init container, which
worker-artifacts.yml publishes beside the infraweave-workers chart, whose
default values pin that image's digest. worker-artifacts.json records what
a release published: the image and chart digests, the binaries' SHA-256,
the runner images the release's server pins and the identity that signed
them. The server image (server.Dockerfile) is ghcr.io/weaveplatform/infraweave,
whose entrypoint is infraweave all; server-image.yml publishes it and
records it in server-image.json.

Subcommands:
  binaries         build the binaries and their SHA256SUMS into a context directory
  build            build an image from a context with buildx, loading or pushing it
  smoke            run an image's version command and check its build and FIPS
                   mode; the server image also starts on PostgreSQL and is probed
  verify           check a pushed image's platforms, SBOM, provenance and anonymous pull
  chart            package the chart with the worker image's digest as its default
  manifest         write worker-artifacts.json
  server-manifest  write server-image.json
"""

import argparse
import base64
import hashlib
import importlib.util
import json
import os
import re
import secrets
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import NamedTuple

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent.parent
CHART = ROOT / "deploy/helm/infraweave-workers"
LOCK = ROOT / "deploy/images/images.lock.json"
MODULE = "github.com/weaveplatform/infraweave"
PLATFORMS = ("linux/amd64", "linux/arm64")
BINARY = "/usr/local/bin/infraweave"
ISSUER = "https://token.actions.githubusercontent.com"
SIGNER = "https://github.com/weaveplatform/infraweave/.github/workflows/worker-artifacts.yml@refs/heads/main"
SERVER_SIGNER = "https://github.com/weaveplatform/infraweave/.github/workflows/server-image.yml@refs/heads/main"
# The PostgreSQL build the acceptance tests run, which the server's smoke
# test starts on.
POSTGRES_SOURCE = ROOT / "test/deps/images.go"
# The image block of the chart's values.yaml that chart() pins.
VALUES_IMAGE = '  image:\n    repository: ghcr.io/weaveplatform/infraweave-worker\n    digest: ""\n'
DIGEST = re.compile(r"^sha256:[0-9a-f]{64}$")


class Image(NamedTuple):
    """One infraweave image's recipe and the contract its smoke test checks."""
    dockerfile: Path
    user: str
    entrypoint: list
    ports: dict


# The server's ports are server.listen's and server.admin_listen's defaults,
# which server.Dockerfile exposes and the smoke test leaves unset.
IMAGES = {
    "worker": Image(HERE / "Dockerfile", "1983:1983", [BINARY], {}),
    "server": Image(HERE / "server.Dockerfile", "65532:65532", [BINARY, "all"], {"api": 8080, "admin": 9090}),
}
# server.public_url and the fs object store's URL in the smoke test's
# configuration; nothing dials it.
SMOKE_URL = "http://infraweave.test"
# How long the server may take to report ready: an emulated platform runs
# the binary, migrations included, several times slower.
READY_TIMEOUT = 300


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


def recipe(context, kind):
    """The image's recipe: its Dockerfile and the binaries' sums together."""
    digest = hashlib.sha256()
    digest.update(IMAGES[kind].dockerfile.read_bytes())
    digest.update((Path(context) / "SHA256SUMS").read_bytes())
    return "sha256:" + digest.hexdigest()


def build(args):
    labels = {
        "org.opencontainers.image.title": args.repository.rsplit("/", 1)[-1],
        "org.opencontainers.image.source": "https://github.com/weaveplatform/infraweave",
        "org.opencontainers.image.licenses": "MIT",
        "org.opencontainers.image.version": args.version,
        "org.opencontainers.image.revision": revision(),
        "io.infraweave.recipe": recipe(args.context, args.kind),
    }
    command = ["docker", "buildx", "build", "--file", str(IMAGES[args.kind].dockerfile), "--platform", args.platform,
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


def check_contract(config, kind):
    """Refuse an image whose user, entrypoint or exposed ports are not its
    kind's: a deployment of the server image passes only --config, and its
    probes target the admin port."""
    image = IMAGES[kind]
    exposed = sorted(config.get("ExposedPorts") or {})
    expected = sorted(f"{port}/tcp" for port in image.ports.values())
    if config.get("User") != image.user or config.get("Entrypoint") != image.entrypoint or exposed != expected:
        raise ValueError(f"{kind} image user/entrypoint/ports contract: "
                         f"{config.get('User')} {config.get('Entrypoint')} {exposed}")


def smoke(args):
    config = json.loads(run("docker", "image", "inspect", args.reference, capture=True))[0]["Config"]
    check_contract(config, args.kind)
    output = run("docker", "run", "--rm", "--platform", args.platform, "--entrypoint", BINARY, args.reference,
                 "version", capture=True)
    module = check_version(output, args.version, args.revision or revision())
    if args.kind == "worker":
        run("docker", "run", "--rm", "--platform", args.platform, args.reference, "worker-controller", "--help",
            capture=True)
    else:
        serve(args.reference, args.platform)
    print(f"{args.reference} ({args.platform}): infraweave {args.version}, Go Cryptographic Module {module}")


def postgres_image(source=POSTGRES_SOURCE):
    """The digest-pinned PostgreSQL image the acceptance tests run."""
    match = re.search(r'^\s*ImagePostgres\s*=\s*"([^"]+)"', Path(source).read_text(), re.MULTILINE)
    if not match or not DIGEST.match(match.group(1).partition("@")[2]):
        raise ValueError(f"{source}: no ImagePostgres pinned by digest")
    return match.group(1)


def server_config(dsn):
    """The smoke test's strict configuration: the database at dsn, the local
    KEK, which the environment carries, the fs object store on the
    container's tmpfs, and vendor release indexes on a closed port, so the
    test reaches nothing beyond its PostgreSQL. Both listeners keep their
    defaults, the ports the image exposes. JSON is YAML, so the file is the
    JSON of this value."""
    return {
        "server": {"public_url": SMOKE_URL},
        "database": {"url": dsn},
        "secrets": {"kek": {"provider": "local"}},
        "objectstore": {"driver": "fs", "fs": {"root": "/tmp/objects", "url": SMOKE_URL}},
        "vendors": {index: "http://127.0.0.1:1/" + index.removesuffix("_index")
                    for index in ("terraform_index", "opentofu_index", "terragrunt_index")},
    }


def host_port(output):
    """The host port of `docker port` output, such as 127.0.0.1:49153."""
    for line in output.splitlines():
        host, _, port = line.strip().rpartition(":")
        if host and port.isdigit():
            return int(port)
    raise ValueError("no published port: " + output)


def get(url):
    """The status and body of a GET of url; status 0 when nothing answers."""
    try:
        with urllib.request.urlopen(url, timeout=5) as response:  # noqa: S310 - a loopback URL this test builds
            return response.status, response.read()
    except urllib.error.HTTPError as error:
        return error.code, error.read()
    except (urllib.error.URLError, OSError):
        return 0, b""


def serve(reference, platform):
    """Start the server image with only --config, on a read-only root file
    system with a tmpfs /tmp, against a fresh PostgreSQL, and require
    /readyz on the admin port, the API listener's OAuth metadata naming
    server.public_url, and a clean exit on SIGTERM."""
    name = "infraweave-smoke-" + secrets.token_hex(6)
    password = secrets.token_hex(16)
    kek = base64.b64encode(secrets.token_bytes(32)).decode()
    image = IMAGES["server"]
    with tempfile.TemporaryDirectory(prefix="infraweave-smoke-") as directory:
        config = Path(directory) / "infraweave.yaml"
        config.write_text(json.dumps(server_config(
            f"postgres://postgres:{password}@{name}-db:5432/infraweave?sslmode=disable")))
        config.chmod(0o644)
        Path(directory).chmod(0o755)
        try:
            run("docker", "network", "create", name, capture=True)
            run("docker", "run", "-d", "--name", name + "-db", "--network", name, "-e", "POSTGRES_PASSWORD=" + password,
                "-e", "POSTGRES_DB=infraweave", postgres_image(), capture=True)
            wait(lambda: subprocess.run(["docker", "exec", name + "-db", "pg_isready", "-h", "127.0.0.1", "-U",
                                         "postgres", "-d", "infraweave"], capture_output=True).returncode == 0,
                 60, "PostgreSQL to accept connections")
            run("docker", "run", "-d", "--name", name, "--network", name, "--platform", platform, "--read-only",
                "--tmpfs", "/tmp", "--volume", f"{directory}:/etc/infraweave:ro", "--env", "INFRAWEAVE_SECRETS_KEK=" + kek,
                "--publish", f"127.0.0.1::{image.ports['api']}", "--publish", f"127.0.0.1::{image.ports['admin']}",
                reference, "--config", "/etc/infraweave/infraweave.yaml", capture=True)
            admin = host_port(run("docker", "port", name, f"{image.ports['admin']}/tcp", capture=True))
            api = host_port(run("docker", "port", name, f"{image.ports['api']}/tcp", capture=True))

            def ready():
                if not running(name):
                    raise ValueError("the server exited before it was ready")
                return get(f"http://127.0.0.1:{admin}/readyz")[0] == 200

            started = time.monotonic()
            wait(ready, READY_TIMEOUT, "/readyz on the admin port")
            print(f"{reference} ({platform}): ready in {time.monotonic() - started:.0f}s")
            status, body = get(f"http://127.0.0.1:{api}/.well-known/oauth-authorization-server")
            if status != 200 or json.loads(body).get("issuer", "").rstrip("/") != SMOKE_URL:
                raise ValueError(f"the API listener's OAuth metadata: {status} {body[:200]!r}")
            run("docker", "stop", "--timeout", "60", name, capture=True)
            code = json.loads(run("docker", "inspect", name, "--format", "{{json .State.ExitCode}}", capture=True))
            if code != 0:
                raise ValueError(f"the server exited {code} on SIGTERM")
        except (ValueError, subprocess.CalledProcessError):
            subprocess.run(["docker", "logs", "--tail", "200", name], check=False)
            raise
        finally:
            subprocess.run(["docker", "rm", "--force", "--volumes", name, name + "-db"], capture_output=True, check=False)
            subprocess.run(["docker", "network", "rm", name], capture_output=True, check=False)


def running(name):
    state = run("docker", "inspect", name, "--format", "{{.State.Running}}", capture=True)
    return state.strip() == "true"


def wait(condition, timeout, what):
    deadline = time.monotonic() + timeout
    while not condition():
        if time.monotonic() > deadline:
            raise ValueError(f"timed out after {timeout}s waiting for {what}")
        time.sleep(2)


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
    ctl.attestations(args.reference, {"recipe": recipe(args.context, args.kind), "source": args.revision or revision()})
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


def binary_sums(context):
    sums = {}
    for line in (Path(context) / "SHA256SUMS").read_text().splitlines():
        digest, name = line.split()
        sums["linux/" + name.rsplit("-", 1)[1]] = "sha256:" + digest
    return sums


def describe(version, commit, image, image_platforms, context, chart_source, chart_digest, chart_package, lock):
    """The worker artifact contract of one release."""
    if not DIGEST.match(image.partition("@")[2]) or not DIGEST.match(chart_digest):
        raise ValueError("artifacts must be pinned by digest")
    sums = binary_sums(context)
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


def describe_server(version, commit, image, image_platforms, context):
    """The server image contract of one release: the digest a deployment
    pins, such as test/terraform/platform/<cloud>'s image variable."""
    if not DIGEST.match(image.partition("@")[2]):
        raise ValueError("the server image must be pinned by digest: " + image)
    spec = IMAGES["server"]
    return {
        "schema": 1, "kind": "server-image", "version": version, "revision": commit,
        "image": image, "platforms": image_platforms, "binaries": binary_sums(context),
        "user": spec.user, "entrypoint": spec.entrypoint, "ports": spec.ports,
        "fips": {"build": "GOFIPS140=certified", "default_mode": "on"},
        "signature": {"identity": SERVER_SIGNER, "issuer": ISSUER},
    }


def server_manifest(args):
    value = describe_server(args.version, args.revision or revision(), args.image, platforms(args.image), args.context)
    Path(args.out).write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    commands = parser.add_subparsers(dest="command", required=True)
    cmd = commands.add_parser("binaries")
    cmd.add_argument("--version", required=True)
    cmd.add_argument("--out", required=True)
    cmd.set_defaults(func=binaries)
    cmd = commands.add_parser("build")
    cmd.add_argument("--kind", choices=sorted(IMAGES), default="worker")
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
    cmd.add_argument("--kind", choices=sorted(IMAGES), default="worker")
    cmd.add_argument("--platform", required=True)
    cmd.add_argument("--version", required=True)
    cmd.add_argument("--revision")
    cmd.set_defaults(func=smoke)
    cmd = commands.add_parser("verify")
    cmd.add_argument("reference")
    cmd.add_argument("--kind", choices=sorted(IMAGES), default="worker")
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
    cmd = commands.add_parser("server-manifest")
    cmd.add_argument("--version", required=True)
    cmd.add_argument("--revision")
    cmd.add_argument("--image", required=True)
    cmd.add_argument("--context", required=True)
    cmd.add_argument("--out", required=True)
    cmd.set_defaults(func=server_manifest)
    args = parser.parse_args(argv)
    try:
        args.func(args)
    except (ValueError, subprocess.CalledProcessError) as error:
        print(f"build.py {args.command}: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
