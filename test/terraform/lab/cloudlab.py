"""Managed cloud worker batches: owned per-cloud Terraform stages beside one lab control plane.

Each selected cloud applies its committed `test/terraform/platform/workers/<cloud>`
`cluster/` then `deployment/` root with an independent private state, provider
directory, variables file and log. The local platform deployment of labctl.py
supplies the single application control plane. Required clouds are never
skipped: missing roots, inputs, credentials, quota or an inaccessible cluster
fail preflight before any billable apply.
"""

import concurrent.futures
import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import socket
import ssl
import subprocess
import threading
import time
import urllib.parse

import labctl as lab

CLOUDS = ("aws", "azure", "google")
ROOTS = lab.REPO / "test/terraform/platform/workers"
STAGES = ("cluster", "deployment")
STAGE_OUTPUTS = {"cluster": "cluster", "deployment": "hosting"}
IDENTIFIERS = {
    "aws": {"account_id": r"[0-9]{12}", "region": r"[a-z]{2}(-[a-z]+)+-[0-9]"},
    "azure": {"tenant_id": r"[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}",
              "subscription_id": r"[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}",
              "location": r"[a-z0-9]{2,40}", "resource_group_name": r"[A-Za-z0-9._()-]{1,90}"},
    "google": {"project_id": r"[a-z][a-z0-9-]{4,28}[a-z0-9]", "project_number": r"[0-9]{6,20}",
               "location": r"[a-z]+-[a-z]+[0-9]"},
}
# Account-naming values never reach published evidence.
REDACTED = ("account_id", "tenant_id", "subscription_id", "project_id", "project_number")
# Google label keys admit no dots, so its roots encode the owner key with
# underscores; every other provider keeps the lab's Docker label key.
OWNER_KEYS = (lab.OWNER_LABEL, "io_infraweave_lab_owner")
# Inclusive ranges a committed batch definition may request. They cap what a
# reviewer approves before billable provisioning.
BOUND_LIMITS = {"parallelism": (1, 3), "ttl_minutes": (30, 720), "vcpus": (2, 64), "pool_capacity": (1, 4),
                "provision_seconds": (60, 5400), "readiness_seconds": (5, 1800), "cleanup_seconds": (60, 5400)}
INPUT_LIMIT = 65536
COMMAND_TIMEOUT = 60
READBACK_INTERVAL = 15
PRIVATE_KEY = re.compile(r"-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----")
DIGEST = re.compile(r"[^@\s]+@sha256:[0-9a-f]{64}")
LABEL = re.compile(r"[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?")
ULID = re.compile(r"[0-9A-HJKMNP-TV-Z]{26}")
LIFECYCLES = ("cluster", "deployment", "setup", "external")
POOL_MODES = ("Managed", "Existing", "Public")
PRIVATE_FILES = ("deployment.tfplan", "terraform.tfstate.backup", "variables.tfvars.json")


class Unavailable(RuntimeError):
    """A cloud command could not answer; its outcome is unknown, never absent."""


def definition(name):
    """Return the validated cloud batch definition and its content digest."""
    result = lab.batch(name)
    clouds, bounds = result.get("clouds"), result.get("bounds")
    if (not isinstance(clouds, list) or not clouds or len(set(clouds)) != len(clouds) or
            any(cloud not in CLOUDS for cloud in clouds)):
        raise ValueError("a cloud batch must select distinct known clouds")
    if not isinstance(bounds, dict) or set(bounds) != set(BOUND_LIMITS):
        raise ValueError("a cloud batch must declare every resource, cost and time bound")
    for key, (low, high) in BOUND_LIMITS.items():
        value = bounds[key]
        if type(value) is not int or not low <= value <= high:
            raise ValueError("cloud batch bound " + key + " is outside " + str(low) + ".." + str(high))
    if bounds["parallelism"] > len(clouds):
        raise ValueError("cloud batch parallelism exceeds its clouds")
    digest = hashlib.sha256((lab.HERE / "batches" / (name + ".json")).read_bytes()).hexdigest()
    return dict(result, digest=digest)


def parse_inputs(values):
    """Map repeated CLOUD=/absolute/path arguments to their private input files."""
    result = {}
    for value in values or ():
        cloud, separator, path = value.partition("=")
        if not separator or cloud not in CLOUDS or cloud in result or not os.path.isabs(path):
            raise ValueError("--inputs takes one CLOUD=/absolute/path per known cloud")
        result[cloud] = Path(path)
    return result


def exact(value, keys, where):
    if not isinstance(value, dict) or set(value) != set(keys):
        raise ValueError(where + " must have exactly " + ", ".join(sorted(keys)))
    return value


def text(value, where, pattern=None):
    if not isinstance(value, str) or not value or (pattern and not re.fullmatch(pattern, value)):
        raise ValueError(where + " is missing or malformed")
    return value


