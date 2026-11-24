"""Managed cloud batches must preflight, isolate owners and clean every cloud.

The lifecycle tests run the real Terraform executable against fixture
cluster/deployment roots built from terraform_data, so init, plan, apply,
output, destroy and state readback are actual. Provider CLIs are replaced by
recorded answers on PATH; the cluster API is a real local TLS listener.
"""

import argparse
import base64
from contextlib import contextmanager
import json
import os
from pathlib import Path
import shutil
import signal
import socket
import ssl
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parent))
import cloudlab as cloud  # noqa: E402
import labctl as lab  # noqa: E402

TERRAFORM = shutil.which("terraform")
IDS = {
    "aws": {"account_id": "123456789012", "region": "eu-west-1"},
    "azure": {"tenant_id": "11111111-1111-1111-1111-111111111111", "subscription_id": "22222222-2222-2222-2222-222222222222",
              "location": "westeurope", "resource_group_name": "iw-lab-workers"},
    "google": {"project_id": "iw-lab-project", "project_number": "987654321", "location": "europe-west1"},
}
IMAGE = "ghcr.io/weaveplatform/fixture@sha256:" + "a" * 64

CLUSTER_ROOT = """
terraform {
  backend "local" {}
}
variable "ownership" {
  type = object({ owner = string, batch = string, expires_at = string, labels = map(string) })
}
variable "cloud" {
  type = object({ provider = string, identifiers = map(string) })
}
variable "cluster_id" { type = string }
variable "name" { type = string }
variable "endpoint" { type = string }
variable "ca_certificate" { type = string }
variable "fail" {
  type    = bool
  default = false
}
resource "terraform_data" "cluster" {
  input = { tags = { "io.infraweave.lab.owner" = var.ownership.owner } }
}
resource "terraform_data" "failure" {
  count      = var.fail ? 1 : 0
  depends_on = [terraform_data.cluster]
  provisioner "local-exec" {
    command = "exit 1"
  }
}
output "cluster" {
  value = {
    schema = 1, kind = "worker-cluster", owner = var.ownership.owner, batch = var.ownership.batch,
    provider = var.cloud.provider, id = var.cluster_id, name = var.name, endpoint = var.endpoint,
    ca_certificate = var.ca_certificate, owned = true,
    inventory = [{ kind = "cluster", id = var.cluster_id, scope = "region", owned = true,
                   owner = var.ownership.owner, lifecycle = "cluster" }]
  }
}
"""

DEPLOYMENT_ROOT = """
terraform {
  backend "local" {}
}
variable "ownership" {
  type = object({ owner = string, batch = string, expires_at = string, labels = map(string) })
}
variable "cloud" {
  type = object({ provider = string, identifiers = map(string) })
}
variable "cluster_stage" { type = any }
variable "cluster" { type = any }
variable "pools" { type = any }
variable "reported_owner" {
  type    = string
  default = ""
}
resource "terraform_data" "installation" {
  input = { labels = { "io_infraweave_lab_owner" = var.ownership.owner } }
}
output "hosting" {
  value = {
    schema = 1, kind = "worker-hosting", batch = var.ownership.batch, expires_at = var.ownership.expires_at,
    owner = var.reported_owner != "" ? var.reported_owner : var.ownership.owner,
    cloud = var.cloud,
    cluster = { id = var.cluster_stage.id, name = var.cluster_stage.name, cluster_id = "lab-${var.cloud.provider}",
                kubernetes_version = "1.34", managed_profile = "fixture", owned = var.cluster_stage.owned,
                network_ids = ["network-1"] },
    namespace = { name = "iw-workers", uid = terraform_data.installation.id, owned = true },
    installation = { release_name = "infraweave-workers", chart_version = "0.1.0", chart_sha256 = sha256("chart"),
                     controller_service_account = "controller", run_service_account = "run", crds_owned = true },
    artifacts = { module_revision = "0123456789abcdef0123456789abcdef01234567",
                  controller_image = "%(image)s", bootstrap_image = "%(image)s",
                  runner_images = { terraform = "%(image)s" } },
    pools = [for pool in var.pools : { resource_name = pool.resource_name, resource_uid = "uid-${pool.resource_name}",
             mode = "Managed", requested_pool_id = null, capacity = pool.capacity, credentials_ref = "pool-credentials" }],
    resources = { controller = {} },
    inventory = [{ kind = "namespace", id = "namespace/iw-workers", scope = "cluster", owned = true,
                   owner = var.ownership.owner, lifecycle = "setup" }]
  }
}
""" % {"image": IMAGE}

