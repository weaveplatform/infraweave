#!/usr/bin/env bash
# Pushes the lab history to the empty repository at SEED_URL, as SEED_USER
# with SEED_TOKEN when they are set. Every author, committer and tagger
# date is fixed and no configuration is read, so each push writes the same
# commits, whose hashes internal/vcs/vcslab names and its test checks:
#
#   base    README.md, main.tf
#   tagged  main.tf                    tagged v1, annotated
#   head    main.tf, modules/net/main.tf    main's head
#   dev     dev.tf                     dev's head, on head
#
# main goes first and alone, so a host that takes an empty repository's
# first branch as its default takes main.
set -euo pipefail

: "${SEED_URL:?SEED_URL names the repository to seed}"

export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null
export GIT_AUTHOR_NAME='infraweave lab' GIT_AUTHOR_EMAIL='lab@infraweave.invalid'
export GIT_COMMITTER_NAME="$GIT_AUTHOR_NAME" GIT_COMMITTER_EMAIL="$GIT_AUTHOR_EMAIL"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cd "$work"

# The helper reads the token from the environment when git asks, so the
# token is in no argument or file; the quotes keep the shell from
# expanding it here.
git init -q --object-format=sha1 -b main
git config commit.gpgsign false
git config tag.gpgsign false
git config core.autocrlf false
if [ -n "${SEED_TOKEN:-}" ]; then
  # shellcheck disable=SC2016
  git config credential.helper \
    '!f() { echo "username=${SEED_USER:-git}"; echo "password=${SEED_TOKEN}"; }; f'
fi

commit() {
  export GIT_AUTHOR_DATE="$1" GIT_COMMITTER_DATE="$1"
  git add -A
  git commit -q -m "$2"
}

printf '# lab\n' > README.md
printf '# base\n' > main.tf
commit '2026-01-01T00:00:00Z' base

printf '# tagged\n' > main.tf
commit '2026-01-01T01:00:00Z' tagged
git tag -a v1 -m v1

mkdir -p modules/net
printf '# head\n' > main.tf
printf '# net\n' > modules/net/main.tf
commit '2026-01-01T02:00:00Z' head

git checkout -q -b dev
printf '# dev\n' > dev.tf
commit '2026-01-01T03:00:00Z' dev

git push -q "$SEED_URL" refs/heads/main:refs/heads/main
git push -q "$SEED_URL" refs/heads/dev:refs/heads/dev refs/tags/v1:refs/tags/v1