def read_inputs(cloud, path, bounds):
    """Read one cloud's operator inputs, which name accounts but hold no secret."""
    if path is None:
        raise ValueError("no private inputs file was supplied")
    try:
        status = path.stat()
    except OSError:
        raise ValueError("the private inputs file is unreadable") from None
    if not path.is_file() or status.st_mode & 0o077 or status.st_size > INPUT_LIMIT:
        raise ValueError("the inputs file must be a private (0600) regular file of at most 64 KiB")
    raw = path.read_text()
    if PRIVATE_KEY.search(raw):
        raise ValueError("the inputs file carries private key material; reference Secrets instead")
    try:
        inputs = json.loads(raw)
    except json.JSONDecodeError:
        raise ValueError("the inputs file is not JSON") from None
    exact(inputs, ("cloud", "cluster", "deployment"), "inputs")
    provider = exact(inputs["cloud"], ("provider", "identifiers"), "inputs.cloud")
    if provider["provider"] != cloud:
        raise ValueError("inputs.cloud.provider names another cloud")
    identifiers = exact(provider["identifiers"], IDENTIFIERS[cloud], "inputs.cloud.identifiers")
    for key, pattern in IDENTIFIERS[cloud].items():
        text(identifiers[key], "inputs.cloud.identifiers." + key, pattern)
    for stage in STAGES:
        if not isinstance(inputs[stage], dict):
            raise ValueError("inputs." + stage + " must be an object of stage variables")
        reserved = {"ownership", "cloud", "cluster_stage"} & set(inputs[stage])
        if reserved:
            raise ValueError("inputs." + stage + " may not set coordinator variables " + ", ".join(sorted(reserved)))
    deployment = inputs["deployment"]
    cluster = deployment.get("cluster")
    if not isinstance(cluster, dict) or cluster.get("mode") not in ("create", "existing"):
        raise ValueError("inputs.deployment.cluster.mode must be create or existing")
    text(cluster.get("name"), "inputs.deployment.cluster.name", r"[A-Za-z0-9][A-Za-z0-9_-]{0,62}")
    if (cluster["mode"] == "existing") != isinstance(cluster.get("existing_id"), str):
        raise ValueError("inputs.deployment.cluster.existing_id is required for, and only for, existing mode")
    pools = deployment.get("pools")
    if not isinstance(pools, list) or not pools or any(not isinstance(p, dict) or type(p.get("capacity")) is not int or
                                                         p["capacity"] < 0 for p in pools):
        raise ValueError("inputs.deployment.pools must list pools with explicit capacity")
    if sum(pool["capacity"] for pool in pools) > bounds["pool_capacity"]:
        raise ValueError("requested pool capacity exceeds the batch bound of " + str(bounds["pool_capacity"]))
    return inputs


def cloud_command(*args, timeout=COMMAND_TIMEOUT):
    """Run a provider CLI; a missing CLI or timeout is an unknown outcome."""
    try:
        return subprocess.run(args, capture_output=True, text=True, timeout=timeout, stdin=subprocess.DEVNULL)
    except (FileNotFoundError, PermissionError):
        raise Unavailable(args[0] + " CLI is not installed") from None
    except subprocess.TimeoutExpired:
        raise Unavailable(args[0] + " did not answer within " + str(timeout) + " seconds") from None


def cloud_json(*args):
    completed = cloud_command(*args)
    if completed.returncode:
        raise Unavailable(args[0] + " " + args[1] + " refused the request")
    try:
        return json.loads(completed.stdout)
    except json.JSONDecodeError:
        raise Unavailable(args[0] + " " + args[1] + " answered malformed JSON") from None


def verify_identity(cloud, ids):
    """Fail closed unless the caller's credentials resolve to the explicit IDs."""
    try:
        if cloud == "aws":
            matched = cloud_json("aws", "sts", "get-caller-identity", "--output", "json").get("Account") == ids["account_id"]
        elif cloud == "azure":
            account = cloud_json("az", "account", "show", "--subscription", ids["subscription_id"], "--output", "json")
            matched = account.get("tenantId") == ids["tenant_id"] and account.get("id") == ids["subscription_id"]
        else:
            project = cloud_json("gcloud", "projects", "describe", ids["project_id"], "--format=json")
            matched = project.get("projectNumber") == ids["project_number"] and project.get("lifecycleState") == "ACTIVE"
    except Unavailable as error:
        raise ValueError("credentials are missing or cannot be verified: " + str(error)) from None
    except AttributeError:
        raise ValueError("credentials could not be verified: the identity answer was incomplete") from None
    if not matched:
        raise ValueError("credentials resolve to a different account than the explicit inputs")


def verify_quota(cloud, ids, vcpus, create):
    """Require headroom for the batch's vCPU bound and, on AWS, one more cluster."""
    clusters = 1
    try:
        if cloud == "aws":
            region = ("--region", ids["region"], "--output", "json")
            # Running On-Demand Standard instance vCPUs and EKS clusters per Region.
            # ref: https://docs.aws.amazon.com/eks/latest/userguide/service-quotas.html
            headroom = cloud_json("aws", "service-quotas", "get-service-quota", "--service-code", "ec2",
                                  "--quota-code", "L-1216C47A", *region)["Quota"]["Value"]
            if create:
                limit = cloud_json("aws", "service-quotas", "get-service-quota", "--service-code", "eks",
                                   "--quota-code", "L-1194D53C", *region)["Quota"]["Value"]
                clusters = limit - len(cloud_json("aws", "eks", "list-clusters", *region)["clusters"])
        elif cloud == "azure":
            usage = cloud_json("az", "vm", "list-usage", "--location", ids["location"],
                               "--subscription", ids["subscription_id"], "--output", "json")
            cores = [entry for entry in usage if entry.get("name", {}).get("value") == "cores"]
            headroom = int(cores[0]["limit"]) - int(cores[0]["currentValue"]) if len(cores) == 1 else None
        else:
            region = cloud_json("gcloud", "compute", "regions", "describe", ids["location"],
                                "--project", ids["project_id"], "--format=json")
            cpus = [entry for entry in region.get("quotas", []) if entry.get("metric") == "CPUS"]
            headroom = cpus[0]["limit"] - cpus[0]["usage"] if len(cpus) == 1 else None
    except Unavailable as error:
        raise ValueError("quota could not be read: " + str(error)) from None
    except (KeyError, TypeError, ValueError, AttributeError):
        raise ValueError("quota could not be read: the answer was incomplete") from None
    if headroom is None:
        raise ValueError("quota could not be read: no regional vCPU quota was reported")
    if clusters < 1:
        raise ValueError("the EKS cluster quota has no headroom")
    if headroom < vcpus:
        raise ValueError("vCPU quota headroom is below the batch bound of " + str(vcpus))


