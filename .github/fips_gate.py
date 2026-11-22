"""Retain bounded FIPS execution facts and require complete successful shards."""

import argparse
import json
import os
from pathlib import Path
import subprocess
import time


def identity():
    def git(*args):
        return subprocess.check_output(["git", *args], text=True, timeout=10).strip()
    return {"head": os.environ["PR_HEAD"], "base": os.environ.get("PR_BASE", ""),
            "checkout": git("rev-parse", "HEAD"), "tree": git("rev-parse", "HEAD^{tree}"),
            "run": os.environ["GITHUB_RUN_ID"], "attempt": int(os.environ["GITHUB_RUN_ATTEMPT"])}


def catalogue(matrix):
    jobs = matrix["include"]
    names = [job["shard"] for job in jobs]
    if not jobs or len(names) != len(set(names)) or names.count("native") != 1 or "unit" in names:
        raise ValueError("Incomplete or duplicate FIPS matrix")
    for job in jobs:
        if job["partition"] != ("" if job["shard"] == "native" else job["shard"]) or not 10 < job["timeout"] <= 30:
            raise ValueError("Invalid FIPS shard selector or budget")
    return {"unit", *names}


def scenarios(path):
    report = json.loads(Path(path).read_text())
    cases = [case for feature in report for case in feature.get("elements", []) if case.get("type") == "scenario"]
    steps = [step for case in cases for step in case.get("steps", [])]
    if not cases or not steps or any(not case.get("steps") for case in cases) or any(
            step.get("result", {}).get("status") != "passed" for step in steps):
        raise ValueError("FIPS partition did not pass nonempty scenarios and every step")
    return {"scenarios": len(cases), "steps": len(steps)}


def record(shard, status, started, root, source):
    result = {"schema": 1, "shard": shard, "status": status, **source,
              "seconds": max(0, int(time.time()) - int(Path(started).read_text()))}
    if status == "success" and shard not in ("unit", "native"):
        result.update(scenarios(Path(root) / "cover/accept/cucumber.json"))
    if status == "success" and shard == "native":
        mode = (Path(root) / "cover/native/runtime-crypto-mode.txt").read_text().strip()
        if not mode.startswith("FIPS 140-3 mode only, Go Cryptographic Module v"):
            raise ValueError("Native suite did not verify the actual application crypto mode")
        result["cryptoMode"] = mode
    return result


def validate(matrix, results, children, source):
    expected = catalogue(matrix)
    if children != {"fips-unit": "success", "fips-tests": "success"}:
        raise ValueError("A required FIPS job failed, was cancelled, skipped or missing")
    selected = {}
    seen = set()
    for result in results:
        shard, attempt = result["shard"], result["attempt"]
        if shard not in expected or type(attempt) is not int or not 1 <= attempt <= source["attempt"]:
            raise ValueError("Unexpected FIPS shard or run attempt")
        if (shard, attempt) in seen:
            raise ValueError("Duplicate FIPS shard receipt for one attempt")
        seen.add((shard, attempt))
        for field in ("head", "base", "checkout", "tree", "run"):
            if result[field] != source[field]:
                raise ValueError("FIPS receipts do not describe the same exact tested source/run")
        if result.get("schema") != 1 or type(result.get("seconds")) is not int or result["seconds"] < 0:
            raise ValueError("Invalid FIPS receipt")
        if shard not in selected or attempt > selected[shard]["attempt"]:
            selected[shard] = result
    if set(selected) != expected:
        raise ValueError("Missing FIPS shard receipts")
    for shard, result in selected.items():
        if result["status"] != "success":
            raise ValueError("Latest FIPS shard execution did not succeed")
        if shard == "native":
            if not result.get("cryptoMode", "").startswith("FIPS 140-3 mode only, Go Cryptographic Module v"):
                raise ValueError("Missing native application crypto proof")
        elif shard != "unit" and (result.get("scenarios", 0) <= 0 or result.get("steps", 0) <= 0):
            raise ValueError("Missing scenario execution proof")
    return {"passed": True, "source": source, "shards": [selected[name] for name in sorted(expected)]}


def main():
    parser = argparse.ArgumentParser(__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    save = sub.add_parser("record")
    save.add_argument("--shard", required=True)
    save.add_argument("--status", required=True)
    save.add_argument("--started", required=True)
    save.add_argument("--directory", required=True)
    check = sub.add_parser("check")
    check.add_argument("--directory", required=True)
    check.add_argument("--matrix", required=True)
    args = parser.parse_args()
    directory = Path(args.directory)
    if args.command == "record":
        directory.mkdir(parents=True, exist_ok=True)
        source = identity()
        # Retain normal validation provenance without exposing test inputs.
        (directory / "revision.json").write_text(json.dumps(source, indent=2) + "\n")
        value = record(args.shard, args.status, args.started, ".", source)
        (directory / "result.json").write_text(json.dumps(value, indent=2) + "\n")
    else:
        children = {name: job["result"] for name, job in json.loads(os.environ["FIPS_CHILDREN"]).items()}
        value = validate(json.loads(args.matrix), [json.loads(path.read_text()) for path in directory.rglob("result.json")],
                         children, identity())
        (directory / "summary.json").write_text(json.dumps(value, indent=2) + "\n")
        print("Certified FIPS passed all " + str(len(value["shards"])) + " required shards")
        for shard in value["shards"]:
            print(shard["shard"] + ": " + str(shard["seconds"]) + "s; attempt " + str(shard["attempt"]))


if __name__ == "__main__":
    main()
