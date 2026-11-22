import copy
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

from fips_gate import catalogue, identity, record, validate


class FipsGateTest(unittest.TestCase):
    def setUp(self):
        self.matrix = {"include": [{"shard": "native", "partition": "", "timeout": 20},
                                   {"shard": "core", "partition": "core", "timeout": 20}]}
        self.source = {"head": "head", "base": "base", "checkout": "merge", "tree": "tree", "run": "42", "attempt": 2}
        self.children = {"fips-unit": "success", "fips-tests": "success"}
        self.results = [{"schema": 1, "shard": shard, "status": "success", "seconds": 1, **self.source,
                         "scenarios": 3, "steps": 8, "cryptoMode": "FIPS 140-3 mode only, Go Cryptographic Module v1.0.0"}
                        for shard in ("unit", "native", "core")]

    def test_complete_same_source_partial_rerun_reuses_prior_success(self):
        self.results[0]["attempt"] = 1
        self.results.append(dict(self.results[2], status="failure", attempt=1))
        summary = validate(self.matrix, self.results, self.children, self.source)
        self.assertTrue(summary["passed"])
        self.assertEqual([row["attempt"] for row in summary["shards"]], [2, 2, 1])

    def test_every_child_failure_cancellation_skip_or_absence_refuses_gate(self):
        for child in self.children:
            for status in ("failure", "cancelled", "skipped", None):
                with self.subTest(child=child, status=status):
                    children = dict(self.children)
                    if status is None:
                        del children[child]
                    else:
                        children[child] = status
                    with self.assertRaises(ValueError):
                        validate(self.matrix, self.results, children, self.source)

    def test_receipts_cannot_hide_missing_failed_or_duplicate_work(self):
        for bad in ("missing", "duplicate", "unexpected", "failed", "cancelled", "skipped", "future", "source", "tree", "run", "mode", "empty"):
            with self.subTest(bad=bad):
                rows = copy.deepcopy(self.results)
                if bad == "missing":
                    rows.pop()
                elif bad == "duplicate":
                    rows.append(dict(rows[-1]))
                elif bad == "unexpected":
                    rows[-1]["shard"] = "unreviewed"
                elif bad in ("failed", "cancelled", "skipped"):
                    rows.append(dict(rows[-1], status="success", attempt=1))
                    rows[-2]["status"] = bad
                elif bad == "future":
                    rows[-1]["attempt"] = 3
                elif bad in ("source", "tree", "run"):
                    rows[-1][{"source": "checkout"}.get(bad, bad)] = "other"
                elif bad == "mode":
                    rows[1]["cryptoMode"] = "FIPS 140-3 mode off"
                elif bad == "empty":
                    rows[-1]["scenarios"] = 0
                with self.assertRaises(ValueError):
                    validate(self.matrix, rows, self.children, self.source)

    def test_matrix_cannot_duplicate_or_silently_reselect_work(self):
        for bad in ("duplicate", "missing-native", "unit", "selector", "budget"):
            with self.subTest(bad=bad):
                matrix = copy.deepcopy(self.matrix)
                if bad == "duplicate":
                    matrix["include"].append(dict(matrix["include"][-1]))
                elif bad == "missing-native":
                    matrix["include"].pop(0)
                elif bad == "unit":
                    matrix["include"][-1]["shard"] = "unit"
                elif bad == "selector":
                    matrix["include"][-1]["partition"] = ""
                elif bad == "budget":
                    matrix["include"][-1]["timeout"] = 10
                with self.assertRaises(ValueError):
                    catalogue(matrix)

    def test_partition_receipt_requires_actual_passed_steps_and_bounds_fields(self):
        with tempfile.TemporaryDirectory() as root:
            directory = Path(root)
            (directory / "started").write_text(str(int(time.time()) - 3))
            (directory / "cover/accept").mkdir(parents=True)
            report = directory / "cover/accept/cucumber.json"
            for status in ("passed", "failed", "skipped", "undefined", "pending"):
                report.write_text(json.dumps([{"elements": [{"type": "scenario", "name": "private-input",
                    "steps": [{"result": {"status": status}, "text": "private-secret"}]}]}]))
                if status == "passed":
                    result = record("core", "success", directory / "started", directory, self.source)
                    self.assertEqual((result["scenarios"], result["steps"]), (1, 1))
                    self.assertGreaterEqual(result["seconds"], 3)
                    self.assertNotIn("private", json.dumps(result))
                else:
                    with self.assertRaises(ValueError):
                        record("core", "success", directory / "started", directory, self.source)
            report.write_text("[]")
            with self.assertRaises(ValueError):
                record("core", "success", directory / "started", directory, self.source)

    def test_real_cli_writes_summary_and_exits_unsuccessfully_for_missing_work(self):
        env = dict(os.environ, PR_HEAD="head", PR_BASE="base", GITHUB_RUN_ID="42", GITHUB_RUN_ATTEMPT="2",
                   FIPS_CHILDREN=json.dumps({key: {"result": value} for key, value in self.children.items()}))
        with patch.dict(os.environ, env):
            source = identity()
        with tempfile.TemporaryDirectory() as root:
            directory = Path(root)
            for row in self.results:
                child = directory / row["shard"]
                child.mkdir()
                (child / "result.json").write_text(json.dumps(dict(row, **source)))
            command = [sys.executable, str(Path(__file__).with_name("fips_gate.py")), "check",
                       "--directory", root, "--matrix", json.dumps(self.matrix)]
            completed = subprocess.run(command, env=env, text=True, capture_output=True, timeout=10)
            self.assertEqual(completed.returncode, 0, completed.stderr)
            self.assertTrue(json.loads((directory / "summary.json").read_text())["passed"])
            (directory / "core/result.json").unlink()
            completed = subprocess.run(command, env=env, text=True, capture_output=True, timeout=10)
            self.assertNotEqual(completed.returncode, 0)
            self.assertNotIn("Certified FIPS passed", completed.stdout)


if __name__ == "__main__":
    unittest.main()