def cluster_state(cloud, ids, name, existing_id=None):
    """Return ready, present or absent for one exact cluster, or raise Unavailable."""
    if cloud == "aws":
        completed = cloud_command("aws", "eks", "describe-cluster", "--name", name, "--region", ids["region"], "--output", "json")
        missing = ("ResourceNotFoundException",)
    elif cloud == "azure":
        completed = cloud_command("az", "aks", "show", "--ids", existing_id or cluster_id(cloud, ids, name), "--output", "json")
        missing = ("ResourceNotFound", "ResourceGroupNotFound")
    else:
        completed = cloud_command("gcloud", "container", "clusters", "describe", name, "--location", ids["location"],
                                  "--project", ids["project_id"], "--format=json")
        missing = ("NOT_FOUND", "Not found")
    if completed.returncode:
        if any(marker in completed.stderr for marker in missing):
            return "absent"
        raise Unavailable(cloud + " cluster readback was refused")
    try:
        body = json.loads(completed.stdout)
        ready = {"aws": body.get("cluster", {}).get("status") == "ACTIVE",
                 "azure": body.get("provisioningState") == "Succeeded" and body.get("powerState", {}).get("code") == "Running",
                 "google": body.get("status") == "RUNNING"}[cloud]
    except (json.JSONDecodeError, AttributeError):
        raise Unavailable(cloud + " cluster readback answered malformed JSON") from None
    return "ready" if ready else "present"


def cluster_id(cloud, ids, name):
    if cloud == "aws":
        return "arn:aws:eks:" + ids["region"] + ":" + ids["account_id"] + ":cluster/" + name
    if cloud == "azure":
        return ("/subscriptions/" + ids["subscription_id"] + "/resourceGroups/" + ids["resource_group_name"] +
                "/providers/Microsoft.ContainerService/managedClusters/" + name)
    return "projects/" + ids["project_id"] + "/locations/" + ids["location"] + "/clusters/" + name


def owner_search(cloud, ids, owner):
    """List cloud resources still carrying the owner's tag or label."""
    if cloud == "aws":
        found = cloud_json("aws", "resourcegroupstaggingapi", "get-resources", "--region", ids["region"],
                           "--tag-filters", "Key=" + lab.OWNER_LABEL + ",Values=" + owner, "--output", "json")
        return [entry["ResourceARN"] for entry in found.get("ResourceTagMappingList", [])]
    if cloud == "azure":
        tag = ("--subscription", ids["subscription_id"], "--tag", lab.OWNER_LABEL + "=" + owner, "--output", "json")
        return [entry["id"] for entry in cloud_json("az", "resource", "list", *tag) + cloud_json("az", "group", "list", *tag)]
    found = cloud_json("gcloud", "asset", "search-all-resources", "--scope=projects/" + ids["project_id"],
                       "--query=labels." + OWNER_KEYS[1] + "=" + owner, "--format=json")
    return [entry["name"] for entry in found]


def preflight(name, inputs):
    """Check every required cloud before any mutation and report all problems."""
    batch = definition(name)
    bounds = batch["bounds"]
    review = {"schema": 1, "batch": name, "definitionSHA256": batch["digest"], "bounds": bounds,
              "clouds": {}, "passed": False}
    configs = {}
    for cloud in batch["clouds"]:
        finding = {"root": False, "inputs": False, "credentials": False, "quota": False, "cluster": False, "problems": []}
        review["clouds"][cloud] = finding
        root = ROOTS / cloud
        missing = [stage for stage in STAGES
                   if not (root / stage / "versions.tf").is_file() or not (root / stage / ".terraform.lock.hcl").is_file()]
        if missing:
            finding["problems"].append("committed root stages with provider locks are missing: " + ", ".join(missing))
        else:
            finding["root"] = True
        try:
            config = read_inputs(cloud, inputs.get(cloud), bounds)
            finding["inputs"] = True
            ids = config["cloud"]["identifiers"]
            cluster = config["deployment"]["cluster"]
            verify_identity(cloud, ids)
            finding["credentials"] = True
            verify_quota(cloud, ids, bounds["vcpus"], cluster["mode"] == "create")
            finding["quota"] = True
            if cluster["mode"] == "existing":
                try:
                    state = cluster_state(cloud, ids, cluster["name"], cluster["existing_id"])
                except Unavailable as error:
                    raise ValueError("the existing cluster is inaccessible: " + str(error)) from None
                if state != "ready":
                    raise ValueError("the existing cluster is not accessible and ready")
            finding["cluster"] = True
            configs[cloud] = config
        except ValueError as error:
            finding["problems"].append(str(error))
    for cloud in sorted(set(inputs) - set(batch["clouds"])):
        review["clouds"][cloud] = {"problems": ["inputs name a cloud this batch does not select"]}
    review["passed"] = all(not finding["problems"] for finding in review["clouds"].values())
    return review, configs, batch


