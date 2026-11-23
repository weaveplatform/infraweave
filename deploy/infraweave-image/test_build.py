"""Unit tests of build.py: python3 -m unittest discover -s deploy/infraweave-image."""

import importlib.util
import json
import tempfile
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("build", HERE / "build.py")
build = importlib.util.module_from_spec(spec)
spec.loader.exec_module(build)

DIGEST = "sha256:" + "a" * 64


class PinValues(unittest.TestCase):
    def test_pins_the_chart_values_image(self):
        text = (build.CHART / "values.yaml").read_text()
        pinned = build.pin_values(text, "ghcr.io/weaveplatform/infraweave-worker@" + DIGEST)
        self.assertIn(f'    digest: "{DIGEST}"\n', pinned)
        self.assertEqual(pinned.count("digest: \"\""), text.count("digest: \"\"") - 1)

    def test_refuses_a_tag_or_a_moved_block(self):
        text = (build.CHART / "values.yaml").read_text()
        with self.assertRaises(ValueError):
            build.pin_values(text, "ghcr.io/weaveplatform/infraweave-worker:latest")
        with self.assertRaises(ValueError):
            build.pin_values("controller: {}\n", "ghcr.io/weaveplatform/infraweave-worker@" + DIGEST)


class CheckVersion(unittest.TestCase):
    def test_accepts_the_build_in_fips_mode(self):
        out = "infraweave 0.1.0 (commit abc, built 2026-11-23T00:00:00Z)\nFIPS 140-3 mode on, Go Cryptographic Module v1.0.0\n"
        self.assertEqual(build.check_version(out, "0.1.0", "abc"), "v1.0.0")

    def test_refuses_another_build_or_mode(self):
        for out in (
            "infraweave 0.2.0 (commit abc, built x)\nFIPS 140-3 mode on, Go Cryptographic Module v1.0.0\n",
            "infraweave 0.1.0 (commit def, built x)\nFIPS 140-3 mode on, Go Cryptographic Module v1.0.0\n",
            "infraweave 0.1.0 (commit abc, built x)\nFIPS 140-3 mode off, Go Cryptographic Module v1.0.0\n",
            "infraweave 0.1.0 (commit abc, built x)\nFIPS 140-3 mode on, Go Cryptographic Module latest\n",
            "infraweave 0.1.0 (commit abc, built x)\n",
        ):
            with self.assertRaises(ValueError, msg=out):
                build.check_version(out, "0.1.0", "abc")


class Describe(unittest.TestCase):
    def test_records_every_pin(self):
        with tempfile.TemporaryDirectory() as directory:
            context = Path(directory)
            (context / "SHA256SUMS").write_text(f"{'b' * 64}  infraweave-linux-amd64\n{'c' * 64}  infraweave-linux-arm64\n")
            package = context / "infraweave-workers-0.1.0.tgz"
            package.write_bytes(b"chart")
            lock = context / "images.lock.json"
            lock.write_text(json.dumps({"schema": 1, "images": {
                "terraform": {"reference": "ghcr.io/weaveplatform/infraweave-runner-terraform@" + DIGEST}}}))
            image = "ghcr.io/weaveplatform/infraweave-worker@" + DIGEST
            platforms = {"linux/amd64": DIGEST, "linux/arm64": DIGEST}
            value = build.describe("0.1.0", "abc", image, platforms, context,
                                   "oci://ghcr.io/weaveplatform/charts/infraweave-workers", DIGEST, package, lock)
            self.assertEqual(value["controller_image"], image)
            self.assertEqual(value["bootstrap_image"], image)
            self.assertEqual(value["binaries"], {"linux/amd64": "sha256:" + "b" * 64, "linux/arm64": "sha256:" + "c" * 64})
            self.assertEqual(value["chart"]["sha256"], "sha256:" + build.sha256(package))
            self.assertEqual(value["runner_images"], {"terraform": "ghcr.io/weaveplatform/infraweave-runner-terraform@" + DIGEST})
            self.assertEqual(value["signature"]["identity"], build.SIGNER)
            with self.assertRaises(ValueError):
                build.describe("0.1.0", "abc", "ghcr.io/weaveplatform/infraweave-worker:0.1.0", platforms, context,
                               "oci://x", DIGEST, package, lock)

    def test_the_recipe_follows_the_binaries(self):
        with tempfile.TemporaryDirectory() as directory:
            context = Path(directory)
            (context / "SHA256SUMS").write_text("one\n")
            first = build.recipe(context, "worker")
            (context / "SHA256SUMS").write_text("two\n")
            self.assertNotEqual(first, build.recipe(context, "worker"))
            self.assertNotEqual(build.recipe(context, "worker"), build.recipe(context, "server"))
            self.assertRegex(first, r"^sha256:[0-9a-f]{64}$")

    def test_the_repository_runner_pins_are_read(self):
        images = build.runner_images(build.LOCK)
        self.assertTrue(images)
        for reference in images.values():
            self.assertRegex(reference, r"@sha256:[0-9a-f]{64}$")


