"""Exercise the real PR helper's duplicate path against a real bare Git remote."""

from pathlib import Path
import os
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("pr.sh").resolve()


class PendingPRTests(unittest.TestCase):
    def test_identical_pending_update_does_not_commit_push_or_restart_ci(self):
        self.exercise(True)

    def test_missing_pr_is_recovered_without_replacing_the_already_pushed_commit(self):
        self.exercise(False)

    def exercise(self, pending):
        with tempfile.TemporaryDirectory(prefix="infraweave-pr-helper-") as directory:
            root = Path(directory)
            remote, checkout = root / "remote.git", root / "checkout"
            subprocess.run(["git", "init", "--bare", "-b", "main", str(remote)], check=True, capture_output=True)
            subprocess.run(["git", "init", "-b", "main", str(checkout)], check=True, capture_output=True)

            def git(*args):
                return subprocess.check_output(["git", "-C", str(checkout), *args], text=True, stderr=subprocess.DEVNULL).strip()

            git("config", "user.name", "Image updater test")
            git("config", "user.email", "test@example.invalid")
            (checkout / "versions.json").write_text('{"revision":1}\n')
            git("add", "versions.json")
            git("commit", "-m", "test: initial inputs")
            git("remote", "add", "origin", str(remote))
            git("push", "origin", "main")
            branch = "deps/runner-image-inputs"
            git("switch", "-c", branch)
            (checkout / "versions.json").write_text('{"revision":2}\n')
            git("commit", "-am", "test: pending image update")
            git("push", "origin", branch)
            expected = git("rev-parse", "HEAD")
            git("switch", "main")
            head = git("rev-parse", "HEAD")
            (checkout / "versions.json").write_text('{"revision":2}\n')
            commands = root / "bin"
            commands.mkdir()
            state, creates = root / "pr-state", root / "pr-creates"
            state.write_text("123\n" if pending else "")
            gh = commands / "gh"
            gh.write_text('''#!/bin/sh
case "$1 $2" in
  "pr list") cat "$TEST_PR_STATE" ;;
  "pr create") printf '123\\n' > "$TEST_PR_STATE"; printf 'created\\n' >> "$TEST_PR_CREATES" ;;
  *) exit 2 ;;
esac
''')
            gh.chmod(0o700)
            env = dict(os.environ, PATH=str(commands) + os.pathsep + os.environ["PATH"],
                       TEST_PR_STATE=str(state), TEST_PR_CREATES=str(creates))
            for iteration in range(2):
                result = subprocess.run(["bash", str(SCRIPT), branch, "chore(images): update",
                                         str(root / "unused-body.md"), "versions.json"],
                                        cwd=checkout, check=True, capture_output=True, text=True, env=env)
                expected_message = "pending PR already has these exact updates" if pending or iteration else "Restored the missing PR"
                self.assertIn(expected_message, result.stdout)
                self.assertEqual(git("rev-parse", "HEAD"), head)
                self.assertEqual(git("branch", "--show-current"), "main")
                self.assertEqual(git("ls-remote", "origin", "refs/heads/" + branch).split()[0], expected)
                git("reset", "--", "versions.json")
            self.assertEqual(creates.read_text().splitlines() if creates.exists() else [], [] if pending else ["created"])


if __name__ == "__main__":
    unittest.main()
