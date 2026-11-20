#!/usr/bin/env bash
# Only called on trusted main with the org App token, so PRs run required CI.
set -euo pipefail
branch=$1
title=$2
body=$3
shift 3
if git diff --quiet -- "$@" && [ -z "$(git ls-files --others --exclude-standard -- "$@")" ]; then
  echo 'No change; no duplicate PR.'
  exit 0
fi
expected=$(git ls-remote origin "refs/heads/$branch" | awk '{print $1}')
git add -- "$@"
if [ -n "$expected" ]; then
  git fetch --no-tags origin "refs/heads/$branch"
  if git diff --cached --quiet FETCH_HEAD -- "$@"; then
    echo 'The pending PR already has these exact updates; no new commit or CI restart.'
    exit 0
  fi
fi
git switch -C "$branch"
git -c user.name='weaveplatform-release-please[bot]' \
  -c user.email='weaveplatform-release-please[bot]@users.noreply.github.com' commit -m "$title"
git push origin "HEAD:refs/heads/$branch" "--force-with-lease=refs/heads/$branch:$expected"
if [ -z "$(gh pr list --head "$branch" --state open --json number --jq '.[0].number // empty')" ]; then
  gh pr create --head "$branch" --base main --title "$title" --body-file "$body"
fi