FAKE_CLI = """#!%s
import json, os, sys
argv = " ".join(sys.argv[1:])
name = os.path.basename(sys.argv[0])
with open(os.environ["FAKE_CLOUD_LOG"], "a") as log:
    log.write(name + " " + argv + "\\n")
if name == "kubectl":
    # Documents arrive on standard input, apart from the logged arguments.
    with open(os.environ["FAKE_CLOUD_LOG"] + ".stdin", "a") as documents:
        documents.write(sys.stdin.read() + "\\n")
with open(os.environ["FAKE_CLOUD"]) as rules:
    for rule in json.load(rules):
        if rule["cli"] == name and all(part in argv for part in rule["match"]):
            sys.stdout.write(rule.get("stdout", ""))
            sys.stderr.write(rule.get("stderr", ""))
            sys.exit(rule.get("exit", 0))
sys.stderr.write("unexpected command")
sys.exit(2)
""" % sys.executable


def answers(overrides=()):
    """Recorded provider answers for an authorized account with free quota."""
    rules = list(overrides) + [
        {"cli": "aws", "match": ["sts get-caller-identity"], "stdout": json.dumps({"Account": IDS["aws"]["account_id"]})},
        {"cli": "aws", "match": ["get-service-quota", "--service-code ec2"], "stdout": json.dumps({"Quota": {"Value": 64}})},
        {"cli": "aws", "match": ["get-service-quota", "--service-code eks"], "stdout": json.dumps({"Quota": {"Value": 100}})},
        {"cli": "aws", "match": ["eks list-clusters"], "stdout": json.dumps({"clusters": []})},
        {"cli": "aws", "match": ["eks describe-cluster"], "exit": 254,
         "stderr": "An error occurred (ResourceNotFoundException) when calling the DescribeCluster operation"},
        {"cli": "aws", "match": ["resourcegroupstaggingapi get-resources"], "stdout": json.dumps({"ResourceTagMappingList": []})},
        {"cli": "az", "match": ["account show"], "stdout": json.dumps({"tenantId": IDS["azure"]["tenant_id"],
                                                                       "id": IDS["azure"]["subscription_id"]})},
        {"cli": "az", "match": ["vm list-usage"], "stdout": json.dumps([{"name": {"value": "cores"}, "limit": "100",
                                                                          "currentValue": "4"}])},
        {"cli": "az", "match": ["aks show"], "exit": 3, "stderr": "(ResourceNotFound) The Resource was not found."},
        {"cli": "az", "match": ["resource list"], "stdout": "[]"},
        {"cli": "az", "match": ["group list"], "stdout": "[]"},
        {"cli": "gcloud", "match": ["projects describe"], "stdout": json.dumps({"projectNumber": IDS["google"]["project_number"],
                                                                                 "lifecycleState": "ACTIVE"})},
        {"cli": "gcloud", "match": ["compute regions describe"], "stdout": json.dumps({"quotas": [
            {"metric": "CPUS", "limit": 72, "usage": 0}]})},
        {"cli": "gcloud", "match": ["container clusters describe"], "exit": 1, "stderr": "ERROR: (gcloud) NOT_FOUND"},
        {"cli": "gcloud", "match": ["asset search-all-resources"], "stdout": "[]"},
        {"cli": "kubectl", "match": ["apply --server-side"]},
        {"cli": "kubectl", "match": ["delete"]},
        {"cli": "kubectl", "match": ["get", "--ignore-not-found"], "stdout": ""},
        {"cli": "kubectl", "match": ["get namespace"], "stdout": "namespace/iw-workers\n"},
    ]
    return rules


def certificate(directory, name):
    subprocess.run(["openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:prime256v1", "-nodes",
                    "-days", "1", "-subj", "/CN=" + name, "-addext", "subjectAltName=IP:127.0.0.1",
                    "-keyout", str(directory / (name + "-key.pem")), "-out", str(directory / (name + ".pem"))],
                   check=True, capture_output=True, timeout=30)
    return directory / (name + ".pem"), directory / (name + "-key.pem")


@contextmanager
def cluster_api(cert, key):
    """A real TLS listener standing in for a managed cluster's API endpoint."""
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.load_cert_chain(cert, key)
    server = socket.create_server(("127.0.0.1", 0))
    server.settimeout(0.1)
    stop = threading.Event()

    def serve():
        while not stop.is_set():
            try:
                connection, _address = server.accept()
            except (socket.timeout, OSError):
                continue
            connection.settimeout(5)
            try:
                with context.wrap_socket(connection, server_side=True):
                    pass
            except (ssl.SSLError, OSError):
                connection.close()

    worker = threading.Thread(target=serve, daemon=True)
    worker.start()
    try:
        yield "https://127.0.0.1:" + str(server.getsockname()[1])
    finally:
        stop.set()
        worker.join(timeout=2)
        server.close()


