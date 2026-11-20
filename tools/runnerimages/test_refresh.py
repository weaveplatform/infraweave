"""Updater transaction and stable-backport contracts; native proof is separate."""

import importlib.util
import json
from pathlib import Path
import shutil
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("refresh", Path(__file__).with_name("refresh.py"))
refresh = importlib.util.module_from_spec(spec)
spec.loader.exec_module(refresh)


class RefreshTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="infraweave-updater-test-")
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name) / "images"
        shutil.copytree(refresh.HERE, self.root, ignore=shutil.ignore_patterns("__pycache__", "images.lock.json"))
        data = json.loads((self.root / "versions.json").read_text())
        data["tools"]["terraform"]["version"] = "1.17.1"
        data["revision"] = 1
        (self.root / "versions.json").write_text(json.dumps(data))
        state = json.loads((self.root / "upstream.json").read_text())
        state["seen"]["terraform"] = ["1.17.1"]
        (self.root / "upstream.json").write_text(json.dumps(state))
        self.original = self.snapshot()

    def snapshot(self):
        return {str(p.relative_to(self.root)): p.read_bytes() for p in self.root.rglob("*") if p.is_file()}

    def invoke(self, releases, checksums):
        with patch.object(refresh, "HERE", self.root), patch.object(refresh.ctl, "HERE", self.root), \
             patch.object(refresh, "releases", side_effect=releases), \
             patch.object(refresh, "checksums", side_effect=checksums), \
             patch.object(refresh.ctl, "generate"):
            refresh.refresh()

    def test_backports_are_released_without_downgrading_default_and_duplicates_are_noops(self):
        def releases(tool, _since):
            return ["1.17.2", "1.16.6"] if tool == "terraform" else []

        def checksums(_tool, version):
            return {f"terraform_{version}_linux_{arch}.zip": "a" * 64 for arch in ("amd64", "arm64")}

        self.invoke(releases, checksums)
        data = json.loads((self.root / "versions.json").read_text())
        self.assertEqual(data["tools"]["terraform"]["version"], "1.17.2")
        self.assertEqual([e["version"] for e in data["cohorts"]["terraform"]], ["1.16.6"])
        self.assertEqual(data["revision"], 2)
        first = self.snapshot()
        self.invoke(releases, checksums)
        self.assertEqual(self.snapshot(), first)

    def test_failure_after_a_successful_lookup_changes_no_input_or_pin(self):
        def releases(tool, _since):
            if tool == "opentofu":
                raise OSError("upstream temporarily unavailable")
            return ["1.17.2"] if tool == "terraform" else []

        with self.assertRaisesRegex(OSError, "upstream temporarily unavailable"):
            self.invoke(releases, lambda _tool, _version: {"archive": "a" * 64})
        self.assertEqual(self.snapshot(), self.original)


if __name__ == "__main__":
    unittest.main()