def preflight_error(review):
    problems = [cloud + ": " + problem for cloud, finding in review["clouds"].items() for problem in finding["problems"]]
    return RuntimeError("Managed cloud preflight failed; no cloud was provisioned. " + "; ".join(problems))


def redact(value, journal):
    """Replace account-naming identifiers before evidence can be published."""
    rendered = json.dumps(value, indent=2) if not isinstance(value, str) else value
    for cloud in journal.get("clouds", {}).values():
        for key in REDACTED:
            identifier = cloud.get("identifiers", {}).get(key)
            if identifier:
                rendered = rendered.replace(identifier, "[" + key + "]")
    return rendered


def publish(directory, name, value, journal):
    evidence = directory / "evidence"
    evidence.mkdir(mode=0o700, exist_ok=True)
    with open(evidence / name, "w", opener=lambda path, flags: os.open(path, flags, 0o600)) as output:
        output.write(redact(value, journal))
        if not isinstance(value, str):
            output.write("\n")


class Processes:
    """Owned Terraform process groups, stopped together on interruption."""

    def __init__(self):
        self.lock = threading.Lock()
        self.active = set()
        self.cancelled = False

    def start(self, command, **kwargs):
        with self.lock:
            if self.cancelled:
                raise RuntimeError("the batch was interrupted before this Terraform command started")
            process = subprocess.Popen(command, start_new_session=True, **kwargs)
            self.active.add(process)
            return process

    def finish(self, process):
        with self.lock:
            self.active.discard(process)

    def cancel(self):
        with self.lock:
            self.cancelled = True
            processes = list(self.active)
        for process in processes:
            if process.poll() is None:
                lab.terminate_process_group(process)


class Journal:
    """The private deployment journal; worker threads update it under one lock."""

    def __init__(self, directory, value):
        self.path = directory / "deployment.json"
        self.value = value
        self.lock = threading.Lock()
        lab.write_json(self.path, value)

    def stage(self, cloud, stage, status, **fields):
        with self.lock:
            entry = self.value["clouds"][cloud]
            entry["stages"][stage] = status
            entry.update(fields)
            lab.write_json(self.path, self.value)


def terraform(stage_directory, root, executable, *args, timeout, processes=None, capture=False):
    """Run Terraform for one stage with its own provider directory and log."""
    env = dict(os.environ, TF_IN_AUTOMATION="1", TF_INPUT="0", TF_DATA_DIR=str(stage_directory / "terraform-data"))
    # Verbose provider logs can print credentials and request bodies.
    for key in ("TF_LOG", "TF_LOG_CORE", "TF_LOG_PROVIDER", "TF_LOG_PATH"):
        env.pop(key, None)
    command = [executable, "-chdir=" + str(root), *args]
    processes = processes or Processes()
    with open(stage_directory / "terraform.log", "a", opener=lambda path, flags: os.open(path, flags, 0o600)) as log:
        process = processes.start(command, env=env, text=True, stdout=subprocess.PIPE if capture else log, stderr=log)
        try:
            with process:
                try:
                    stdout, _stderr = process.communicate(timeout=timeout)
                except BaseException:
                    lab.terminate_process_group(process)
                    raise
        finally:
            processes.finish(process)
    if process.returncode:
        raise RuntimeError("Terraform " + args[0] + " failed in " + stage_directory.parent.name + " " +
                           stage_directory.name + "; private diagnostics retained")
    return stdout if capture else None


def ownership(journal):
    return {"owner": journal["owner"], "batch": journal["batch"], "expires_at": journal["expiresAt"],
            "labels": {lab.OWNER_LABEL: journal["owner"], "io.infraweave.lab.batch": journal["batch"],
                       "io.infraweave.lab.managed-by": "terraform"}}


def check_inventory(entries, owner, where):
    if not isinstance(entries, list):
        raise ValueError(where + " inventory must be a list")
    for entry in entries:
        exact(entry, ("kind", "id", "scope", "owned", "owner", "lifecycle"), where + " inventory entry")
        text(entry["kind"], where + " inventory kind")
        text(entry["id"], where + " inventory id")
        if type(entry["owned"]) is not bool or entry["lifecycle"] not in LIFECYCLES:
            raise ValueError(where + " inventory ownership or lifecycle is malformed")
        if entry["owned"] and entry["owner"] != owner:
            raise ValueError(where + " inventory claims a resource of another owner")


