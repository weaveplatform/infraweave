"""Recipe identity must be portable and independent of publication outputs."""

import importlib.util
from pathlib import Path
import shutil
import tempfile
import unittest
from unittest.mock import patch

path = Path(__file__).resolve().parents[2] / "deploy/images/imagectl.py"
spec = importlib.util.spec_from_file_location("imagectl_catalog_test", path)
ctl = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ctl)


class CatalogTests(unittest.TestCase):
    def test_recipe_identity_survives_worktree_location_and_output_pin_changes(self):
        expected = ctl.recipe()
        with tempfile.TemporaryDirectory(prefix="infraweave-recipe-test-") as directory:
            destination = Path(directory) / "tests/runner-checkout/deploy/images"
            shutil.copytree(ctl.HERE, destination, ignore=shutil.ignore_patterns("__pycache__"))
            with patch.object(ctl, "HERE", destination):
                self.assertEqual(ctl.recipe(), expected)
                (destination / "images.lock.json").write_text('{"different":"published outputs"}\n')
                (destination / "README.md").write_text("Different operational documentation\n")
                self.assertEqual(ctl.recipe(), expected)
                with (destination / "runner-terraform/Dockerfile").open("a") as dockerfile:
                    dockerfile.write("\n# changed recipe\n")
                self.assertNotEqual(ctl.recipe(), expected)


if __name__ == "__main__":
    unittest.main()
