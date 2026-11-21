#!/bin/sh
set -eu
sh /opt/image-tests/contract.sh
case "$RUNNER_KIND" in
 terraform|opentofu) sh /opt/image-tests/tests/mounted-tools.sh ;;
 pulumi) sh /opt/image-tests/tests/pulumi.sh ;;
 ansible) sh /opt/image-tests/tests/ansible.sh ;;
 *) echo "unknown runner kind: $RUNNER_KIND" >&2; exit 1 ;;
esac
