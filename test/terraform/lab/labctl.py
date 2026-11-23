#!/usr/bin/env python3
"""Terraform deployment batches for real infraweave acceptance tests."""

import argparse
import base64
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request
import ssl
import uuid

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[2]
ROOT = HERE / "services"
OWNER_LABEL = "io.infraweave.lab.owner"
OWNER_PATTERN = re.compile(r"iw-lab-[a-z0-9-]{1,60}\Z")
TERRAFORM_TIMEOUT = 600


def write_json(path, value):
    with open(path, "w", opener=lambda name, flags: os.open(name, flags, 0o600)) as output:
        json.dump(value, output, indent=2)
        output.write("\n")


def read_json(path):
    return json.loads(path.read_text())


def output(*args, **kwargs):
    kwargs.setdefault("timeout", 60)
    return subprocess.check_output(args, text=True, **kwargs).strip()


def batch(name):
    if not re.fullmatch(r"[a-z0-9-]+", name):
        raise ValueError("invalid batch name")
    result = read_json(HERE / "batches" / (name + ".json"))
    selection = {"partition", "tags", "features"}
    # Managed cloud batches add their clouds and reviewed bounds, which
    # cloudlab.definition validates.
    if set(result) not in (selection, selection | {"clouds", "bounds"}) or \
            not all(isinstance(result[key], str) for key in selection):
        raise ValueError("invalid batch definition")
    return result


def cloud_batch(definition):
    return "clouds" in definition


class Platform:
    """The labctl.run hooks for a local platform batch."""

    def __init__(self, args):
        self.args = args

    def up(self, directory, owner):
        return up(directory, self.args.batch, self.args.terraform, self.args.helper, owner)

    def down(self, directory):
        return down(directory)

    def environment(self, directory):
        return {"INFRAWEAVE_LAB_MANIFEST": str(directory / "acceptance.json")}

    def diagnostics(self, directory):
        retain_diagnostics(directory)

    def record(self, directory, result):
        """Local batches publish result.json itself; it names no cloud account."""


def lifecycle(definition, args):
    if not cloud_batch(definition):
        return Platform(args)
    import cloudlab
    return cloudlab.Lifecycle(args)


def pins():
    source = (REPO / "test/deps/images.go").read_text()
    result = {}
    for key, constant in (("postgres_image", "ImagePostgres"), ("objectstore_image", "ImageObjectStore"), ("gitea_image", "ImageGitea"), ("k3s_image", "ImageK3s")):
        match = re.search(r"\b" + constant + r'\s*=\s*"([^\"]+@sha256:[0-9a-f]{64})"', source)
        if not match:
            raise ValueError("missing immutable service image: " + constant)
        result[key] = match[1]
    return result


def environment(directory):
    env = dict(os.environ, TF_IN_AUTOMATION="1", TF_DATA_DIR=str(directory / "terraform-data"))
    # Match the Docker CLI's selected daemon, including Docker Desktop contexts.
    if not env.get("DOCKER_HOST"):
        env["DOCKER_HOST"] = output("docker", "context", "inspect", "--format", "{{.Endpoints.docker.Host}}")
    return env


def terraform(directory, executable, *args, capture=False):
    command = [executable, "-chdir=" + str(ROOT), *args]
    with open(directory / "terraform.log", "a", opener=lambda name, flags: os.open(name, flags, 0o600)) as log:
        with subprocess.Popen(command, env=environment(directory), text=True, start_new_session=True,
                              stdout=subprocess.PIPE if capture else log, stderr=log) as process:
            try:
                stdout, _stderr = process.communicate(timeout=TERRAFORM_TIMEOUT)
            except BaseException:
                terminate_process_group(process)
                raise
    if process.returncode:
        raise RuntimeError("Terraform " + args[0] + " failed; private diagnostics retained")
    return stdout if capture else None


def owned_resources(owner):
    if not OWNER_PATTERN.fullmatch(owner):
        raise ValueError("invalid deployment owner")
    result = {}
    for kind, args in (("containers", ("ps", "-aq")), ("volumes", ("volume", "ls", "-q")), ("networks", ("network", "ls", "-q"))):
        result[kind] = output("docker", *args, "--filter", "label=" + OWNER_LABEL + "=" + owner).splitlines()
    return result


def absent(resources):
    return all(not items for items in resources.values())


