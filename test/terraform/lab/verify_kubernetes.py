#!/usr/bin/env python3
"""Execute owned local-cluster failure and two-invocation cleanup controls."""

import argparse
import json
import signal
from pathlib import Path
import subprocess
import uuid

import labctl as lab


def verify(helper, manifest, succeeds=True):
    result = subprocess.run([helper, "verify-lab", "--manifest", str(manifest)], capture_output=True, text=True, timeout=30)
    if (result.returncode == 0) != succeeds:
        raise RuntimeError("The real lab attachment control had an unexpected result")


def run(args):
    directory = Path(args.directory).resolve()
    directory.mkdir(mode=0o700, parents=True, exist_ok=False)
    invocations = []
    result = {"passed": False, "revision": lab.output("git", "-C", str(lab.REPO), "rev-parse", "HEAD"),
              "dirty": bool(lab.output("git", "-C", str(lab.REPO), "status", "--porcelain")), "controls": []}
    try:
        for name in ("a", "b"):
            path = directory / name
            owner = "iw-lab-" + uuid.uuid4().hex
            invocations.append((path, owner))
            lab.up(path, "kubernetes", args.terraform, args.helper, owner)
        a, b = (path for path, _owner in invocations)
        am, bm = (lab.read_json(path / "acceptance.json") for path in (a, b))
        verify(args.helper, a / "acceptance.json")
        verify(args.helper, b / "acceptance.json")
        result["controls"].append("two independently owned clusters attached")

        foreign = dict(am, owner=bm["owner"])
        lab.write_json(a / "foreign-manifest.json", foreign)
        verify(args.helper, a / "foreign-manifest.json", succeeds=False)
        result["controls"].append("foreign manifest refused without replacement")

        journal = lab.read_json(a / "deployment.json")
        try:
            lab.write_json(a / "deployment.json", dict(journal, owner=bm["owner"]))
            try:
                lab.down(a)
            except RuntimeError as error:
                if "refusing destroy" not in str(error):
                    raise
            else:
                raise RuntimeError("A foreign cleanup journal was accepted")
        finally:
            lab.write_json(a / "deployment.json", journal)
        verify(args.helper, a / "acceptance.json")
        verify(args.helper, b / "acceptance.json")
        result["controls"].append("foreign cleanup journal refused before destroy")

        cluster = am["kubernetes"]["container"]
        subprocess.run(["docker", "pause", cluster], check=True, capture_output=True, timeout=30)
        try:
            verify(args.helper, a / "acceptance.json", succeeds=False)
            result["controls"].append("paused real cluster fails attachment")
        finally:
            subprocess.run(["docker", "unpause", cluster], check=True, capture_output=True, timeout=30)
            lab.kubernetes_ready(a, am)
        verify(args.helper, a / "acceptance.json")
        result["controls"].append("same owned cluster recovers without replacement")

        first = lab.down(a)
        verify(args.helper, b / "acceptance.json")
        second = lab.down(a)
        verify(args.helper, b / "acceptance.json")
        if not first["passed"] or not second["passed"]:
            raise RuntimeError("Repeated cleanup did not prove absence")
        result["controls"].append("destroy and repeated cleanup preserve the second owner")
        result["passed"] = True
    finally:
        cleanups = []
        cleanup_failed = False
        for path, owner in reversed(invocations):
            if (path / "deployment.json").exists() and lab.read_json(path / "deployment.json")["owner"] == owner:
                try:
                    cleanups.append(lab.down(path))
                except BaseException:
                    result["passed"] = False
                    cleanup_failed = True
        result["cleanup"] = cleanups
        lab.write_json(directory / "controls.json", result)
        if cleanup_failed:
            raise RuntimeError("Owned control cleanup failed; private journals retained")
    return result


if __name__ == "__main__":
    def terminated(signum, _frame):
        raise SystemExit(128 + signum)
    signal.signal(signal.SIGTERM, terminated)
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument("--directory", required=True)
    parser.add_argument("--terraform", default="terraform")
    parser.add_argument("--helper", required=True)
    print(json.dumps(run(parser.parse_args())))