class Lab:
    """A private workspace: fixture roots, recorded CLIs, inputs and a TLS cluster API."""

    def __init__(self, root, failing=(), reported_owner=None):
        self.root = root
        self.roots = root / "roots"
        for name in cloud.CLOUDS:
            for stage, source in (("cluster", CLUSTER_ROOT), ("deployment", DEPLOYMENT_ROOT)):
                path = self.roots / name / stage
                path.mkdir(parents=True)
                (path / "versions.tf").write_text(source)
                (path / ".terraform.lock.hcl").write_text("")
        self.bin = root / "bin"
        self.bin.mkdir()
        for name in ("aws", "az", "gcloud", "kubectl"):
            (self.bin / name).write_text(FAKE_CLI)
            (self.bin / name).chmod(0o700)
        self.rules = root / "answers.json"
        self.calls = root / "calls.log"
        self.answer()
        self.cert, self.key = certificate(root, "cluster")
        self.failing, self.reported_owner = set(failing), reported_owner or {}

    def answer(self, overrides=()):
        self.rules.write_text(json.dumps(answers(overrides)))

    def inputs(self, clouds, endpoint, capacity=2, mode=0o600, setup=None, **extra):
        paths = []
        for name in clouds:
            path = self.root / (name + "-inputs.json")
            cluster = {"cluster_id": cloud.cluster_id(name, IDS[name], "lab-workers"), "name": "lab-workers",
                       "endpoint": endpoint, "ca_certificate": self.cert.read_text(), "fail": name in self.failing}
            deployment = {"cluster": {"mode": "create", "name": "lab-workers"},
                          "pools": [{"resource_name": "stable", "capacity": capacity}]}
            if name in self.reported_owner:
                deployment["reported_owner"] = self.reported_owner[name]
            deployment.update(extra)
            value = {"cloud": {"provider": name, "identifiers": IDS[name]}, "cluster": cluster, "deployment": deployment}
            if setup is not None:
                value["setup"] = setup
            path.write_text(json.dumps(value))
            path.chmod(mode)
            paths.append(name + "=" + str(path))
        return paths

    @contextmanager
    def active(self):
        environment = {"PATH": str(self.bin) + os.pathsep + os.environ["PATH"], "FAKE_CLOUD": str(self.rules),
                       "FAKE_CLOUD_LOG": str(self.calls)}
        with patch.dict(os.environ, environment), patch.object(cloud, "ROOTS", self.roots):
            yield


def platform_up(directory, _name, _executable, _helper, owner):
    directory.mkdir(mode=0o700)
    lab.write_json(directory / "deployment.json", {"owner": owner})
    lab.write_json(directory / "acceptance.json", {"owner": owner})


def arguments(directory, batch, inputs):
    return argparse.Namespace(directory=str(directory), batch=batch, terraform=TERRAFORM, helper="infraweave-lab",
                              features=None, tags=None, fips=False, inputs=inputs)


def passing_report(_command, **kwargs):
    environment = kwargs["env"]
    workers = lab.read_json(Path(environment["INFRAWEAVE_LAB_WORKERS"]))
    assert Path(environment["INFRAWEAVE_LAB_MANIFEST"]).name == "acceptance.json"
    assert environment["INFRAWEAVE_ACCEPT_PARTITION"] == "managed-cloud-workers"
    assert all(entry["owner"] == workers["owner"] for entry in workers["hosting"])
    lab.write_json(Path(environment["INFRAWEAVE_ACCEPT_CUCUMBER"]),
                   [{"elements": [{"type": "scenario", "steps": [{"result": {"status": "passed"}}]}]}])
    return 0


def states(directory, clouds):
    """Count the resources each stage's local state still records."""
    result = {}
    for name in clouds:
        for stage in cloud.STAGES:
            state = directory / "clouds" / name / stage / "terraform.tfstate"
            if state.exists():
                result[name + "/" + stage] = len(lab.read_json(state)["resources"])
    return result