def ready(directory, manifest):
    targets = [(manifest["objectstore"]["endpoint"] + "/health/ready", None),
               (manifest["gitea"]["url"] + "/api/healthz", ssl.create_default_context(cafile=manifest["gitea"]["caFile"]))]
    deadline = time.monotonic() + 90
    for url, context in targets:
        while True:
            try:
                with urllib.request.urlopen(url, context=context, timeout=2) as response:
                    if response.status == 200:
                        break
            except urllib.error.HTTPError as error:
                error.close()
            except (urllib.error.URLError, TimeoutError, ConnectionError):
                pass
            if time.monotonic() >= deadline:
                raise RuntimeError("Terraform lab services did not become ready")
            time.sleep(0.25)
    container = manifest["postgres"]["container"]
    while subprocess.run(["docker", "exec", container, "pg_isready", "-U", "infraweave", "-d", "infraweave"],
                         stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=5).returncode:
        if time.monotonic() >= deadline:
            raise RuntimeError("Terraform PostgreSQL did not become ready")
        time.sleep(0.25)
    with open(directory / "bootstrap.log", "w", opener=lambda name, flags: os.open(name, flags, 0o600)) as log:
        subprocess.run(["docker", "exec", "--user", "git", manifest["gitea"]["container"],
                        "gitea", "--config", "/data/gitea/conf/app.ini", "admin", "user", "create", "--admin",
                        "--username", "infraweave", "--password", "infraweave-gitea",
                        "--email", "infraweave@example.invalid", "--must-change-password=false"],
                       check=True, stdout=log, stderr=log, timeout=30)


def verify_state_owner(directory, config):
    state = json.loads(terraform(directory, config["terraform"], "show", "-json", capture=True))
    for resource in state.get("values", {}).get("root_module", {}).get("resources", []):
        if resource["type"] not in ("docker_container", "docker_volume", "docker_network"):
            continue
        labels = {entry["label"]: entry["value"] for entry in resource["values"].get("labels", [])}
        if labels.get(OWNER_LABEL) != config["owner"]:
            raise RuntimeError("Terraform state ownership differs from the cleanup journal; refusing destroy")


class RefuseRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, _request, _file, _code, _message, _headers, _url):
        return None


def kubernetes_opener(context):
    return urllib.request.build_opener(urllib.request.HTTPSHandler(context=context), RefuseRedirect())


def kubernetes_credentials(raw, cluster):
    if len(raw) > 65536:
        raise ValueError("Kubernetes kubeconfig exceeds 64 KiB")
    config = json.loads(raw)

    def first(collection, field):
        entries = config.get(collection) if isinstance(config, dict) else None
        if not isinstance(entries, list) or not entries or not isinstance(entries[0], dict):
            raise RuntimeError("Kubernetes bootstrap credentials are incomplete")
        value = entries[0].get(field)
        if not isinstance(value, dict):
            raise RuntimeError("Kubernetes bootstrap credentials are incomplete")
        return value

    connection = first("clusters", "cluster")
    identity = first("users", "user")
    context = first("contexts", "context")
    names = {kind: config[kind][0].get("name") for kind in ("clusters", "contexts", "users")}
    if any(not isinstance(name, str) or not name for name in names.values()):
        raise RuntimeError("Kubernetes bootstrap credentials are incomplete")
    # Export the same CA/client identity that the readiness probes validate.
    if (context.get("cluster") != names["clusters"] or context.get("user") != names["users"] or
            config.get("current-context") != names["contexts"]):
        raise RuntimeError("Kubernetes bootstrap credentials are incomplete")
    encoded = {"ca": connection.get("certificate-authority-data"),
               "cert": identity.get("client-certificate-data"), "key": identity.get("client-key-data")}
    certificates = {}
    for name, value in encoded.items():
        if not isinstance(value, str) or not value:
            raise RuntimeError("Kubernetes bootstrap credentials are incomplete")
        try:
            certificates[name] = base64.b64decode(value, validate=True)
        except ValueError:
            raise RuntimeError("Kubernetes bootstrap credentials are incomplete") from None
        if not certificates[name]:
            raise RuntimeError("Kubernetes bootstrap credentials are incomplete")
    # Bootstrap may return an empty kubeconfig before k3s writes its identity.
    # Validate every required field before writing any private credential file.
    connection["server"] = cluster["endpoint"]
    context["namespace"] = cluster["namespace"]
    return config, certificates


