"""Print bounded facts from the existing acceptance trace while Go buffers output."""

import argparse
import json
from pathlib import Path
import time


FIELDS = ("at", "event", "scenario", "uri", "step", "failed", "seconds", "exitCode")


def latest_record(path):
    try:
        with path.open("rb") as source:
            source.seek(0, 2)
            source.seek(max(0, source.tell() - 16384))
            lines = source.read().splitlines()
    except OSError:
        return None
    for line in reversed(lines):
        try:
            row = json.loads(line)
        except (ValueError, UnicodeDecodeError):
            continue
        if not isinstance(row, dict):
            continue
        return {
            key: value[:240] if isinstance(value, str) else value
            for key in FIELDS
            if isinstance(value := row.get(key), (str, int, float, bool))
        }
    return None


def snapshot(directory):
    root = Path(directory)
    traces = []
    try:
        paths = sorted(root.rglob("progress.jsonl"))[:8]
    except OSError:
        paths = []
    for path in paths:
        row = latest_record(path)
        if row is not None:
            traces.append({"file": str(path.relative_to(root))[:240], **row})
    return {"traces": traces, "phase": "observing" if traces else "waiting-for-progress"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory")
    parser.add_argument("--once", action="store_true")
    args = parser.parse_args()
    while True:
        print("acceptance progress " + json.dumps(snapshot(args.directory)), flush=True)
        if args.once:
            return
        time.sleep(30)


if __name__ == "__main__":
    main()
