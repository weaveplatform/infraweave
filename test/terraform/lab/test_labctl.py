"""The controller must retain ownership and failure evidence on every exit."""

import argparse
import base64
from contextlib import contextmanager
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib.util
import json
import os
from pathlib import Path
import signal
import ssl
import sys
import tempfile
import threading
import unittest
import urllib.error
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("labctl", Path(__file__).with_name("labctl.py"))
lab = importlib.util.module_from_spec(spec)
spec.loader.exec_module(lab)


@contextmanager
def startup_service(responses):
    """A real HTTP listener may close requests before its application is ready."""
    attempts = {}

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            attempt = attempts.get(self.path, 0)
            attempts[self.path] = attempt + 1
            response = responses[min(attempt, len(responses) - 1)]
            self.close_connection = True
            if response == "disconnect":
                return
            self.send_response(response)
            self.send_header("Content-Length", "0")
            self.end_headers()

        def log_message(self, *_args):
            pass

    with ThreadingHTTPServer(("127.0.0.1", 0), Handler) as server:
        worker = threading.Thread(target=server.serve_forever, daemon=True)
        worker.start()
        try:
            yield "http://127.0.0.1:" + str(server.server_port), attempts
        finally:
            server.shutdown()
            worker.join(timeout=2)
            if worker.is_alive():
                raise RuntimeError("readiness test HTTP server did not stop")


