import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from accept_progress import snapshot


class AcceptanceProgressTest(unittest.TestCase):
    def test_missing_trace_does_not_claim_completion(self):
        with tempfile.TemporaryDirectory() as directory:
            self.assertEqual(snapshot(directory), {"traces": [], "phase": "waiting-for-progress"})

    def test_partial_write_retains_last_complete_boundary_without_private_fields(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "progress.jsonl"
            row = {"event": "step_end", "scenario": "real plan", "step": 3,
                   "request": "credential-sentinel", "error": "private-error"}
            path.write_text(json.dumps(row) + '\n{"request":"partial-secret')
            result = snapshot(directory)
            self.assertEqual(result["traces"][0]["event"], "step_end")
            self.assertEqual(result["traces"][0]["step"], 3)
            rendered = json.dumps(result)
            for private in ("credential-sentinel", "private-error", "partial-secret"):
                self.assertNotIn(private, rendered)
            row.update(event="suite_end", exitCode=1)
            path.write_text(json.dumps(row) + "\n")
            self.assertEqual(snapshot(directory)["traces"][0]["exitCode"], 1)

    def test_cli_reports_real_file_and_bounds_values(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "capture-replays"
            path.mkdir()
            row = {"event": "scenario_start", "scenario": "x" * 10000,
                   "uri": {"secret": "nested-private"}}
            (path / "progress.jsonl").write_text(json.dumps(row) + "\n")
            result = subprocess.run([sys.executable, str(Path(__file__).with_name("accept_progress.py")),
                                     directory, "--once"], text=True, capture_output=True, check=True)
            observed = json.loads(result.stdout.removeprefix("acceptance progress "))
            self.assertEqual(observed["traces"][0]["file"], "capture-replays/progress.jsonl")
            self.assertEqual(len(observed["traces"][0]["scenario"]), 240)
            self.assertNotIn("nested-private", result.stdout)


if __name__ == "__main__":
    unittest.main()