def validate_cluster(cloud, output, journal, config):
    """The cluster stage output must name this owner's exact cluster and trust."""
    exact(output, ("schema", "kind", "owner", "batch", "provider", "id", "name", "endpoint", "ca_certificate",
                   "owned", "inventory"), cloud + " cluster output")
    cluster = config["deployment"]["cluster"]
    ids = config["cloud"]["identifiers"]
    expected = cluster.get("existing_id") or cluster_id(cloud, ids, cluster["name"])
    if (output["schema"] != 1 or output["kind"] != "worker-cluster" or output["owner"] != journal["owner"] or
            output["batch"] != journal["batch"] or output["provider"] != cloud):
        raise ValueError(cloud + " cluster output belongs to another owner, batch or cloud")
    if output["name"] != cluster["name"] or output["id"].lower() != expected.lower():
        raise ValueError(cloud + " cluster output names a cluster other than the explicit inputs")
    if output["owned"] is not (cluster["mode"] == "create"):
        raise ValueError(cloud + " cluster ownership contradicts its mode")
    endpoint = urllib.parse.urlsplit(text(output["endpoint"], cloud + " cluster endpoint"))
    if (endpoint.scheme != "https" or not endpoint.hostname or endpoint.username or endpoint.password or
            endpoint.query or endpoint.fragment or endpoint.path not in ("", "/")):
        raise ValueError(cloud + " cluster endpoint must be a bare HTTPS origin")
    if not text(output["ca_certificate"], cloud + " cluster CA").startswith("-----BEGIN CERTIFICATE-----"):
        raise ValueError(cloud + " cluster CA must be a PEM certificate")
    check_inventory(output["inventory"], journal["owner"], cloud + " cluster")
    return output


def reach(cloud, endpoint, ca_certificate, deadline):
    """Complete a verified TLS handshake with the cluster API before attachment."""
    target = urllib.parse.urlsplit(endpoint)
    try:
        context = ssl.create_default_context(cadata=ca_certificate)
    except ssl.SSLError:
        raise ValueError(cloud + " cluster CA is not a usable certificate") from None
    while True:
        try:
            with socket.create_connection((target.hostname, target.port or 443), timeout=10) as connection:
                with context.wrap_socket(connection, server_hostname=target.hostname):
                    return
        except ssl.SSLCertVerificationError:
            raise ValueError(cloud + " cluster API TLS identity does not match its declared CA and name") from None
        except (OSError, ssl.SSLError):
            if time.monotonic() >= deadline:
                raise RuntimeError(cloud + " cluster API was unreachable within the readiness bound") from None
            time.sleep(1)


def validate_hosting(cloud, hosting, journal, config, cluster, bounds):
    """The deployment's `hosting` output must match every explicit input."""
    where = cloud + " hosting"
    exact(hosting, ("schema", "kind", "owner", "batch", "expires_at", "cloud", "cluster", "namespace", "installation",
                    "artifacts", "pools", "resources", "inventory"), where)
    if (hosting["schema"] != 1 or hosting["kind"] != "worker-hosting" or hosting["owner"] != journal["owner"] or
            hosting["batch"] != journal["batch"] or hosting["expires_at"] != journal["expiresAt"]):
        raise ValueError(where + " ownership differs from this batch")
    if hosting["cloud"] != config["cloud"]:
        raise ValueError(where + " cloud identifiers differ from the explicit inputs")
    observed = exact(hosting["cluster"], ("id", "name", "cluster_id", "kubernetes_version", "managed_profile", "owned",
                                          "network_ids"), where + " cluster")
    if observed["id"] != cluster["id"] or observed["name"] != cluster["name"] or observed["owned"] is not cluster["owned"]:
        raise ValueError(where + " cluster differs from the cluster stage")
    text(observed["cluster_id"], where + " controller cluster ID", LABEL.pattern)
    text(observed["kubernetes_version"], where + " Kubernetes version")
    text(observed["managed_profile"], where + " managed profile")
    if not isinstance(observed["network_ids"], list) or not all(isinstance(i, str) and i for i in observed["network_ids"]):
        raise ValueError(where + " network IDs are malformed")
    namespace = exact(hosting["namespace"], ("name", "uid", "owned"), where + " namespace")
    text(namespace["name"], where + " namespace name", LABEL.pattern)
    text(namespace["uid"], where + " namespace UID")
    if type(namespace["owned"]) is not bool:
        raise ValueError(where + " namespace ownership is malformed")
    installation = exact(hosting["installation"], ("release_name", "chart_version", "chart_sha256",
                                                   "controller_service_account", "run_service_account", "crds_owned"),
                         where + " installation")
    text(installation["chart_sha256"], where + " chart digest", r"[0-9a-f]{64}")
    for key in ("release_name", "chart_version", "controller_service_account", "run_service_account"):
        text(installation[key], where + " installation " + key)
    if type(installation["crds_owned"]) is not bool:
        raise ValueError(where + " CRD ownership is malformed")
    artifacts = exact(hosting["artifacts"], ("module_revision", "controller_image", "bootstrap_image", "runner_images"),
                      where + " artifacts")
    text(artifacts["module_revision"], where + " module revision", r"[0-9a-f]{40}")
    images = [artifacts["controller_image"], artifacts["bootstrap_image"]]
    if not isinstance(artifacts["runner_images"], dict) or not artifacts["runner_images"]:
        raise ValueError(where + " runner images are missing")
    images.extend(artifacts["runner_images"].values())
    if any(not isinstance(image, str) or not DIGEST.fullmatch(image) for image in images):
        raise ValueError(where + " artifacts must be immutable @sha256 references")
    pools = hosting["pools"]
    if not isinstance(pools, list) or not pools:
        raise ValueError(where + " pools are missing")
    names = set()
    for pool in pools:
        exact(pool, ("resource_name", "resource_uid", "mode", "requested_pool_id", "capacity", "credentials_ref"),
              where + " pool")
        text(pool["resource_name"], where + " pool resource name", LABEL.pattern)
        text(pool["resource_uid"], where + " pool resource UID")
        text(pool["credentials_ref"], where + " pool credentials reference", LABEL.pattern)
        if pool["mode"] not in POOL_MODES or type(pool["capacity"]) is not int or pool["capacity"] < 0:
            raise ValueError(where + " pool mode or capacity is malformed")
        if (pool["mode"] == "Managed") != (pool["requested_pool_id"] is None):
            raise ValueError(where + " only Managed pools may omit their requested pool ID")
        if pool["requested_pool_id"] is not None:
            text(pool["requested_pool_id"], where + " requested pool ID", ULID.pattern)
        names.add(pool["resource_name"])
    if len(names) != len(pools):
        raise ValueError(where + " pool resource names must be distinct")
    if sum(pool["capacity"] for pool in pools) > bounds["pool_capacity"]:
        raise ValueError(where + " configured capacity exceeds the batch bound")
    if not isinstance(hosting["resources"], dict):
        raise ValueError(where + " resources are malformed")
    check_inventory(hosting["inventory"], journal["owner"], where)
    if PRIVATE_KEY.search(json.dumps(hosting)):
        raise ValueError(where + " output carries private key material")
    return hosting