class Server(unittest.TestCase):
    def test_both_images_share_one_base(self):
        bases = {kind: [line for line in image.dockerfile.read_text().splitlines() if line.startswith("FROM ")]
                 for kind, image in build.IMAGES.items()}
        self.assertEqual(len(bases["server"]), 1)
        self.assertEqual(bases["server"], bases["worker"])
        self.assertRegex(bases["server"][0], r"@sha256:[0-9a-f]{64}$")

    def test_the_contract_is_the_kinds(self):
        build.check_contract({"User": "65532:65532", "Entrypoint": [build.BINARY, "all"],
                              "ExposedPorts": {"8080/tcp": {}, "9090/tcp": {}}}, "server")
        build.check_contract({"User": "1983:1983", "Entrypoint": [build.BINARY]}, "worker")
        for config in (
            {"User": "1983:1983", "Entrypoint": [build.BINARY], "ExposedPorts": {"8080/tcp": {}, "9090/tcp": {}}},
            {"User": "65532:65532", "Entrypoint": [build.BINARY], "ExposedPorts": {"8080/tcp": {}, "9090/tcp": {}}},
            {"User": "65532:65532", "Entrypoint": [build.BINARY, "all"], "ExposedPorts": {"8080/tcp": {}}},
            {"User": "0", "Entrypoint": [build.BINARY, "all"], "ExposedPorts": {"8080/tcp": {}, "9090/tcp": {}}},
        ):
            with self.assertRaises(ValueError, msg=config):
                build.check_contract(config, "server")

    def test_the_configuration_keeps_the_listener_defaults(self):
        config = build.server_config("postgres://u:p@db:5432/infraweave")
        self.assertEqual(config["database"], {"url": "postgres://u:p@db:5432/infraweave"})
        self.assertNotIn("listen", config["server"])
        self.assertNotIn("admin_listen", config["server"])
        self.assertEqual(config["server"]["public_url"], build.SMOKE_URL)
        self.assertEqual(set(config["vendors"]), {"terraform_index", "opentofu_index", "terragrunt_index"})
        self.assertTrue(all(url.startswith("http://127.0.0.1:1/") for url in config["vendors"].values()))

    def test_postgres_is_the_acceptance_pin(self):
        self.assertRegex(build.postgres_image(), r"^postgres:[^@]+@sha256:[0-9a-f]{64}$")
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "images.go"
            source.write_text('\tImagePostgres = "postgres:18"\n')
            with self.assertRaises(ValueError):
                build.postgres_image(source)

    def test_host_port(self):
        self.assertEqual(build.host_port("127.0.0.1:49153\n"), 49153)
        self.assertEqual(build.host_port("0.0.0.0:1\n[::]:2\n"), 1)
        with self.assertRaises(ValueError):
            build.host_port("")

    def test_describes_the_pinned_image(self):
        with tempfile.TemporaryDirectory() as directory:
            context = Path(directory)
            (context / "SHA256SUMS").write_text(f"{'b' * 64}  infraweave-linux-amd64\n{'c' * 64}  infraweave-linux-arm64\n")
            image = "ghcr.io/weaveplatform/infraweave@" + DIGEST
            platforms = {"linux/amd64": DIGEST, "linux/arm64": DIGEST}
            value = build.describe_server("0.1.0", "abc", image, platforms, context)
            self.assertEqual(value["kind"], "server-image")
            self.assertEqual(value["image"], image)
            self.assertEqual(value["entrypoint"], [build.BINARY, "all"])
            self.assertEqual(value["ports"], {"api": 8080, "admin": 9090})
            self.assertEqual(value["binaries"]["linux/arm64"], "sha256:" + "c" * 64)
            self.assertEqual(value["signature"]["identity"], build.SERVER_SIGNER)
            with self.assertRaises(ValueError):
                build.describe_server("0.1.0", "abc", "ghcr.io/weaveplatform/infraweave:0.1.0", platforms, context)


class Main(unittest.TestCase):
    def test_a_refusal_exits_nonzero(self):
        with tempfile.TemporaryDirectory() as directory:
            self.assertEqual(build.main(["chart", "--version", "0.1.0", "--image", "x:latest", "--out", directory]), 1)


if __name__ == "__main__":
    unittest.main()
