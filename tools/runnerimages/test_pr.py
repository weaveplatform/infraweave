"""Exercise the real PR helper's duplicate path against a real bare Git remote."""

from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("pr.sh").resolve()


class PendingPRTests(unittest.TestCase):
    def test_identical_pending_update_does_not_commit_push_or_restart_ci(self):
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
            for _ in range(2):
                result = subprocess.run(["bash", str(SCRIPT), branch, "chore(images): update",
                                         str(root / "unused-body.md"), "versions.json"],
                                        cwd=checkout, check=True, capture_output=True, text=True)
                self.assertIn("pending PR already has these exact updates", result.stdout)
                self.assertEqual(git("rev-parse", "HEAD"), head)
                self.assertEqual(git("branch", "--show-current"), "main")
                self.assertEqual(git("ls-remote", "origin", "refs/heads/" + branch).split()[0], expected)
                git("reset", "--", "versions.json")


if __name__ == "__main__":
    unittest.main()