def apply_stage(directory, cloud, stage, journal, config, executable, bounds, processes, extra=None):
    """Initialize, plan, apply and read one stage's output from its private state."""
    stage_directory = directory / "clouds" / cloud / stage
    root = Path(journal.value["clouds"][cloud]["root"]) / stage
    stage_directory.mkdir(mode=0o700, parents=True)
    variables = dict(config[stage], ownership=ownership(journal.value), cloud=config["cloud"], **(extra or {}))
    lab.write_json(stage_directory / "variables.tfvars.json", variables)
    step = lambda *args, **kwargs: terraform(stage_directory, root, executable, *args, processes=processes, **kwargs)
    step("init", "-input=false", "-no-color", "-lockfile=readonly",
         "-backend-config=path=" + str(stage_directory / "terraform.tfstate"), timeout=lab.TERRAFORM_TIMEOUT)
    step("plan", "-input=false", "-no-color", "-var-file=" + str(stage_directory / "variables.tfvars.json"),
         "-out=" + str(stage_directory / "deployment.tfplan"), timeout=lab.TERRAFORM_TIMEOUT)
    # Written before apply so cleanup reads the cloud back after any partial apply.
    journal.stage(cloud, stage, "applying")
    step("apply", "-input=false", "-no-color", str(stage_directory / "deployment.tfplan"), timeout=bounds["provision_seconds"])
    journal.stage(cloud, stage, "applied")
    return json.loads(step("output", "-no-color", "-json", STAGE_OUTPUTS[stage], capture=True, timeout=lab.TERRAFORM_TIMEOUT))


def provision(directory, cloud, journal, config, executable, bounds, processes):
    """Apply one cloud's cluster then deployment stage and validate attachment."""
    output = apply_stage(directory, cloud, "cluster", journal, config, executable, bounds, processes)
    cluster = validate_cluster(cloud, output, journal.value, config)
    journal.stage(cloud, "cluster", "applied", cluster={"id": cluster["id"], "name": cluster["name"], "owned": cluster["owned"]},
                  inventory=cluster["inventory"])
    reach(cloud, cluster["endpoint"], cluster["ca_certificate"], time.monotonic() + bounds["readiness_seconds"])
    hosting = apply_stage(directory, cloud, "deployment", journal, config, executable, bounds, processes,
                          extra={"cluster_stage": cluster})
    hosting = validate_hosting(cloud, hosting, journal.value, config, cluster, bounds)
    journal.stage(cloud, "deployment", "applied", inventory=cluster["inventory"] + hosting["inventory"])
    return hosting


def bounded(clouds, parallelism, work, processes):
    """Run work per cloud with bounded parallelism; every cloud finishes or is stopped."""
    results, errors = {}, {}
    with concurrent.futures.ThreadPoolExecutor(max_workers=parallelism) as pool:
        futures = {pool.submit(work, cloud): cloud for cloud in clouds}
        try:
            for future in concurrent.futures.as_completed(futures):
                cloud = futures[future]
                try:
                    results[cloud] = future.result()
                except Exception as error:
                    errors[cloud] = type(error).__name__ + ": " + str(error)
        except BaseException:
            # Interruption stops every owned Terraform process before cleanup
            # can run destroy against the same state.
            processes.cancel()
            raise
    return results, errors


