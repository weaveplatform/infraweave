#!/usr/bin/env bash
# Deletes the lab repositories a provider holds that are more than a day
# old, which a run whose destroy never ran leaves behind. A lab
# repository's name starts infraweave-lab-<Unix time it was made>-, so its
# age needs nothing a provider might not record. It reads the same
# variables as the provider's Terraform root, and prints the names it
# deletes, never a credential.
#
#   sweep.sh github           TF_VAR_token, TF_VAR_organization
#   sweep.sh bitbucket-cloud  BITBUCKET_USERNAME, BITBUCKET_PASSWORD, TF_VAR_workspace
#   sweep.sh azure-devops     AZDO_ORG_SERVICE_URL, AZDO_PERSONAL_ACCESS_TOKEN, TF_VAR_project
#
# GitLab and Bitbucket Data Center need none: their containers live for
# one run.
set -euo pipefail

cutoff=$(($(date +%s) - 86400))

# stale reads names on stdin and prints those of lab repositories made
# before the cutoff.
stale() {
  while read -r name; do
    if [[ $name =~ ^infraweave-lab-([0-9]+)- ]] && ((BASH_REMATCH[1] < cutoff)); then
      echo "$name"
    fi
  done
}

# The credentials go to curl through its config on stdin, never as an
# argument.
api() {
  local method=$1 url=$2 auth=$3
  printf '%s\n' "$auth" | curl -fsS -X "$method" --config - "$url"
}

case "${1:-}" in
  github)
    : "${TF_VAR_token:?}" "${TF_VAR_organization:?}"
    auth="header = \"Authorization: Bearer ${TF_VAR_token}\""
    base="https://api.github.com"
    api GET "$base/orgs/${TF_VAR_organization}/repos?type=all&per_page=100&sort=created&direction=asc" "$auth" |
      jq -r '.[].name' | stale | while read -r name; do
        echo "deleting $name"
        api DELETE "$base/repos/${TF_VAR_organization}/${name}" "$auth"
      done
    ;;
  bitbucket-cloud)
    : "${BITBUCKET_USERNAME:?}" "${BITBUCKET_PASSWORD:?}" "${TF_VAR_workspace:?}"
    auth="user = \"${BITBUCKET_USERNAME}:${BITBUCKET_PASSWORD}\""
    base="https://api.bitbucket.org/2.0/repositories/${TF_VAR_workspace}"
    api GET "$base?pagelen=100&q=name~%22infraweave-lab-%22" "$auth" |
      jq -r '.values[].slug' | stale | while read -r name; do
        echo "deleting $name"
        api DELETE "$base/${name}" "$auth"
      done
    ;;
  azure-devops)
    : "${AZDO_ORG_SERVICE_URL:?}" "${AZDO_PERSONAL_ACCESS_TOKEN:?}" "${TF_VAR_project:?}"
    auth="user = \"pat:${AZDO_PERSONAL_ACCESS_TOKEN}\""
    base="${AZDO_ORG_SERVICE_URL%/}/${TF_VAR_project}/_apis/git"
    api GET "$base/repositories?api-version=7.1" "$auth" |
      jq -r '.value[] | "\(.name) \(.id)"' | while read -r name id; do
        if [ -n "$(stale <<<"$name")" ]; then
          echo "deleting $name"
          api DELETE "$base/repositories/${id}?api-version=7.1" "$auth"
          # A deleted repository waits in the recycle bin until purged.
          api DELETE "$base/recycleBin/repositories/${id}?api-version=7.1" "$auth"
        fi
      done
    ;;
  *)
    echo "usage: sweep.sh github|bitbucket-cloud|azure-devops" >&2
    exit 2
    ;;
esac
