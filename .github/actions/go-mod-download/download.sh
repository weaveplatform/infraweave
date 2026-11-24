#!/usr/bin/env bash
# download.sh [package@version ...]
#
# Downloads every module the main module builds and tests with, then the
# module graph of each package@version the build runs with `go run`, which
# resolves outside go.mod. A dropped connection to the module proxy fails
# one attempt, not the job: each download is tried three times, 10 s and
# then 30 s apart. A download that fails every attempt exits non-zero,
# naming the command that failed.
set -euo pipefail

export GOWORK=off

delays=(10 30)
attempts=$((${#delays[@]} + 1))

# GitHub Actions turns ::warning:: and ::error:: lines into annotations on
# the run; elsewhere they read as plain prefixes.
retry() {
	local attempt=1
	until "$@"; do
		if ((attempt == attempts)); then
			echo "::error::'$*' failed ${attempts} times; the Go module proxy is unreachable or a module is missing" >&2
			return 1
		fi
		local delay=${delays[attempt - 1]}
		echo "::warning::'$*' failed (attempt ${attempt} of ${attempts}); retrying in ${delay}s" >&2
		sleep "$delay"
		attempt=$((attempt + 1))
	done
}

retry go mod download

# go install -n resolves and downloads the package's whole module graph,
# and its latest version for the deprecation check, as go run would, then
# lists the build commands rather than running them. The listing goes to
# stderr with any error, so it is shown only when the command fails.
resolve() {
	local out
	out=$(go install -n "$1" 2>&1) || {
		printf '%s\n' "$out" >&2
		return 1
	}
}

for tool in "$@"; do
	retry resolve "$tool"
done