class CloudBatchTests(unittest.TestCase):
    def setUp(self):
        self.assertIsNotNone(TERRAFORM, "the lifecycle tests require the terraform executable on PATH")
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name).resolve()
        patcher = patch.object(cloud, "READBACK_INTERVAL", 0)
        patcher.start()
        self.addCleanup(patcher.stop)

    def tearDown(self):
        self.temporary.cleanup()

    def run_batch(self, workspace, batch, inputs, report=passing_report):
        directory = self.root / "batch"
        with workspace.active(), patch.object(lab, "up", side_effect=platform_up), \
                patch.object(lab, "down", return_value={"passed": True}) as platform_down, \
                patch.object(lab, "output", return_value="revision"), \
                patch.object(lab, "terraform_version", return_value="1.17.2"), \
                patch.object(lab, "stream_command", side_effect=report) as acceptance:
            try:
                return directory, lab.run(arguments(directory, batch, inputs)), acceptance, platform_down
            except BaseException as error:  # noqa: BLE001 - the caller asserts on the failure
                return directory, error, acceptance, platform_down

    def test_definitions_are_immutable_and_bounded(self):
        for name, clouds in (("workers-aws", ["aws"]), ("workers-azure", ["azure"]), ("workers-google", ["google"]),
                             ("workers-multicloud", ["aws", "azure", "google"])):
            with self.subTest(batch=name):
                definition = cloud.definition(name)
                self.assertEqual(definition["clouds"], clouds)
                self.assertEqual(definition["partition"], "managed-cloud-workers")
                self.assertRegex(definition["digest"], r"^[0-9a-f]{64}$")
                feature = lab.REPO / "test/acceptance" / definition["features"]
                self.assertTrue(feature.is_file())
                self.assertIn("@managed-cloud-workers", feature.read_text().splitlines()[0])
        with self.assertRaises(ValueError):
            cloud.definition("policies")
        batches = self.root / "batches"
        batches.mkdir()
        valid = lab.read_json(lab.HERE / "batches/workers-aws.json")
        for name, mutate in (("unbounded", lambda d: d["bounds"].update(vcpus=1000)),
                             ("missing-bound", lambda d: d["bounds"].pop("ttl_minutes")),
                             ("unknown-cloud", lambda d: d.update(clouds=["other"])),
                             ("duplicate-cloud", lambda d: d.update(clouds=["aws", "aws"])),
                             ("parallelism", lambda d: d["bounds"].update(parallelism=2)),
                             ("boolean-bound", lambda d: d["bounds"].update(parallelism=True)),
                             ("extra-key", lambda d: d.update(region="eu-west-1"))):
            with self.subTest(definition=name):
                candidate = json.loads(json.dumps(valid))
                mutate(candidate)
                lab.write_json(batches / (name + ".json"), candidate)
                with patch.object(lab, "HERE", self.root), self.assertRaises(ValueError):
                    cloud.definition(name)

    def test_inputs_are_private_explicit_and_secret_free(self):
        workspace = Lab(self.root)
        bounds = cloud.definition("workers-aws")["bounds"]
        path = Path(workspace.inputs(["aws"], "https://127.0.0.1:1")[0].split("=", 1)[1])
        self.assertEqual(cloud.read_inputs("aws", path, bounds)["cloud"]["identifiers"], IDS["aws"])
        original = json.loads(path.read_text())
        for name, mutate, message in (
                ("readable", None, "private"),
                ("key", lambda d: d["deployment"].update(token="-----BEGIN EC PRIVATE KEY-----"), "private key"),
                ("provider", lambda d: d["cloud"].update(provider="google"), "another cloud"),
                ("identifier", lambda d: d["cloud"]["identifiers"].update(account_id="12"), "account_id"),
                ("discovery", lambda d: d["cloud"]["identifiers"].pop("region"), "exactly"),
                ("reserved", lambda d: d["cluster"].update(ownership={}), "coordinator"),
                ("mode", lambda d: d["deployment"]["cluster"].update(mode="adopt"), "create or existing"),
                ("existing", lambda d: d["deployment"]["cluster"].update(existing_id="arn"), "existing_id"),
                ("capacity", lambda d: d["deployment"]["pools"][0].update(capacity=5), "batch bound"),
                ("unbounded", lambda d: d["deployment"]["pools"][0].pop("capacity"), "explicit capacity")):
            with self.subTest(case=name):
                candidate = json.loads(json.dumps(original))
                if mutate:
                    mutate(candidate)
                path.write_text(json.dumps(candidate))
                path.chmod(0o644 if mutate is None else 0o600)
                with self.assertRaisesRegex(ValueError, message):
                    cloud.read_inputs("aws", path, bounds)
        with self.assertRaisesRegex(ValueError, "no private inputs"):
            cloud.read_inputs("aws", None, bounds)
        for value in (["aws"], ["aws=relative.json"], ["other=/x"], ["aws=/a", "aws=/b"]):
            with self.assertRaises(ValueError):
                cloud.parse_inputs(value)

    def test_preflight_reports_every_required_cloud_and_never_skips(self):
        workspace = Lab(self.root)
        inputs = cloud.parse_inputs(workspace.inputs(["aws", "azure"], "https://127.0.0.1:1"))
        workspace.answer([
            {"cli": "aws", "match": ["sts get-caller-identity"], "stdout": json.dumps({"Account": "210987654321"})},
            {"cli": "az", "match": ["vm list-usage"], "stdout": json.dumps([{"name": {"value": "cores"}, "limit": "10",
                                                                              "currentValue": "4"}])},
            {"cli": "gcloud", "match": ["projects describe"], "exit": 1, "stderr": "PERMISSION_DENIED"},
        ])
        shutil.rmtree(workspace.roots / "google" / "deployment")
        with workspace.active():
            review, configs, _definition = cloud.preflight("workers-multicloud", inputs)
        self.assertFalse(review["passed"])
        self.assertEqual(configs, {})
        self.assertIn("different account", " ".join(review["clouds"]["aws"]["problems"]))
        self.assertIn("vCPU quota headroom", " ".join(review["clouds"]["azure"]["problems"]))
        google = " ".join(review["clouds"]["google"]["problems"])
        self.assertIn("deployment", google)
        self.assertIn("no private inputs", google)
        self.assertNotIn(IDS["aws"]["account_id"], json.dumps(review))
        message = str(cloud.preflight_error(review))
        for name in ("aws", "azure", "google"):
            self.assertIn(name + ":", message)
        # Preflight reads identity and quota only; nothing can mutate.
        for call in workspace.calls.read_text().splitlines():
            self.assertRegex(call, r" (sts get-caller-identity|account show|vm list-usage|service-quotas|eks list-clusters)")

    def test_preflight_detects_missing_credentials_quota_and_inaccessible_clusters(self):
        workspace = Lab(self.root)
        cases = (
            ("missing CLI login", [{"cli": "aws", "match": ["sts"], "exit": 255, "stderr": "Unable to locate credentials"}],
             "credentials are missing"),
            ("cluster quota", [{"cli": "aws", "match": ["eks list-clusters"], "stdout": json.dumps({"clusters": ["x"] * 100})}],
             "cluster quota"),
            ("quota denied", [{"cli": "aws", "match": ["--service-code ec2"], "exit": 254, "stderr": "AccessDenied"}],
             "quota could not be read"),
            ("inaccessible cluster", [{"cli": "aws", "match": ["eks describe-cluster"], "exit": 254, "stderr": "AccessDenied"}],
             "inaccessible"),
            ("cluster not ready", [{"cli": "aws", "match": ["eks describe-cluster"],
                                    "stdout": json.dumps({"cluster": {"status": "CREATING"}})}], "not accessible and ready"),
        )
        for name, overrides, message in cases:
            with self.subTest(case=name):
                existing = name.endswith("cluster") or name == "cluster not ready"
                extra = {"cluster": {"mode": "existing", "name": "lab-workers", "existing_id": "arn"}} if existing else {}
                inputs = cloud.parse_inputs(workspace.inputs(["aws"], "https://127.0.0.1:1", **extra))
                workspace.answer(overrides)
                with workspace.active():
                    review, _configs, _definition = cloud.preflight("workers-aws", inputs)
                self.assertFalse(review["passed"])
                self.assertIn(message, " ".join(review["clouds"]["aws"]["problems"]))
        workspace.answer([{"cli": "aws", "match": ["eks describe-cluster"],
                           "stdout": json.dumps({"cluster": {"status": "ACTIVE"}})}])
        inputs = cloud.parse_inputs(workspace.inputs(["aws"], "https://127.0.0.1:1", cluster={
            "mode": "existing", "name": "lab-workers", "existing_id": "arn"}))
        with workspace.active():
            self.assertTrue(cloud.preflight("workers-aws", inputs)[0]["passed"])
            self.assertFalse(cloud.preflight("workers-aws", {})[0]["passed"])

    def test_multicloud_batch_applies_runs_and_verifies_absence_with_real_terraform(self):
        workspace = Lab(self.root)
        with cluster_api(workspace.cert, workspace.key) as endpoint:
            inputs = workspace.inputs(cloud.CLOUDS, endpoint)
            directory, result, acceptance, platform_down = self.run_batch(workspace, "workers-multicloud", inputs)
        self.assertIsInstance(result, dict, result)
        self.assertTrue(result["passed"])
        acceptance.assert_called_once()
        platform_down.assert_called_once_with(directory / "platform")
        journal = lab.read_json(directory / "deployment.json")
        self.assertEqual(journal["kind"], "managed-cloud-workers")
        self.assertEqual(journal["definitionSHA256"], cloud.definition("workers-multicloud")["digest"])
        workers = lab.read_json(directory / "workers.json")
        self.assertEqual([entry["cloud"]["provider"] for entry in workers["hosting"]], list(cloud.CLOUDS))
        cleanup = lab.read_json(directory / "cleanup.json")
        self.assertTrue(cleanup["passed"] and cleanup["privateInputsRemoved"])
        for name in cloud.CLOUDS:
            outcome = cleanup["clouds"][name]
            self.assertEqual(outcome["stages"]["deployment"]["state"], "destroyed")
            self.assertEqual({item["outcome"] for item in outcome["readback"]["inventory"]}, {"absent"})
            self.assertEqual({check["outcome"] for check in outcome["readback"]["checks"]}, {"absent"})
            for stage in cloud.STAGES:
                stage_directory = directory / "clouds" / name / stage
                self.assertEqual(stage_directory.stat().st_mode & 0o777, 0o700)
                for private in cloud.PRIVATE_FILES:
                    self.assertFalse((stage_directory / private).exists())
        self.assertEqual(set(states(directory, cloud.CLOUDS).values()), {0})
        self.assertEqual((directory / "deployment.json").stat().st_mode & 0o777, 0o600)
        self.assertTrue(lab.read_json(directory / "evidence/result.json")["passed"])
        published = "".join(path.read_text() for path in (directory / "evidence").iterdir())
        # The cleanup inventory names each cluster by an ID that embeds its account.
        self.assertIn("arn:aws:eks:eu-west-1:[account_id]:cluster/lab-workers", published)
        for name, values in IDS.items():
            for key in cloud.REDACTED:
                if key in values:
                    self.assertNotIn(values[key], published)
        # A repeated teardown needs neither the removed variables nor the plans.
        with workspace.active():
            again = cloud.down(directory)
        self.assertTrue(again["passed"])
        self.assertEqual(again["clouds"]["aws"]["stages"]["cluster"]["state"], "empty")

    def setup_inputs(self, workspace, endpoint):
        management = self.root / "management.json"
        management.write_text(json.dumps({"id": "01JABCDEFGHJKMNPQRSTVWXYZ0", "secret": "iwk_m4nagement-s3cr3t"}))
        management.chmod(0o600)
        return workspace.inputs(
            ["aws"], endpoint, setup={"management_credentials": str(management)},
            namespace={"name": "iw-workers", "setup_owned": True},
            management_credentials_ref={"name": "management", "id_key": "id", "secret_key": "secret"},
            pools=[{"resource_name": "stable", "mode": "Managed", "capacity": 2, "credentials_ref": "stable-credentials"}])

    def test_setup_creates_and_removes_the_namespace_and_secrets_between_the_stages(self):
        workspace = Lab(self.root)
        with cluster_api(workspace.cert, workspace.key) as endpoint:
            directory, result, acceptance, _down = self.run_batch(workspace, "workers-aws",
                                                                  self.setup_inputs(workspace, endpoint))
        self.assertIsInstance(result, dict, result)
        self.assertTrue(result["passed"])
        acceptance.assert_called_once()
        calls = workspace.calls.read_text()
        documents = Path(str(workspace.calls) + ".stdin").read_text()
        applies = [line for line in calls.splitlines() if line.startswith("kubectl") and " apply " in line]
        self.assertEqual(len(applies), 3)
        self.assertTrue(all(line.endswith("-f -") for line in applies))
        # Values travel only on standard input, base64-encoded as a Secret's data.
        self.assertNotIn("m4nagement", calls)
        self.assertIn(base64.b64encode(b"iwk_m4nagement-s3cr3t").decode(), documents)
        self.assertIn('"kind": "Namespace"', documents)
        self.assertIn(base64.b64encode(b"-----BEGIN EC PRIVATE KEY-----").decode()[:20], documents)
        self.assertIn("kubectl --kubeconfig", calls)
        self.assertRegex(calls, r"kubectl .* delete namespace iw-workers")
        cleanup = lab.read_json(directory / "cleanup.json")
        self.assertEqual(cleanup["clouds"]["aws"]["setup"], {"state": "removed", "namespace": "iw-workers",
                                                             "namespaceOwned": True, "secrets": 2})
        self.assertEqual(cleanup["clouds"]["aws"]["stages"]["cluster"]["state"], "destroyed")
        setup = directory / "clouds/aws/setup"
        self.assertEqual(setup.stat().st_mode & 0o777, 0o700)
        self.assertEqual(list((setup / "keys").iterdir()), [])
        kubeconfig = (setup / "kubeconfig").read_text()
        self.assertIn("get-token", kubeconfig)
        self.assertNotIn("m4nagement", kubeconfig)
        self.assertEqual(lab.read_json(directory / "deployment.json")["clouds"]["aws"]["setup"]["status"], "removed")
        published = "".join(path.read_text() for path in (directory / "evidence").iterdir())
        self.assertNotIn("m4nagement", published)

    def test_unverified_setup_removal_keeps_the_cluster_for_a_retry(self):
        workspace = Lab(self.root)
        workspace.answer([{"cli": "kubectl", "match": ["delete"], "exit": 1, "stderr": "Unable to connect"}])
        with cluster_api(workspace.cert, workspace.key) as endpoint:
            directory, error, _acceptance, _down = self.run_batch(workspace, "workers-aws",
                                                                 self.setup_inputs(workspace, endpoint))
        self.assertIsInstance(error, RuntimeError)
        cleanup = lab.read_json(directory / "cleanup.json")
        self.assertFalse(cleanup["passed"])
        self.assertIn("could not be deleted", cleanup["clouds"]["aws"]["error"])
        self.assertEqual(cleanup["clouds"]["aws"]["stages"]["deployment"]["state"], "destroyed")
        self.assertNotIn("cluster", cleanup["clouds"]["aws"]["stages"])
        self.assertGreater(states(directory, ["aws"])["aws/cluster"], 0)
        workspace.answer()
        with workspace.active():
            again = cloud.down(directory)
        self.assertTrue(again["passed"])
        self.assertEqual(again["clouds"]["aws"]["setup"]["state"], "removed")
        self.assertEqual(again["clouds"]["aws"]["stages"]["cluster"]["state"], "destroyed")

    def test_setup_inputs_name_only_private_files_for_the_secrets_they_create(self):
        workspace = Lab(self.root)
        bounds = cloud.definition("workers-aws")["bounds"]
        path = Path(self.setup_inputs(workspace, "https://127.0.0.1:1")[0].split("=", 1)[1])
        self.assertIn("setup", cloud.read_inputs("aws", path, bounds))
        original = json.loads(path.read_text())
        for name, mutate, message in (
                ("relative", lambda d: d["setup"].update(management_credentials="management.json"), "absolute"),
                ("unknown", lambda d: d["setup"].update(kubeconfig="/k"), "only"),
                ("unreferenced", lambda d: d["deployment"].pop("management_credentials_ref"), "only for"),
                ("managed-file", lambda d: d["setup"].update(pool_credentials={"stable": "/p.json"}), "Existing and Public"),
                ("namespace", lambda d: d["deployment"].pop("namespace"), "setup_owned")):
            with self.subTest(case=name):
                candidate = json.loads(json.dumps(original))
                mutate(candidate)
                path.write_text(json.dumps(candidate))
                with self.assertRaisesRegex(ValueError, message):
                    cloud.read_inputs("aws", path, bounds)
        candidate = json.loads(json.dumps(original))
        candidate["cloud"] = {"provider": "azure", "identifiers": IDS["azure"]}
        path.write_text(json.dumps(candidate))
        with self.assertRaisesRegex(ValueError, "ships with its cloud delivery"):
            cloud.read_inputs("azure", path, bounds)
        loose = self.root / "loose.json"
        loose.write_text(json.dumps({"id": "x", "secret": "y"}))
        loose.chmod(0o644)
        with self.assertRaisesRegex(ValueError, "private"):
            cloud.read_private(loose, "management")

    def test_one_clouds_failed_apply_still_cleans_every_owned_cloud(self):
        workspace = Lab(self.root, failing=["google"])
        with cluster_api(workspace.cert, workspace.key) as endpoint:
            inputs = workspace.inputs(cloud.CLOUDS, endpoint)
            directory, error, acceptance, _down = self.run_batch(workspace, "workers-multicloud", inputs)
        self.assertIsInstance(error, RuntimeError)
        self.assertIn("google", str(error))
        acceptance.assert_not_called()
        result = lab.read_json(directory / "result.json")
        self.assertFalse(result["passed"])
        self.assertIn("google: RuntimeError: Terraform apply failed", result["error"])
        cleanup = lab.read_json(directory / "cleanup.json")
        self.assertTrue(cleanup["passed"])
        self.assertEqual(cleanup["clouds"]["google"]["stages"]["deployment"]["state"], "never-applied")
        self.assertEqual(cleanup["clouds"]["google"]["stages"]["cluster"]["state"], "destroyed")
        self.assertEqual(cleanup["clouds"]["aws"]["stages"]["deployment"]["state"], "destroyed")
        self.assertEqual(set(states(directory, cloud.CLOUDS).values()), {0})
        self.assertIn("local-exec", (directory / "evidence/google-cluster-terraform.log").read_text())

    def test_hosting_manifest_mismatch_fails_before_any_scenario(self):
        workspace = Lab(self.root, reported_owner={"aws": "iw-lab-another-owner"})
        with cluster_api(workspace.cert, workspace.key) as endpoint:
            inputs = workspace.inputs(["aws"], endpoint)
            directory, error, acceptance, _down = self.run_batch(workspace, "workers-aws", inputs)
        self.assertIsInstance(error, RuntimeError)
        self.assertIn("ownership differs", str(error))
        acceptance.assert_not_called()
        self.assertFalse((directory / "workers.json").exists())
        self.assertTrue(lab.read_json(directory / "cleanup.json")["passed"])
        self.assertEqual(set(states(directory, ["aws"]).values()), {0})

    def test_untrusted_cluster_api_is_never_attached(self):
        workspace = Lab(self.root)
        other, _key = certificate(self.root, "other")
        with cluster_api(workspace.cert, workspace.key) as endpoint:
            cloud.reach("aws", endpoint, workspace.cert.read_text(), time.monotonic() + 5)
            with self.assertRaisesRegex(ValueError, "TLS identity"):
                cloud.reach("aws", endpoint, other.read_text(), time.monotonic() + 5)
            inputs = workspace.inputs(["aws"], endpoint)
            payload = json.loads(Path(inputs[0].split("=", 1)[1]).read_text())
            payload["cluster"]["ca_certificate"] = other.read_text()
            Path(inputs[0].split("=", 1)[1]).write_text(json.dumps(payload))
            directory, error, acceptance, _down = self.run_batch(workspace, "workers-aws", inputs)
        self.assertIn("TLS identity", str(error))
        acceptance.assert_not_called()
        self.assertTrue(lab.read_json(directory / "cleanup.json")["passed"])
        with socket.create_server(("127.0.0.1", 0)) as closed:
            port = closed.getsockname()[1]
        with self.assertRaisesRegex(RuntimeError, "unreachable"):
            cloud.reach("aws", "https://127.0.0.1:" + str(port), workspace.cert.read_text(), time.monotonic())

    def test_foreign_journal_cannot_destroy_and_owner_cleanup_still_succeeds(self):
        workspace = Lab(self.root)
        directory = self.root / "owned"
        with cluster_api(workspace.cert, workspace.key) as endpoint, workspace.active(), \
                patch.object(lab, "up", side_effect=platform_up), patch.object(lab, "down", return_value={"passed": True}):
            inputs = cloud.parse_inputs(workspace.inputs(["aws"], endpoint))
            cloud.up(directory, "workers-aws", TERRAFORM, "infraweave-lab", "iw-lab-owned", inputs)
            with self.assertRaises(FileExistsError):
                cloud.up(directory, "workers-aws", TERRAFORM, "infraweave-lab", "iw-lab-adopter", inputs)
            journal = lab.read_json(directory / "deployment.json")
            lab.write_json(directory / "deployment.json", dict(journal, owner="iw-lab-foreign"))
            with self.assertRaisesRegex(RuntimeError, "could not verify absence"):
                cloud.down(directory)
            refused = lab.read_json(directory / "cleanup.json")
            self.assertIn("refusing destroy", refused["clouds"]["aws"]["error"])
            self.assertEqual(states(directory, ["aws"]), {"aws/deployment": 1, "aws/cluster": 1})
            self.assertTrue((directory / "clouds/aws/cluster/variables.tfvars.json").exists())
            lab.write_json(directory / "deployment.json", journal)
            self.assertTrue(cloud.down(directory)["passed"])
        self.assertEqual(set(states(directory, ["aws"]).values()), {0})

    def test_unknown_or_remaining_cloud_resources_are_never_reported_absent(self):
        workspace = Lab(self.root)
        directory = self.root / "owned"
        with cluster_api(workspace.cert, workspace.key) as endpoint, workspace.active(), \
                patch.object(lab, "up", side_effect=platform_up), patch.object(lab, "down", return_value={"passed": True}):
            inputs = cloud.parse_inputs(workspace.inputs(["azure"], endpoint))
            cloud.up(directory, "workers-azure", TERRAFORM, "infraweave-lab", "iw-lab-owned", inputs)
            workspace.answer([{"cli": "az", "match": ["resource list"], "exit": 1, "stderr": "AuthorizationFailed"}])
            with self.assertRaisesRegex(RuntimeError, "could not verify absence"):
                cloud.down(directory)
            unknown = lab.read_json(directory / "cleanup.json")["clouds"]["azure"]["readback"]
            self.assertEqual(unknown["checks"][-1]["outcome"], "unknown")
            # State is empty, but the private journal and inputs stay for a retry.
            self.assertTrue((directory / "clouds/azure/deployment/variables.tfvars.json").exists())
            cluster = cloud.cluster_id("azure", IDS["azure"], "lab-workers")
            workspace.answer([{"cli": "az", "match": ["resource list"], "stdout": json.dumps([{"id": cluster}])}])
            remaining = cloud.readback("azure", lab.read_json(directory / "deployment.json"), deadline=0)
            self.assertFalse(remaining["passed"])
            self.assertEqual(remaining["checks"][0], {"check": "owner-search", "outcome": "present", "remaining": 1})
            self.assertEqual({item["kind"]: item["outcome"] for item in remaining["inventory"]},
                             {"cluster": "present", "namespace": "absent"})
            workspace.answer()
            self.assertTrue(cloud.down(directory)["passed"])
        self.assertFalse((directory / "clouds/azure/deployment/variables.tfvars.json").exists())

    def test_interruption_stops_owned_terraform_and_refuses_new_commands(self):
        processes = cloud.Processes()
        process = processes.start([sys.executable, "-c", "import time; time.sleep(60)"])
        try:
            processes.cancel()
            self.assertEqual(process.wait(timeout=5), -signal.SIGTERM)
            with self.assertRaisesRegex(RuntimeError, "interrupted"):
                processes.start([sys.executable, "-c", "pass"])
        finally:
            if process.poll() is None:
                process.kill()
                process.wait(timeout=5)

    def test_existing_directory_is_never_adopted_or_cleaned(self):
        workspace = Lab(self.root)
        directory = self.root / "batch"
        directory.mkdir()
        journal = {"owner": "iw-lab-another-invocation", "kind": "managed-cloud-workers"}
        lab.write_json(directory / "deployment.json", journal)
        inputs = workspace.inputs(["aws"], "https://127.0.0.1:1")
        directory, error, acceptance, platform_down = self.run_batch(workspace, "workers-aws", inputs)
        self.assertIsInstance(error, FileExistsError)
        acceptance.assert_not_called()
        platform_down.assert_not_called()
        self.assertEqual(lab.read_json(directory / "deployment.json"), journal)
        self.assertFalse((directory / "result.json").exists())


if __name__ == "__main__":
    unittest.main()