class BatchTests(unittest.TestCase):
    def test_process_group_escalates_and_joins_a_term_resistant_child(self):
        command = [sys.executable, "-c",
                   "import signal,time; signal.signal(signal.SIGTERM,signal.SIG_IGN); print('ready',flush=True); time.sleep(60)"]
        with lab.subprocess.Popen(command, start_new_session=True, stdout=lab.subprocess.PIPE, text=True) as process:
            self.assertEqual(process.stdout.readline(), "ready\n")
            try:
                lab.terminate_process_group(process, grace=0.1)
                self.assertEqual(process.returncode, -signal.SIGKILL)
                with self.assertRaises(ProcessLookupError):
                    os.killpg(process.pid, 0)
            finally:
                if process.poll() is None:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait(timeout=5)

    def test_kubernetes_readiness_opener_never_contacts_redirect_target(self):
        with startup_service([200]) as (target, target_attempts):
            class Redirect(BaseHTTPRequestHandler):
                def do_GET(self):
                    self.send_response(307)
                    self.send_header("Location", target + "/readyz")
                    self.send_header("Content-Length", "0")
                    self.end_headers()
                def log_message(self, *_args):
                    pass
            with ThreadingHTTPServer(("127.0.0.1", 0), Redirect) as server:
                worker = threading.Thread(target=server.serve_forever, daemon=True)
                worker.start()
                try:
                    opener = lab.kubernetes_opener(ssl.create_default_context())
                    with self.assertRaises(urllib.error.HTTPError) as refused:
                        opener.open("http://127.0.0.1:" + str(server.server_port) + "/readyz", timeout=2)
                    self.assertEqual(refused.exception.code, 307)
                    refused.exception.close()
                    self.assertEqual(target_attempts, {})
                finally:
                    server.shutdown()
                    worker.join(timeout=2)
                    self.assertFalse(worker.is_alive())

    def test_terraform_timeout_and_cancellation_join_real_provider_descendant(self):
        for cancellation in (False, True):
            with self.subTest(cancellation=cancellation), tempfile.TemporaryDirectory() as root:
                directory = Path(root)
                child_pid = directory / "provider.pid"
                executable = directory / "terraform"
                executable.write_text("#!" + sys.executable + "\n" +
                    "import signal,subprocess,sys,time\n" +
                    "child=subprocess.Popen([sys.executable,'-c','import time; time.sleep(60)'])\n" +
                    "def stop(*_args):\n    child.wait(timeout=5)\n    sys.exit(0)\n" +
                    "signal.signal(signal.SIGTERM,stop)\n" +
                    "open(" + repr(str(child_pid)) + ",'w').write(str(child.pid))\n" +
                    "time.sleep(60)\n")
                executable.chmod(0o700)
                processes = []
                popen = lab.subprocess.Popen
                def start(*args, **kwargs):
                    process = popen(*args, **kwargs)
                    processes.append(process)
                    if cancellation:
                        communicate = process.communicate
                        def cancel(timeout):
                            try:
                                return communicate(timeout=timeout)
                            except lab.subprocess.TimeoutExpired:
                                raise KeyboardInterrupt from None
                        process.communicate = cancel
                    return process
                expected = KeyboardInterrupt if cancellation else lab.subprocess.TimeoutExpired
                with patch.object(lab, "environment", return_value=os.environ.copy()), \
                     patch.object(lab, "TERRAFORM_TIMEOUT", 0.5), patch.object(lab.subprocess, "Popen", side_effect=start):
                    with self.assertRaises(expected):
                        lab.terraform(directory, str(executable), "apply", capture=True)
                self.assertEqual(len(processes), 1)
                self.assertEqual(processes[0].returncode, 0)
                self.assertTrue(processes[0].stdout.closed)
                with self.assertRaises(ProcessLookupError):
                    os.kill(int(child_pid.read_text()), 0)
                with self.assertRaises(ProcessLookupError):
                    os.killpg(processes[0].pid, 0)

    def test_cancelled_stream_terminates_and_joins_its_child(self):
        processes = []
        popen = lab.subprocess.Popen
        def start(*args, **kwargs):
            process = popen(*args, **kwargs)
            processes.append(process)
            return process
        class CancelledLog:
            def write(self, _line):
                raise RuntimeError("cancelled test stream")
        with tempfile.TemporaryDirectory() as root, patch.object(lab.subprocess, "Popen", side_effect=start):
            with self.assertRaisesRegex(RuntimeError, "cancelled test stream"):
                lab.stream_command([sys.executable, "-c", "import time; print('ready', flush=True); time.sleep(60)"],
                                   cwd=root, env=os.environ.copy(), log=CancelledLog())
        self.assertEqual(len(processes), 1)
        self.assertEqual(processes[0].returncode, -signal.SIGTERM)
        self.assertTrue(processes[0].stdout.closed)

    def test_http_readiness_survives_actual_startup_disconnects(self):
        # Both HTTP probes must observe 200 before PostgreSQL/bootstrap starts.
        # Only the unrelated Docker commands are replaced in this socket test.
        with tempfile.TemporaryDirectory() as root, startup_service(["disconnect", 503, 200]) as (url, attempts):
            manifest = {"objectstore": {"endpoint": url}, "gitea": {"url": url, "caFile": None, "container": "gitea"},
                        "postgres": {"container": "postgres"}}
            with patch.object(lab.subprocess, "run") as command:
                command.return_value.returncode = 0
                lab.ready(Path(root), manifest)
                self.assertEqual(command.call_count, 2)
            self.assertEqual(attempts, {"/health/ready": 3, "/api/healthz": 3})

    def test_http_readiness_disconnect_still_fails_at_original_deadline(self):
        with tempfile.TemporaryDirectory() as root, startup_service(["disconnect"]) as (url, attempts):
            manifest = {"objectstore": {"endpoint": url}, "gitea": {"url": url, "caFile": None}}
            # Advance the clock across the existing 90s budget without making
            # the regression wait 90 real seconds. The service never answers.
            with patch.object(lab.time, "monotonic", side_effect=[0, 91]), patch.object(lab.subprocess, "run") as command:
                with self.assertRaisesRegex(RuntimeError, "services did not become ready"):
                    lab.ready(Path(root), manifest)
                command.assert_not_called()
            self.assertEqual(attempts, {"/health/ready": 1})

    def test_kubernetes_foreign_owner_cannot_export_credentials(self):
        manifest = {"owner": "iw-lab-owned", "batch": "kubernetes", "kubernetes": {"container": "cluster"}}
        inspected = [{"Config": {"Labels": {lab.OWNER_LABEL: "iw-lab-foreign"}}}]
        with tempfile.TemporaryDirectory() as root, patch.object(lab, "output", return_value=json.dumps(inspected)), \
             patch.object(lab.subprocess, "check_output") as export:
            with self.assertRaisesRegex(RuntimeError, "ownership mismatch"):
                lab.kubernetes_ready(Path(root), manifest)
            export.assert_not_called()

    def test_cleanup_refuses_another_invocations_terraform_state(self):
        with tempfile.TemporaryDirectory() as root:
            directory = Path(root)
            lab.write_json(directory / "deployment.json", {"owner": "iw-lab-owned", "terraform": "terraform"})
            (directory / "terraform.tfstate").write_text("state")
            foreign = {"values": {"root_module": {"resources": [{"type": "docker_container", "values": {
                "labels": [{"label": lab.OWNER_LABEL, "value": "iw-lab-foreign"}]}}]}}}
            with patch.object(lab, "owned_resources", return_value={}), \
                 patch.object(lab, "terraform", side_effect=["docker_container.kubernetes[0]", json.dumps(foreign)]) as tf:
                with self.assertRaisesRegex(RuntimeError, "refusing destroy"):
                    lab.down(directory)
                self.assertEqual(tf.call_count, 2)
                self.assertTrue(all("destroy" not in call.args for call in tf.call_args_list))
            self.assertTrue((directory / "deployment.json").exists())

    def test_report_must_execute_all_steps(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "report.json"
            for status in ("passed", "failed", "skipped", "undefined", "pending"):
                with self.subTest(status=status):
                    lab.write_json(path, [{"elements": [{"type": "scenario", "steps": [{"result": {"status": status}}]}]}])
                    if status == "passed":
                        self.assertEqual(lab.census(path), {"scenarios": 1, "steps": 1, "passed": True})
                    else:
                        with self.assertRaises(RuntimeError):
                            lab.census(path)
            for report in ([], [{"elements": [{"type": "scenario", "steps": []}]}]):
                lab.write_json(path, report)
                with self.assertRaises(RuntimeError):
                    lab.census(path)
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)

    def test_definitions_are_bounded_and_immutable(self):
        self.assertEqual(lab.batch("policies")["tags"], "@policies")
        self.assertEqual(lab.batch("vcs")["partition"], "vcs-lab")
        with self.assertRaises(ValueError):
            lab.batch("../outside")
        for reference in lab.pins().values():
            self.assertRegex(reference, r"@sha256:[0-9a-f]{64}$")
        with patch.object(lab, "output") as command:
            with self.assertRaises(ValueError):
                lab.owned_resources("unrelated-container")
            command.assert_not_called()

    def test_failed_and_successful_commands_always_cleanup(self):
        for status in (0, 1):
            with self.subTest(status=status), tempfile.TemporaryDirectory() as root:
                directory = Path(root) / "new-batch"
                args = argparse.Namespace(directory=str(directory), batch="policies", terraform="terraform",
                                          helper="infraweave-lab", features=None, tags=None, fips=True)
                def provision(directory, name, executable, helper, owner):
                    directory.mkdir()
                    lab.write_json(directory / "deployment.json", {"owner": owner})
                def execute(_command, **kwargs):
                    report = Path(kwargs["env"]["INFRAWEAVE_ACCEPT_CUCUMBER"])
                    lab.write_json(report, [{"elements": [{"type": "scenario", "steps": [{"result": {"status": "passed"}}]}]}])
                    self.assertEqual(kwargs["env"]["GOFIPS140"], "certified")
                    self.assertEqual(kwargs["env"]["GODEBUG"], "fips140=only")
                    self.assertEqual(kwargs["env"]["INFRAWEAVE_ACCEPT_REQUIRE_DOCKER"], "1")
                    self.assertNotIn("INFRAWEAVE_ACCEPT_BINARY", kwargs["env"])
                    return status
                with patch.object(lab, "up", side_effect=provision), patch.object(lab, "output", return_value="revision"), patch.object(lab, "terraform_version", return_value="1.17.2"), \
                     patch.object(lab, "stream_command", side_effect=execute), patch.object(lab, "down", return_value={"passed": True}) as cleanup:
                    if status:
                        with self.assertRaises(RuntimeError):
                            lab.run(args)
                    else:
                        self.assertTrue(lab.run(args)["passed"])
                    cleanup.assert_called_once_with(directory.resolve())
                result = lab.read_json(directory / "result.json")
                self.assertEqual(result["passed"], status == 0)
                self.assertTrue(result["cleanup"]["passed"])

    def test_cleanup_failure_cannot_report_success(self):
        with tempfile.TemporaryDirectory() as root:
            directory = Path(root) / "new-batch"
            args = argparse.Namespace(directory=str(directory), batch="policies", terraform="terraform",
                                      helper="infraweave-lab", features=None, tags=None, fips=False)
            def provision(directory, name, executable, helper, owner):
                directory.mkdir()
                lab.write_json(directory / "deployment.json", {"owner": owner})
            with patch.object(lab, "up", side_effect=provision), patch.object(lab, "output", return_value="revision"), patch.object(lab, "terraform_version", return_value="1.17.2"), \
                 patch.object(lab, "stream_command", return_value=0), \
                 patch.object(lab, "census", return_value={"passed": True}), \
                 patch.object(lab, "down", side_effect=RuntimeError("cleanup failed")):
                with self.assertRaisesRegex(RuntimeError, "cleanup failed"):
                    lab.run(args)
            self.assertFalse(lab.read_json(directory / "result.json")["passed"])

    def test_failure_diagnostics_never_publish_the_lab_private_key(self):
        with tempfile.TemporaryDirectory() as root:
            directory = Path(root)
            (directory / "pki").mkdir()
            key = "-----BEGIN PRIVATE KEY-----\nlocal-test-key\n-----END PRIVATE KEY-----\n"
            (directory / "pki/gitea-key.pem").write_text(key)
            representations = [key, json.dumps(key)[1:-1], base64.b64encode(key.encode()).decode()]
            (directory / "terraform.log").write_text("\n".join(representations) + "\nresource creation failed")
            lab.retain_diagnostics(directory)
            published = (directory / "evidence/terraform.log").read_text()
            for value in representations:
                self.assertNotIn(value, published)
            self.assertIn("resource creation failed", published)

    def test_existing_directory_is_never_adopted_or_cleaned(self):
        with tempfile.TemporaryDirectory() as root:
            directory = Path(root)
            journal = {"owner": "iw-lab-another-invocation"}
            lab.write_json(directory / "deployment.json", journal)
            args = argparse.Namespace(directory=str(directory), batch="policies", terraform="terraform",
                                      helper="infraweave-lab", features=None, tags=None, fips=False)
            with patch.object(lab, "output", return_value="revision"), patch.object(lab, "terraform_version", return_value="1.17.2"), patch.object(lab, "down") as cleanup:
                with self.assertRaises(FileExistsError):
                    lab.run(args)
                cleanup.assert_not_called()
            self.assertEqual(lab.read_json(directory / "deployment.json"), journal)
            self.assertFalse((directory / "result.json").exists())


if __name__ == "__main__":
    unittest.main()
