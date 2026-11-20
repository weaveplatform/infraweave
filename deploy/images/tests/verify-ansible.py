import json
import sys

records = [json.loads(line) for line in open(sys.argv[1], encoding="utf-8")]
phase = sys.argv[2]
tasks, recap = records[:-1], records[-1]["stats"]
keys = {"host", "playbook", "role", "task", "action", "status", "checkMode"}
assert tasks and all(set(task) == keys for task in tasks), records
assert all(task["checkMode"] == (phase == "check" and task["action"] != "ansible.builtin.command")
           for task in tasks), records
statuses = {task["status"] for task in tasks}
assert {"ok", "skipped", "ignored", "rescued"} <= statuses, records
assert ("changed" in statuses) == (phase != "fail"), records
assert ("failed" in statuses) == (phase == "fail"), records
assert set(recap) == {"processed", "ok", "changed", "dark", "failures", "skipped", "rescued", "ignored"}, recap
assert recap["rescued"]["localhost"] == 1 and recap["ignored"]["localhost"] == 1, recap
assert recap["failures"].get("localhost", 0) == (1 if phase == "fail" else 0), recap
print("Ansible metadata and recap verified:", phase)