def kubernetes_ready(directory, manifest):
    cluster = manifest["kubernetes"]
    owner = manifest["owner"]
    inspected = json.loads(output("docker", "inspect", cluster["container"]))
    if len(inspected) != 1:
        raise RuntimeError("Kubernetes container inspection is incomplete")
    labels = inspected[0].get("Config", {}).get("Labels", {})
    if any(labels.get(key) != value for key, value in {
        OWNER_LABEL: owner, "io.infraweave.lab.batch": manifest["batch"],
        "io.infraweave.lab.managed-by": "terraform", "io.infraweave.lab.service": "kubernetes",
    }.items()):
        raise RuntimeError("Kubernetes container ownership mismatch; refusing credential export")
    deadline = time.monotonic() + 180
    while True:
        try:
            raw = subprocess.check_output(["docker", "exec", cluster["container"], "kubectl", "config", "view", "--raw", "-o", "json"],
                                          stderr=subprocess.DEVNULL, timeout=5)
            config, certificates = kubernetes_credentials(raw, cluster)
            for name, value in certificates.items():
                path = directory / "pki" / ("kubernetes-" + name + ".pem")
                with open(path, "wb", opener=lambda name, flags: os.open(name, flags, 0o600)) as file:
                    file.write(value)
            context = ssl.create_default_context(cafile=str(directory / "pki/kubernetes-ca.pem"))
            context.load_cert_chain(directory / "pki/kubernetes-cert.pem", directory / "pki/kubernetes-key.pem")
            opener = kubernetes_opener(context)
            with opener.open(cluster["endpoint"] + "/readyz", timeout=3) as response:
                if response.status != 200:
                    raise RuntimeError("Kubernetes readiness refused")
            with opener.open(cluster["endpoint"] + "/api/v1/nodes", timeout=3) as response:
                nodes = json.load(response)["items"]
            if len(nodes) != 1 or not any(c["type"] == "Ready" and c["status"] == "True" for c in nodes[0]["status"]["conditions"]):
                raise RuntimeError("Kubernetes node is not Ready")
            with opener.open(cluster["endpoint"] + "/api/v1/namespaces/" + cluster["namespace"], timeout=3) as response:
                namespace = json.load(response)
            if namespace["metadata"].get("annotations", {}).get(OWNER_LABEL) != owner:
                raise ValueError("Kubernetes namespace ownership mismatch")
            write_json(Path(cluster["kubeconfig"]), config)
            return
        except urllib.error.HTTPError as error:
            error.close()
            if time.monotonic() >= deadline:
                raise RuntimeError("Terraform Kubernetes did not become ready within 180 seconds") from None
            time.sleep(0.5)
        except (KeyError, IndexError, json.JSONDecodeError, subprocess.CalledProcessError, subprocess.TimeoutExpired,
                urllib.error.URLError, TimeoutError, ConnectionError, OSError, RuntimeError):
            if time.monotonic() >= deadline:
                raise RuntimeError("Terraform Kubernetes did not become ready within 180 seconds") from None
            time.sleep(0.5)


def up(directory, name, executable, helper, owner):
    batch(name)
    directory.mkdir(mode=0o700, parents=True, exist_ok=False)
    config = {"schema": 1, "owner": owner, "batch": name, "terraform": executable}
    write_json(directory / "deployment.json", config)
    subprocess.run([helper, "pki", "--directory", str(directory / "pki")], check=True, timeout=30)
    variables = dict(pins(), owner=owner, batch=name, pki_directory=str(directory / "pki"),
                     kubernetes_enabled=name == "kubernetes")
    write_json(directory / "variables.tfvars.json", variables)
    terraform(directory, executable, "init", "-input=false", "-no-color", "-lockfile=readonly",
              "-backend-config=path=" + str(directory / "terraform.tfstate"))
    terraform(directory, executable, "plan", "-input=false", "-no-color", "-var-file=" + str(directory / "variables.tfvars.json"),
              "-out=" + str(directory / "deployment.tfplan"))
    terraform(directory, executable, "apply", "-input=false", "-no-color", str(directory / "deployment.tfplan"))
    manifest = json.loads(terraform(directory, executable, "output", "-json", "acceptance", capture=True))
    write_json(directory / "acceptance.json", manifest)
    ready(directory, manifest)
    if "kubernetes" in manifest:
        kubernetes_ready(directory, manifest)
    write_json(directory / "readiness.json", {"passed": True, "resources": owned_resources(owner)})
    return manifest


