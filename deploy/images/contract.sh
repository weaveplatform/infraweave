#!/bin/sh
# The runner image contract, run in a container of a runner image as the
# image's own user, from its own working directory: CI's images job runs it
# on each image and platform, and the acceptance suite's TestAccRunnerImages
# on each image it builds. It checks UID and GID 1983, a ps that lists the
# container's processes, a workspace that user writes, the tools a phase's
# shell needs. Baked-tool contracts are checked separately so retained
# images and custom images remain usable with per-run read-only mounts.
# ref: decision 0008 § "Runner images"
set -eu

fail() {
	echo "runner image contract: $*" >&2
	exit 1
}

[ "$(id -u):$(id -g)" = 1983:1983 ] || fail "runs as $(id -u):$(id -g), want 1983:1983"
ps -o pid,user,args >/dev/null || fail "ps does not list the processes"
[ "$(pwd)" = /mnt/workspace ] || fail "works in $(pwd), want /mnt/workspace"
probe=/mnt/workspace/.contract
echo probe >"$probe" || fail "cannot write the workspace"
rm "$probe"
for tool in bash curl git jq ssh; do
	command -v "$tool" >/dev/null || fail "lacks $tool"
done
[ -s /etc/ssl/certs/ca-certificates.crt ] || fail "lacks the CA certificates"
[ -e /usr/share/zoneinfo/UTC ] || fail "lacks the time zone database"
echo "runner image contract: met by $(. /etc/os-release && echo "$PRETTY_NAME") on $(uname -m)"