def up(directory, name, executable, helper, owner, inputs, processes=None):
    """Provision the control plane and every selected cloud for one new owner."""
    processes = processes or Processes()
    directory.mkdir(mode=0o700, parents=True, exist_ok=False)
    review, configs, batch = preflight(name, inputs)
    expires = datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(minutes=batch["bounds"]["ttl_minutes"])
    journal = Journal(directory, {
        "schema": 1, "kind": "managed-cloud-workers", "owner": owner, "batch": name, "terraform": executable,
        "definitionSHA256": batch["digest"], "parallelism": batch["bounds"]["parallelism"],
        "cleanupSeconds": batch["bounds"]["cleanup_seconds"],
        "expiresAt": expires.replace(microsecond=0).isoformat().replace("+00:00", "Z"),
        "clouds": {cloud: {"root": str(ROOTS / cloud), "stages": {stage: "pending" for stage in STAGES},
                           "identifiers": configs[cloud]["cloud"]["identifiers"] if cloud in configs else {},
                           "cluster": ({"name": configs[cloud]["deployment"]["cluster"]["name"],
                                        "owned": configs[cloud]["deployment"]["cluster"]["mode"] == "create"}
                                       if cloud in configs else {}),
                           "inventory": []} for cloud in batch["clouds"]}})
    lab.write_json(directory / "review.json", review)
    publish(directory, "review.json", review, journal.value)
    if not review["passed"]:
        raise preflight_error(review)
    lab.up(directory / "platform", name, executable, helper, owner)
    hosting, errors = bounded(batch["clouds"], batch["bounds"]["parallelism"],
                              lambda cloud: provision(directory, cloud, journal, configs[cloud], executable, batch["bounds"],
                                                      processes), processes)
    if errors:
        raise RuntimeError("Managed cloud provisioning failed; every owned cloud enters cleanup. " +
                           "; ".join(cloud + ": " + error for cloud, error in sorted(errors.items())))
    workers = {"schema": 1, "owner": owner, "batch": name, "hosting": [hosting[cloud] for cloud in batch["clouds"]]}
    lab.write_json(directory / "workers.json", workers)
    verify_workers(directory / "workers.json", journal.value)
    return workers


def verify_workers(path, journal):
    """The combined manifest carries each required cloud exactly once for this owner."""
    workers = exact(lab.read_json(path), ("schema", "owner", "batch", "hosting"), "workers manifest")
    if workers["schema"] != 1 or workers["owner"] != journal["owner"] or workers["batch"] != journal["batch"]:
        raise ValueError("workers manifest belongs to another owner or batch")
    providers = [entry.get("cloud", {}).get("provider") for entry in workers["hosting"]]
    if sorted(providers) != sorted(journal["clouds"]):
        raise ValueError("workers manifest does not attach exactly the required clouds")
    for entry in workers["hosting"]:
        if entry.get("owner") != journal["owner"] or entry.get("batch") != journal["batch"]:
            raise ValueError("workers manifest attaches hosting of another owner")
    return workers


def state_owners(value):
    """Yield every owner tag or label value recorded in a state value."""
    if isinstance(value, dict):
        for key, item in value.items():
            if key in OWNER_KEYS and isinstance(item, str):
                yield item
            else:
                yield from state_owners(item)
    elif isinstance(value, list):
        for item in value:
            yield from state_owners(item)


def managed_values(module):
    """Yield managed resource values; data sources may read borrowed resources."""
    for resource in module.get("resources", []):
        if resource.get("mode") == "managed":
            yield resource.get("values")
    for child in module.get("child_modules", []):
        yield from managed_values(child)


def verify_state_owner(stage_directory, root, journal):
    """Refuse destroy when the stage's tags or recorded variables name another owner.

    Outputs are reports a faulty root can misstate, which hosting validation
    rejects; ownership evidence is what Terraform applied to resources.
    """
    shown = json.loads(terraform(stage_directory, root, journal["terraform"], "show", "-no-color", "-json",
                                 capture=True, timeout=lab.TERRAFORM_TIMEOUT))
    owners = set(state_owners(list(managed_values(shown.get("values", {}).get("root_module", {})))))
    variables = stage_directory / "variables.tfvars.json"
    if variables.exists():
        owners.add(lab.read_json(variables).get("ownership", {}).get("owner"))
    if owners - {journal["owner"]}:
        raise RuntimeError("Terraform state ownership differs from the cleanup journal; refusing destroy")


def destroy_stage(directory, cloud, stage, journal):
    stage_directory = directory / "clouds" / cloud / stage
    root = Path(journal["clouds"][cloud]["root"]) / stage
    if not (stage_directory / "terraform.tfstate").exists():
        return {"state": "never-applied", "emptyState": True}
    step = lambda *args, **kwargs: terraform(stage_directory, root, journal["terraform"], *args, **kwargs)
    listed = step("state", "list", capture=True, timeout=lab.TERRAFORM_TIMEOUT).strip()
    if not listed:
        return {"state": "empty", "emptyState": True}
    verify_state_owner(stage_directory, root, journal)
    variables = stage_directory / "variables.tfvars.json"
    if not variables.exists():
        raise RuntimeError(cloud + " " + stage + " state is not empty but its variables were removed")
    step("destroy", "-input=false", "-auto-approve", "-no-color", "-var-file=" + str(variables),
         timeout=journal["cleanupSeconds"])
    listed = step("state", "list", capture=True, timeout=lab.TERRAFORM_TIMEOUT).strip()
    return {"state": "destroyed", "emptyState": not listed}