def down(directory):
    config = read_json(directory / "deployment.json")
    owner = config["owner"]
    before = owned_resources(owner)
    state = ""
    if (directory / "terraform.tfstate").exists():
        state = terraform(directory, config["terraform"], "state", "list", capture=True).strip()
        if state:
            verify_state_owner(directory, config)
            terraform(directory, config["terraform"], "destroy", "-input=false", "-auto-approve", "-no-color",
                      "-var-file=" + str(directory / "variables.tfvars.json"))
            state = terraform(directory, config["terraform"], "state", "list", capture=True).strip()
    after = owned_resources(owner)
    result = {"owner": owner, "before": before, "after": after, "emptyState": not state,
              "passed": not state and absent(after)}
    if result["passed"]:
        for relative in ("pki/gitea-key.pem", "pki/kubernetes.json", "pki/kubernetes-key.pem",
                         "pki/kubernetes-cert.pem", "pki/kubernetes-ca.pem", "deployment.tfplan", "terraform.tfstate.backup"):
            (directory / relative).unlink(missing_ok=True)
        result["privateInputsRemoved"] = True
    write_json(directory / "cleanup.json", result)
    if not result["passed"]:
        raise RuntimeError("Terraform lab cleanup could not verify absence; deployment journal retained")
    return result


def census(path):
    report = read_json(path)
    scenarios = [case for feature in report for case in feature.get("elements", []) if case.get("type") == "scenario"]
    steps = [step for case in scenarios for step in case.get("steps", [])]
    if not scenarios or not steps or any(not case.get("steps") for case in scenarios) or any(s.get("result", {}).get("status") != "passed" for s in steps):
        raise RuntimeError("Acceptance must execute nonempty scenarios with every step passed")
    return {"scenarios": len(scenarios), "steps": len(steps), "passed": True}


def terraform_version(executable):
    version = json.loads(output(executable, "version", "-json"))["terraform_version"]
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", version):
        raise ValueError("the control-plane Terraform must be an official stable release")
    return version


def retain_diagnostics(directory):
    source = directory / "terraform.log"
    if not source.exists():
        return
    text = source.read_text()
    key = directory / "pki/gitea-key.pem"
    if key.exists():
        value = key.read_text()
        for representation in (value, json.dumps(value)[1:-1], base64.b64encode(value.encode()).decode()):
            text = text.replace(representation, "[redacted lab private key]")
    text = re.sub(r"-----BEGIN [^-]*PRIVATE KEY-----.*?-----END [^-]*PRIVATE KEY-----",
                  "[redacted lab private key]", text, flags=re.DOTALL)
    evidence = directory / "evidence"
    evidence.mkdir(mode=0o700, exist_ok=True)
    with open(evidence / "terraform.log", "w", opener=lambda name, flags: os.open(name, flags, 0o600)) as log:
        log.write(text)


def terminate_process_group(process, grace=30, kill_grace=10):
    # A provider can outlive Terraform itself. Wait for the entire private
    # session to stop before a caller starts destroy against the same state.
    for action, budget in ((signal.SIGTERM, grace), (signal.SIGKILL, kill_grace)):
        try:
            os.killpg(process.pid, action)
        except ProcessLookupError:
            process.wait(timeout=kill_grace)
            return
        deadline = time.monotonic() + budget
        while time.monotonic() < deadline:
            process.poll()
            try:
                os.killpg(process.pid, 0)
            except ProcessLookupError:
                process.wait(timeout=kill_grace)
                return
            except PermissionError:
                # Darwin can refuse a group probe while its leader exits.
                # Keep polling; refusal is not evidence that the group is gone.
                pass
            time.sleep(0.05)
    raise RuntimeError("The owned command process group did not terminate")


def stream_command(command, cwd, env, log):
    with subprocess.Popen(command, cwd=cwd, env=env, text=True, start_new_session=True,
                          stdout=subprocess.PIPE, stderr=subprocess.STDOUT) as process:
        try:
            for line in process.stdout:
                log.write(line)
                print(line, end="", flush=True)
            return process.wait()
        except BaseException:
            terminate_process_group(process)
            raise


