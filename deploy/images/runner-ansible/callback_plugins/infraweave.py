"""Aggregate task metadata and recap, deliberately excluding module results."""

import json
import os

from ansible.plugins.callback import CallbackBase


DOCUMENTATION = """
name: infraweave
type: aggregate
short_description: Record task metadata for infraweave
version_added: '1.0'
description: Writes metadata and the recap to INFRAWEAVE_ANSIBLE_RECORD.
requirements:
  - Enable with ANSIBLE_CALLBACKS_ENABLED=infraweave.
"""


class CallbackModule(CallbackBase):
    CALLBACK_VERSION = 2.0
    CALLBACK_TYPE = "aggregate"
    CALLBACK_NAME = "infraweave"
    CALLBACK_NEEDS_ENABLED = True

    def __init__(self):
        super().__init__()
        self.playbook = ""
        self.records = []

    def v2_playbook_on_start(self, playbook):
        self.playbook = playbook._file_name

    def _record(self, result, status):
        task = result._task
        role = task._role.get_name() if task._role else ""
        # A rescued failure is known only after the strategy has completed.
        # Keep metadata until the recap; never retain result payloads.
        self.records.append({
            "host": result._host.get_name(),
            "playbook": self.playbook,
            "role": role,
            "task": task.get_name(),
            "action": task.action,
            "status": status,
            "checkMode": bool(task.check_mode),
        })

    def v2_runner_on_ok(self, result):
        self._record(result, "changed" if result._result.get("changed", False) else "ok")

    def v2_runner_on_failed(self, result, ignore_errors=False):
        self._record(result, "ignored" if ignore_errors else "failed")

    def v2_runner_on_skipped(self, result):
        self._record(result, "skipped")

    def v2_runner_on_unreachable(self, result):
        self._record(result, "unreachable")

    def v2_playbook_on_stats(self, stats):
        path = os.environ.get("INFRAWEAVE_ANSIBLE_RECORD")
        if not path:
            return
        recap = {key: dict(getattr(stats, key)) for key in (
            "processed", "ok", "changed", "dark", "failures", "skipped", "rescued", "ignored"
        )}
        for host, count in recap["rescued"].items():
            failures = [r for r in self.records if r["host"] == host and r["status"] == "failed"]
            for record in failures[:count]:
                record["status"] = "rescued"
        with open(path, "a", encoding="utf-8") as stream:
            for record in self.records:
                stream.write(json.dumps(record, sort_keys=True) + "\n")
            stream.write(json.dumps({"stats": recap}, sort_keys=True) + "\n")