def readback(cloud, journal, deadline):
    """Read the cloud back until every owned resource is absent or the bound expires."""
    entry = journal["clouds"][cloud]
    ids, owner = entry["identifiers"], journal["owner"]
    while True:
        result = {"checks": [], "inventory": [], "passed": False}
        try:
            remaining = owner_search(cloud, ids, owner)
            result["checks"].append({"check": "owner-search", "outcome": "present" if remaining else "absent",
                                     "remaining": len(remaining)})
            cluster = entry.get("cluster", {})
            if cluster.get("owned") and cluster.get("name"):
                state = cluster_state(cloud, ids, cluster["name"])
                result["checks"].append({"check": "cluster", "outcome": "absent" if state == "absent" else "present"})
            cluster_present = any(check["check"] == "cluster" and check["outcome"] == "present"
                                  for check in result["checks"])
            for item in entry.get("inventory", []):
                if not item["owned"]:
                    outcome = "borrowed"
                elif (item["kind"] == "cluster" and cluster_present) or any(
                        found.lower().endswith(item["id"].lower()) for found in remaining):
                    outcome = "present"
                else:
                    outcome = "absent"
                result["inventory"].append({"kind": item["kind"], "id": item["id"], "owned": item["owned"],
                                            "lifecycle": item["lifecycle"], "outcome": outcome})
        except Unavailable as error:
            # Permission denied, a missing CLI or a timeout is never absence.
            result["checks"].append({"check": "readback", "outcome": "unknown", "error": str(error)})
            return result
        if all(check["outcome"] == "absent" for check in result["checks"]):
            result["passed"] = True
            return result
        if time.monotonic() >= deadline:
            return result
        time.sleep(READBACK_INTERVAL)


def cleanup_cloud(directory, cloud, journal):
    """Destroy deployment before cluster, then read the cloud back."""
    result = {"stages": {}, "passed": False}
    try:
        for stage in ("deployment", "cluster"):
            result["stages"][stage] = destroy_stage(directory, cloud, stage, journal)
            if not result["stages"][stage]["emptyState"]:
                raise RuntimeError(cloud + " " + stage + " state is not empty after destroy")
    except Exception as error:
        result["error"] = type(error).__name__ + ": " + str(error)
        return result
    stages = journal["clouds"][cloud]["stages"]
    if all(status == "pending" for status in stages.values()):
        result["readback"] = {"checks": [], "inventory": [], "passed": True, "reason": "never applied"}
    else:
        result["readback"] = readback(cloud, journal, time.monotonic() + journal["cleanupSeconds"])
    result["passed"] = result["readback"]["passed"]
    return result


def down(directory):
    """Clean every owned cloud and the platform independently; verify absence."""
    journal = lab.read_json(directory / "deployment.json")
    clouds = list(journal["clouds"])
    outcomes, errors = bounded(clouds, journal["parallelism"], lambda cloud: cleanup_cloud(directory, cloud, journal),
                               Processes())
    for cloud, error in errors.items():
        outcomes[cloud] = {"passed": False, "error": error}
    result = {"owner": journal["owner"], "clouds": {cloud: outcomes[cloud] for cloud in clouds}}
    if (directory / "platform" / "deployment.json").exists():
        try:
            result["platform"] = lab.down(directory / "platform")
        except Exception as error:
            result["platform"] = {"passed": False, "error": type(error).__name__ + ": " + str(error)}
    result["passed"] = all(outcome["passed"] for outcome in result["clouds"].values()) and \
        result.get("platform", {"passed": True})["passed"]
    if result["passed"]:
        for cloud in clouds:
            for stage in STAGES:
                stage_directory = directory / "clouds" / cloud / stage
                # The provider directory stays: a repeated teardown lists the
                # empty state through it without re-initializing.
                for relative in PRIVATE_FILES:
                    (stage_directory / relative).unlink(missing_ok=True)
        result["privateInputsRemoved"] = True
    lab.write_json(directory / "cleanup.json", result)
    publish(directory, "cleanup.json", result, journal)
    if not result["passed"]:
        raise RuntimeError("Managed cloud cleanup could not verify absence; deployment journal retained")
    return result


def retain_diagnostics(directory):
    """Publish each stage's Terraform log without private keys or account IDs."""
    journal = lab.read_json(directory / "deployment.json")
    for cloud in journal["clouds"]:
        for stage in STAGES:
            source = directory / "clouds" / cloud / stage / "terraform.log"
            if source.exists():
                scrubbed = re.sub(r"-----BEGIN [^-]*PRIVATE KEY-----.*?-----END [^-]*PRIVATE KEY-----",
                                  "[redacted private key]", source.read_text(), flags=re.DOTALL)
                publish(directory, cloud + "-" + stage + "-terraform.log", scrubbed, journal)
    if (directory / "platform" / "deployment.json").exists():
        lab.retain_diagnostics(directory / "platform")


class Lifecycle:
    """The labctl.run hooks for a managed cloud batch."""

    def __init__(self, args):
        self.args = args
        self.inputs = parse_inputs(getattr(args, "inputs", None))
        self.processes = Processes()
        definition(args.batch)

    def up(self, directory, owner):
        return up(directory, self.args.batch, self.args.terraform, self.args.helper, owner, self.inputs, self.processes)

    def down(self, directory):
        return down(directory)

    def environment(self, directory):
        return {"INFRAWEAVE_LAB_MANIFEST": str(directory / "platform" / "acceptance.json"),
                "INFRAWEAVE_LAB_WORKERS": str(directory / "workers.json")}

    def diagnostics(self, directory):
        retain_diagnostics(directory)

    def record(self, directory, result):
        publish(directory, "result.json", result, lab.read_json(directory / "deployment.json"))
