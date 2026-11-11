# VCS test labs

The repositories that decision 0016's scheduled `vcs-lab` job
(`.github/workflows/vcs-lab.yml`) runs against, as one Terraform root per
VCS provider. Each run applies its provider's root, runs that provider's
`TestAcc<Provider>Lab` and its `@vcs-lab` scenario in
`vcs_sources.feature`, and destroys the root, pass or fail. Nothing here
is part of infraweave or its reference deployments.

| Root | Creates |
|---|---|
| `github` | two private repositories in the lab organization, which the lab App, installed on all its repositories, reaches |
| `gitlab` | on the container the job starts: a group, two private projects, and a user who is the group's Maintainer, whose personal access token with `api`, expiring in two days, is the integration's (GitLab refuses a group access token's bot a project access token) |
| `bitbucketcloud` | two private repositories in the lab workspace's project |
| `azuredevops` | two repositories in the lab project |
| `bitbucketdc` | on the container the job starts: a project, two repositories, and the integration's project access token with project admin, expiring in a day |

Every repository is named `infraweave-lab-<Unix time>-<run ID>`, from the
`suffix` variable, and the second ends `-other`. `seed.sh` pushes the
same history to each: a base commit, an annotated tag `v1`, `main`'s
head and a `dev` branch. Its dates are fixed, so the commit hashes are
the constants in `internal/vcs/vcslab`, whose unit test runs the script
and checks them.

The integration reaches both repositories. A run's token must reach only
the first:
- **GitHub, GitLab, Bitbucket Data Center:** the token is minted for the
  run and narrowed to the first repository.
- **Bitbucket Cloud, Azure DevOps:** these mint no narrower token, so a
  run borrows the integration's, which reaches both.

Before applying, the job runs `sweep.sh`. It deletes any lab repository
more than a day old that a run whose destroy never ran left behind,
reading the age from the name.

## The lab accounts

The maintainer creates each lab once. Each lab holds nothing but these
repositories.

- **GitHub:**
  - an organization;
  - an owner's classic token with `repo` and `delete_repo`, or a
    fine-grained token on the organization with repository
    Administration write;
  - a GitHub App owned by the organization, with read access to
    contents and metadata, no webhook, installed on all the
    organization's repositories; the lab holds nothing else for it to
    reach.
- **GitLab:** nothing. GitLab.com offers group and project access tokens
  only on paid tiers, so the job starts GitLab Community Edition
  (`start-gitlab.sh`), which offers them on every tier, and makes its
  administrator's token and HTTPS certificate for each run.
- **Bitbucket Cloud:** a workspace with a project, and an Atlassian
  account's API token with repository admin and delete scopes on it.
- **Azure DevOps:** an organization with a project, and a personal access
  token with Code (full).
- **Bitbucket Data Center:** a licence; a developer licence will do. The
  job makes the system administrator's password and the HTTPS certificate
  for each run.

## Secrets

They are the `vcs-lab` environment's secrets, set with
`gh secret set --env vcs-lab`. Give the environment a deployment branch
rule that admits only `main`. A provider with any of its secrets unset
is skipped: its job passes having run nothing, and the run's summary
names the secrets it lacks.

| Provider | Secret | What it holds |
|---|---|---|
| GitHub | `VCS_GITHUB_ORGANIZATION` | the organization's login |
| | `VCS_GITHUB_ADMIN_TOKEN` | the owner's token |
| | `VCS_GITHUB_APP_ID`, `VCS_GITHUB_APP_PRIVATE_KEY` | the App's ID and PEM private key |
| Bitbucket Cloud | `VCS_BITBUCKET_CLOUD_WORKSPACE`, `VCS_BITBUCKET_CLOUD_PROJECT_KEY` | the workspace and its project |
| | `VCS_BITBUCKET_CLOUD_EMAIL`, `VCS_BITBUCKET_CLOUD_API_TOKEN` | the account's e-mail and API token |
| Azure DevOps | `VCS_AZURE_DEVOPS_ORGANIZATION_URL`, `VCS_AZURE_DEVOPS_PROJECT` | `https://dev.azure.com/<organization>` and the project |
| | `VCS_AZURE_DEVOPS_PAT` | the personal access token |
| Bitbucket Data Center | `VCS_BITBUCKET_DC_LICENSE` | the licence |

## Running a lab by hand

State is local and ignored, and the provider lock files are committed.
Each root reads its provider's credentials as the comment in its
`versions.tf` says; the GitHub root reads its token from `TF_VAR_token`.
`seed.sh` reads `SEED_USER` and `SEED_TOKEN`.

```sh
image=$(sed -nE 's/^[[:space:]]*ImageGitLabCE = "([^"]+)"/\1/p' test/acceptance/images.go)
test/terraform/vcs/start-gitlab.sh "$image" /tmp/gitlab-lab
set -a; . /tmp/gitlab-lab/env; set +a
cd test/terraform/vcs/gitlab
terraform init
terraform apply -var suffix="$(date +%s)-local"
```

Then set the tests' variables from `terraform output`. Each output's
description names its variable.

```sh
INFRAWEAVE_TEST_VCS_HOST=… INFRAWEAVE_TEST_VCS_TOKEN=… … \
  go test -run '^TestAccGitLabLab$' ./internal/vcs/internal/adapters/gitlab
INFRAWEAVE_ACCEPT_TAGS=@lab:gitlab … make accept PARTITION=vcs-lab
terraform destroy -var suffix=…
docker rm -f gitlab
```

`make labs-vcs` validates every root against its lock file;
`make labs-cloud` does the same for `test/terraform/cloud`.
