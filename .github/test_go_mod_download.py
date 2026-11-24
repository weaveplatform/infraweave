import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("actions") / "go-mod-download" / "download.sh"

# A go that records each call and fails the first STUB_FAILURES calls whose
# arguments start with STUB_FAIL_ON, as a dropped proxy connection does, and
# a sleep that records its delay rather than waiting.
STUB_GO = """#!/usr/bin/env bash
echo "$*" >> "$STUB_DIR/calls"
case "$*" in
"$STUB_FAIL_ON"*)
	n=$(( $(cat "$STUB_DIR/failed" 2>/dev/null || echo 0) + 1 ))
	if [ "$n" -le "$STUB_FAILURES" ]; then
		echo "$n" > "$STUB_DIR/failed"
		echo "go: example.com/m@v1.0.0: stream error: stream ID 547; INTERNAL_ERROR; received from peer" >&2
		exit 1
	fi
	;;
esac
"""
STUB_SLEEP = """#!/usr/bin/env bash
echo "$1" >> "$STUB_DIR/sleeps"
"""


class GoModDownloadTest(unittest.TestCase):
    def run_script(self, fail_on, failures, *tools):
        directory = Path(self.enterContext(tempfile.TemporaryDirectory()))
        for name, body in (("go", STUB_GO), ("sleep", STUB_SLEEP)):
            (directory / name).write_text(body)
            (directory / name).chmod(0o755)
        env = dict(os.environ, PATH=f"{directory}{os.pathsep}{os.environ['PATH']}", STUB_DIR=str(directory),
                   STUB_FAIL_ON=fail_on, STUB_FAILURES=str(failures))
        result = subprocess.run(["bash", str(SCRIPT), *tools], env=env, text=True, capture_output=True)

        def lines(name):
            path = directory / name
            return path.read_text().splitlines() if path.exists() else []

        return result, lines("calls"), lines("sleeps")

    def test_downloads_the_module_graph_then_each_tool(self):
        result, calls, sleeps = self.run_script("", 0, "example.com/a/cmd/a@v1.2.3", "example.com/b@v0.1.0")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, ["mod download", "install -n example.com/a/cmd/a@v1.2.3",
                                 "install -n example.com/b@v0.1.0"])
        self.assertEqual(sleeps, [])

    def test_transient_failure_is_retried_after_a_delay(self):
        result, calls, sleeps = self.run_script("mod download", 1)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, ["mod download", "mod download"])
        self.assertEqual(sleeps, ["10"])
        self.assertIn("::warning::'go mod download' failed (attempt 1 of 3); retrying in 10s", result.stderr)

    def test_tool_resolution_is_retried_and_its_output_shown_on_failure(self):
        result, calls, sleeps = self.run_script("install", 2, "example.com/a@v1.2.3")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, ["mod download"] + ["install -n example.com/a@v1.2.3"] * 3)
        self.assertEqual(sleeps, ["10", "30"])
        self.assertIn("INTERNAL_ERROR", result.stderr)

    def test_persistent_failure_fails_the_step_after_three_attempts(self):
        result, calls, sleeps = self.run_script("mod download", 99, "example.com/a@v1.2.3")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(calls, ["mod download"] * 3)
        self.assertEqual(sleeps, ["10", "30"])
        self.assertIn("::error::'go mod download' failed 3 times; the Go module proxy is unreachable or a module is"
                      " missing", result.stderr)


if __name__ == "__main__":
    unittest.main()
