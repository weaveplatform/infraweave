#!/bin/sh
set -eu
work=$(mktemp -d)
trap 'rm -rf "$work" /tmp/infraweave-ansible-contract.txt' EXIT
export ANSIBLE_CALLBACKS_ENABLED=infraweave
ansible-playbook --version
expected=${RUNNER_TOOL_VERSION:-$(jq -r '.tools.ansible.version' /opt/image-tests/versions.json)}
[ "$(python3 -c 'from ansible.release import __version__; print(__version__)')" = "$expected" ]
for phase in check apply fail; do
 export INFRAWEAVE_ANSIBLE_RECORD="$work/$phase.jsonl"
 args=
 case "$phase" in check) args=--check ;; fail) args='-e terminal_failure=true' ;; esac
 code=0
 # args are the fixed flags above, never user-supplied shell text.
 ansible-playbook /opt/image-tests/tests/ansible.yml $args > "$work/$phase.log" 2>&1 || code=$?
 cat "$work/$phase.log"
 if [ "$phase" = fail ]; then [ "$code" -eq 2 ]; else [ "$code" -eq 0 ]; fi
 if [ "$phase" = check ]; then
  [ ! -e /tmp/infraweave-ansible-contract.txt ]
 else
  [ "$(cat /tmp/infraweave-ansible-contract.txt)" = sentinel-private-module-value-84931 ]
 fi
 python3 /opt/image-tests/tests/verify-ansible.py "$INFRAWEAVE_ANSIBLE_RECORD" "$phase"
 if grep -q 'sentinel-private-' "$work/$phase.log" "$INFRAWEAVE_ANSIBLE_RECORD"; then
  echo 'Ansible leaked no_log content' >&2; exit 1
 fi
done
echo 'Ansible compatibility: check, apply, failure, no_log, rescue and recap passed'
