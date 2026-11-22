"""Partial bootstrap credentials must not bypass bounded, owned readiness."""

import base64
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import labctl as lab


def bootstrap_config():
    return {"current-context": "default", "clusters": [{"name": "default", "cluster": {"server": "https://untrusted.invalid",
            "certificate-authority-data": base64.b64encode(b"test-ca").decode()}}],
            "contexts": [{"name": "default", "context": {"cluster": "default", "user": "default", "namespace": "untrusted"}}],
            "users": [{"name": "default", "user": {"client-certificate-data": base64.b64encode(b"test-cert").decode(),
                                  "client-key-data": base64.b64encode(b"test-key").decode()}}]}


def malformed_configs():
    cases = [("null document", None), ("array document", []), ("string document", "partial"), ("empty document", {})]
    for collection, field in (("clusters", "cluster"), ("contexts", "context"), ("users", "user")):
        for name, value in (("null", None), ("empty", []), ("wrong type", "partial"),
                            ("null entry", [None]), ("wrong entry", ["partial"]), ("missing object", [{}]),
                            ("null object", [{field: None}]), ("wrong object", [{field: []}])):
            config = bootstrap_config()
            config[collection] = value
            cases.append((collection + " " + name, config))
    for collection, field, credential in (("clusters", "cluster", "certificate-authority-data"),
                                         ("users", "user", "client-certificate-data"),
                                         ("users", "user", "client-key-data")):
        for name, value in (("null", None), ("empty", ""), ("wrong type", 7), ("malformed base64", "not base64!")):
            config = bootstrap_config()
            config[collection][0][field][credential] = value
            cases.append((credential + " " + name, config))
    for collection in ("clusters", "contexts", "users"):
        for value in (None, "", 7):
            config = bootstrap_config()
            config[collection][0]["name"] = value
            cases.append((collection + " invalid name " + repr(value), config))
    for reference in ("cluster", "user", "current-context"):
        for value in (None, "foreign"):
            config = bootstrap_config()
            if reference == "current-context":
                config[reference] = value
            else:
                config["contexts"][0]["context"][reference] = value
            cases.append((reference + " unresolved reference " + repr(value), config))
    return cases


class Response(io.BytesIO):
    status = 200

    def __init__(self, body):
        super().__init__(json.dumps(body).encode())


