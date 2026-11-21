"""The controller must retain ownership and failure evidence on every exit."""

import argparse
import base64
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("labctl", Path(__file__).with_name("labctl.py"))
lab = importlib.util.module_from_spec(spec)
spec.loader.exec_module(lab)


class BatchTests(unittest.TestCase):
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
        self.assertEqual(lab.batch("policies")["tags"], "@policy-phase")
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
