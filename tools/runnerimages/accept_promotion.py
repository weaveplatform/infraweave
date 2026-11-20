#!/usr/bin/env python3
"""Exercise real GHCR promotion with isolated preview tags before release tags."""

import argparse
import copy
import importlib.util
import json
import os
from pathlib import Path
import re
import tempfile
from types import SimpleNamespace

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("imagectl_promotion_acceptance", ROOT / "deploy/images/imagectl.py")
ctl = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ctl)


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument("lock")
    args = parser.parse_args()
    ref = os.environ.get("GITHUB_REF")
    if os.environ.get("GITHUB_REPOSITORY") != "weaveplatform/infraweave" or ref != "refs/heads/main":
        raise SystemExit("promotion acceptance is restricted to the trusted main publisher")
    run_id, attempt = os.environ["GITHUB_RUN_ID"], os.environ["GITHUB_RUN_ATTEMPT"]
    if not re.fullmatch(r"[0-9]+", run_id) or not re.fullmatch(r"[0-9]+", attempt):
        raise SystemExit("invalid workflow run identity")
    prefix = f"preview-promotion-{run_id}-{attempt}-"
    lock = ctl.read(args.lock)
    if set(lock["images"]) != set(ctl.KINDS):
        raise SystemExit("incomplete candidate catalogue")
    for kind, entry in lock["images"].items():
        if not re.fullmatch(re.escape(ctl.REGISTRY + kind) + r"@sha256:[0-9a-f]{64}", entry["reference"]):
            raise SystemExit("unexpected candidate package")
    original_tag = ctl.tag
    ctl.tag = lambda kind, version=None: prefix + original_tag(kind, version)
    for entry in list(lock["images"].values()) + lock.get("releases", []):
        entry["tag"] = prefix + entry["tag"]

    def refs(document):
        entries = list(document["images"].items()) + [(e["kind"], e) for e in document.get("releases", [])]
        return {ctl.REGISTRY + kind + ":" + entry["tag"]: entry["reference"].split("@", 1)[1]
                for kind, entry in entries}

    expected = refs(lock)

    def snapshot():
        return {name: ctl.registry_tag(name) for name in expected}

    original_bytes = Path(args.lock).read_bytes()
    consumer_path = ctl.HERE / "images.lock.json"
    consumer_bytes = consumer_path.read_bytes()
    with tempfile.TemporaryDirectory(prefix="infraweave-ghcr-promotion-") as directory:
        proposal = Path(directory) / "proposal.json"
        ctl.write(proposal, lock)
        ctl.promote(SimpleNamespace(lock=proposal))
        if snapshot() != expected:
            raise AssertionError("promotion did not copy the exact tested indexes")
        ctl.promote(SimpleNamespace(lock=proposal))
        if snapshot() != expected:
            raise AssertionError("identical promotion changed an immutable tag")
        print("Real GHCR promotion and duplicate promotion preserved exact digests", flush=True)

        def refused(document, reason):
            ctl.write(proposal, document)
            try:
                ctl.promote(SimpleNamespace(lock=proposal))
            except ValueError as error:
                if reason not in str(error):
                    raise
                print("Expected promotion refusal:", error, flush=True)
            else:
                raise AssertionError("invalid publication was promoted")
            if snapshot() != expected or Path(args.lock).read_bytes() != original_bytes or consumer_path.read_bytes() != consumer_bytes:
                raise AssertionError("failed publication changed preview tags or consumer pins")

        incomplete = copy.deepcopy(lock)
        entry = incomplete["images"]["terraform"]
        entry["reference"] = ctl.REGISTRY + "terraform@" + entry["platforms"]["linux/amd64"]
        refused(incomplete, "both platforms")

        terraform = lock["images"]["terraform"]
        conflict_tag = ctl.REGISTRY + f"terraform:preview-conflict-{run_id}-{attempt}"
        ctl.run("docker", "buildx", "imagetools", "create", "--prefer-index=false", "--annotation",
                "index:io.infraweave.promotion-acceptance=conflict", "--tag", conflict_tag, terraform["reference"])
        digest = ctl.registry_tag(conflict_tag)
        if digest == terraform["reference"].split("@", 1)[1]:
            raise AssertionError("conflict index must have a distinct digest")
        conflict_ref = ctl.REGISTRY + "terraform@" + digest
        identity = "https://github.com/weaveplatform/infraweave/.github/workflows/runner-images.yml@" + ref
        ctl.run("cosign", "sign", "--yes", conflict_ref)
        ctl.run("cosign", "verify", "--certificate-identity", identity,
                "--certificate-oidc-issuer", "https://token.actions.githubusercontent.com", conflict_ref)
        conflict = copy.deepcopy(lock)
        conflict["images"]["terraform"]["reference"] = conflict_ref
        refused(conflict, "immutable tag already exists at another digest")
    print("Real GHCR incomplete-evidence and immutable-tag refusals left tags and pins unchanged", flush=True)


if __name__ == "__main__":
    main()