class KubernetesReadinessTests(unittest.TestCase):
    def manifest(self, directory):
        (directory / "pki").mkdir(mode=0o700)
        return {"owner": "iw-lab-owned", "batch": "kubernetes", "kubernetes": {
            "container": "cluster", "endpoint": "https://127.0.0.1:12345", "namespace": "owned",
            "kubeconfig": str(directory / "pki/kubernetes.json")}}

    def inspected(self, manifest):
        return json.dumps([{"Config": {"Labels": {lab.OWNER_LABEL: manifest["owner"],
            "io.infraweave.lab.batch": "kubernetes", "io.infraweave.lab.managed-by": "terraform",
            "io.infraweave.lab.service": "kubernetes"}}}])

    def ready_responses(self, owner="iw-lab-owned"):
        return [Response({}), Response({"items": [{"status": {"conditions": [{"type": "Ready", "status": "True"}]}}]}),
                Response({"metadata": {"annotations": {lab.OWNER_LABEL: owner}}})]

    def test_partial_bootstrap_recovers_with_pinned_endpoint_and_exact_identity(self):
        with tempfile.TemporaryDirectory() as root:
            directory = Path(root)
            manifest = self.manifest(directory)
            documents = [json.dumps(value).encode() for _name, value in malformed_configs()]
            documents.append(json.dumps(bootstrap_config()).encode())
            # Only credential extraction/TLS/API ports are controlled. This is
            # a readiness-loop regression, not a real Kubernetes delivery claim.
            with patch.object(lab, "output", return_value=self.inspected(manifest)), \
                 patch.object(lab.subprocess, "check_output", side_effect=documents) as export, \
                 patch.object(lab.time, "monotonic", return_value=0), patch.object(lab.time, "sleep") as delay, \
                 patch.object(lab.ssl, "create_default_context") as tls, patch.object(lab, "kubernetes_opener") as opener:
                opener.return_value.open.side_effect = self.ready_responses()
                lab.kubernetes_ready(directory, manifest)
                self.assertEqual(export.call_count, len(documents))
                self.assertEqual(delay.call_count, len(documents) - 1)
                tls.assert_called_once_with(cafile=str(directory / "pki/kubernetes-ca.pem"))
                tls.return_value.load_cert_chain.assert_called_once_with(
                    directory / "pki/kubernetes-cert.pem", directory / "pki/kubernetes-key.pem")
                self.assertEqual([call.args[0] for call in opener.return_value.open.call_args_list],
                                 [manifest["kubernetes"]["endpoint"] + path for path in
                                  ("/readyz", "/api/v1/nodes", "/api/v1/namespaces/owned")])
            config = lab.read_json(Path(manifest["kubernetes"]["kubeconfig"]))
            self.assertEqual(config["clusters"][0]["cluster"]["server"], manifest["kubernetes"]["endpoint"])
            self.assertEqual(config["contexts"][0]["context"]["namespace"], "owned")
            for name in ("ca", "cert", "key"):
                path = directory / ("pki/kubernetes-" + name + ".pem")
                self.assertEqual(path.read_bytes(), ("test-" + name).encode())
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            self.assertEqual(Path(manifest["kubernetes"]["kubeconfig"]).stat().st_mode & 0o777, 0o600)

    def test_malformed_bootstrap_fails_at_original_deadline_without_export(self):
        for name, config in malformed_configs():
            with self.subTest(name=name), tempfile.TemporaryDirectory() as root:
                directory = Path(root)
                manifest = self.manifest(directory)
                with patch.object(lab, "output", return_value=self.inspected(manifest)), \
                     patch.object(lab.subprocess, "check_output", return_value=json.dumps(config).encode()), \
                     patch.object(lab.time, "monotonic", side_effect=[0, 181]), patch.object(lab.time, "sleep") as delay, \
                     patch.object(lab.ssl, "create_default_context") as tls:
                    with self.assertRaisesRegex(RuntimeError, "did not become ready within 180 seconds"):
                        lab.kubernetes_ready(directory, manifest)
                    tls.assert_not_called()
                    delay.assert_not_called()
                self.assertEqual(list((directory / "pki").iterdir()), [])

    def test_namespace_ownership_is_still_required_after_valid_credentials(self):
        with tempfile.TemporaryDirectory() as root:
            directory = Path(root)
            manifest = self.manifest(directory)
            with patch.object(lab, "output", return_value=self.inspected(manifest)), \
                 patch.object(lab.subprocess, "check_output", return_value=json.dumps(bootstrap_config()).encode()), \
                 patch.object(lab.ssl, "create_default_context"), patch.object(lab, "kubernetes_opener") as opener:
                opener.return_value.open.side_effect = self.ready_responses(owner="iw-lab-foreign")
                with self.assertRaisesRegex(ValueError, "namespace ownership mismatch"):
                    lab.kubernetes_ready(directory, manifest)
            self.assertFalse(Path(manifest["kubernetes"]["kubeconfig"]).exists())

    def test_nonready_node_is_not_exported_as_a_ready_cluster(self):
        with tempfile.TemporaryDirectory() as root:
            directory = Path(root)
            manifest = self.manifest(directory)
            with patch.object(lab, "output", return_value=self.inspected(manifest)), \
                 patch.object(lab.subprocess, "check_output", return_value=json.dumps(bootstrap_config()).encode()) as export, \
                 patch.object(lab.time, "monotonic", return_value=0), patch.object(lab.time, "sleep"), \
                 patch.object(lab.ssl, "create_default_context"), patch.object(lab, "kubernetes_opener") as opener:
                opener.return_value.open.side_effect = [Response({}), Response({"items": []})] + self.ready_responses()
                lab.kubernetes_ready(directory, manifest)
                self.assertEqual(export.call_count, 2)
                self.assertEqual(opener.return_value.open.call_count, 5)

    def test_oversized_kubeconfig_retains_its_hard_limit(self):
        with self.assertRaisesRegex(ValueError, "exceeds 64 KiB"):
            lab.kubernetes_credentials(b" " * 65537, {})

    def test_invalid_tls_identity_cannot_export_a_ready_config(self):
        with tempfile.TemporaryDirectory() as root:
            directory = Path(root)
            manifest = self.manifest(directory)
            with patch.object(lab, "output", return_value=self.inspected(manifest)), \
                 patch.object(lab.subprocess, "check_output", return_value=json.dumps(bootstrap_config()).encode()), \
                 patch.object(lab.time, "monotonic", side_effect=[0, 181]), \
                 patch.object(lab.ssl, "create_default_context") as tls, patch.object(lab, "kubernetes_opener") as opener:
                tls.return_value.load_cert_chain.side_effect = lab.ssl.SSLError("invalid test identity")
                with self.assertRaisesRegex(RuntimeError, "did not become ready within 180 seconds"):
                    lab.kubernetes_ready(directory, manifest)
                opener.assert_not_called()
            self.assertFalse(Path(manifest["kubernetes"]["kubeconfig"]).exists())