def run(args):
    directory = Path(args.directory).resolve()
    definition = batch(args.batch)
    features = args.features or definition["features"]
    feature_path = (REPO / "test/acceptance" / features).resolve()
    if not feature_path.is_relative_to(REPO / "test/acceptance/features") or not feature_path.exists():
        raise ValueError("acceptance features must exist under test/acceptance/features")
    owner = "iw-lab-" + uuid.uuid4().hex
    deployment = lifecycle(definition, args)
    result = {"batch": args.batch, "revision": output("git", "-C", str(REPO), "rev-parse", "HEAD"),
              "dirty": bool(output("git", "-C", str(REPO), "status", "--porcelain")), "passed": False,
              "terraformVersion": terraform_version(args.terraform)}
    try:
        deployment.up(directory, owner)
        evidence = directory / "evidence"
        coverage = directory / "coverage"
        evidence.mkdir(mode=0o700, exist_ok=True); coverage.mkdir()
        tags = definition["tags"]
        if args.tags:
            tags = "(" + tags + ") and (" + args.tags + ")" if tags else args.tags
        env = dict(os.environ, GOWORK="off", INFRAWEAVE_ACCEPT_REQUIRE_DOCKER="1", **deployment.environment(directory),
                   INFRAWEAVE_ACCEPT_PARTITION=definition["partition"], INFRAWEAVE_ACCEPT_TAGS=tags,
                   INFRAWEAVE_ACCEPT_FEATURES=features, INFRAWEAVE_ACCEPT_CUCUMBER=str(evidence / "cucumber.json"),
                   INFRAWEAVE_ACCEPT_OUT=str(evidence))
        env.pop("INFRAWEAVE_ACCEPT_BINARY", None)
        if args.fips:
            env.update(GOFIPS140="certified", GODEBUG="fips140=only")
        command = ["go", "test", "-v", "-count=1", "-run", "^TestAccFeatures$", "-timeout=20m",
                   "-covermode=atomic", "-cover", "-coverpkg=github.com/weaveplatform/infraweave/...",
                   "./test/acceptance", "-args", "-test.gocoverdir=" + str(coverage)]
        result.update(features=features, tags=tags, partition=definition["partition"], fips=args.fips,
                      images=pins(), opa=output("go", "list", "-m", "-f", "{{.Version}}", "github.com/open-policy-agent/opa", cwd=REPO))
        with (evidence / "acceptance.log").open("w") as log:
            status = stream_command(command, cwd=REPO, env=env, log=log)
        if status:
            raise RuntimeError("Acceptance failed; complete evidence retained in " + str(evidence))
        result.update(census(evidence / "cucumber.json"))
    except BaseException as error:
        result["error"] = type(error).__name__ + ": " + str(error)
        if (directory / "deployment.json").exists() and read_json(directory / "deployment.json")["owner"] == owner:
            deployment.diagnostics(directory)
        raise
    finally:
        if (directory / "deployment.json").exists() and read_json(directory / "deployment.json")["owner"] == owner:
            try:
                result["cleanup"] = deployment.down(directory)
            except BaseException as error:
                result["passed"] = False
                result["cleanupError"] = type(error).__name__ + ": " + str(error)
                deployment.diagnostics(directory)
                raise
            finally:
                write_json(directory / "result.json", result)
                deployment.record(directory, result)
    return result


def main():
    parser = argparse.ArgumentParser(__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    for verb in ("up", "run", "preflight"):
        cmd = commands.add_parser(verb)
        cmd.add_argument("--batch", required=True)
        # Managed cloud batches read one private operator inputs file per cloud.
        cmd.add_argument("--inputs", action="append", metavar="CLOUD=PATH")
        if verb == "preflight":
            continue
        cmd.add_argument("--directory", required=True)
        cmd.add_argument("--terraform", default="terraform")
        cmd.add_argument("--helper", default="infraweave-lab")
        if verb == "run":
            cmd.add_argument("--tags")
            cmd.add_argument("--features")
            cmd.add_argument("--fips", action="store_true")
    cmd = commands.add_parser("down")
    cmd.add_argument("--directory", required=True)
    args = parser.parse_args()
    if args.command == "run":
        result = run(args)
    elif args.command == "preflight":
        import cloudlab
        result, _configs, _definition = cloudlab.preflight(args.batch, cloudlab.parse_inputs(args.inputs))
        print(json.dumps(result))
        if not result["passed"]:
            raise cloudlab.preflight_error(result)
        return
    elif args.command == "up":
        directory = Path(args.directory).resolve()
        owner = "iw-lab-" + uuid.uuid4().hex
        deployment = lifecycle(batch(args.batch), args)
        try:
            result = deployment.up(directory, owner)
        except BaseException:
            if (directory / "deployment.json").exists() and read_json(directory / "deployment.json")["owner"] == owner:
                deployment.down(directory)
            raise
    else:
        directory = Path(args.directory).resolve()
        if read_json(directory / "deployment.json").get("kind") == "managed-cloud-workers":
            import cloudlab
            result = cloudlab.down(directory)
        else:
            result = down(directory)
    print(json.dumps(result))


if __name__ == "__main__":
    def terminated(signum, _frame):
        raise SystemExit(128 + signum)
    signal.signal(signal.SIGTERM, terminated)
    try:
        main()
    except (ValueError, RuntimeError, OSError, subprocess.CalledProcessError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
