# VCS test labs

The repositories that decision 0016's scheduled `vcs-lab` job
(`.github/workflows/vcs-lab.yml`) runs against, as one Terraform root per
VCS provider. Each run applies its provider's root, runs that provider's
`TestAcc<Provider>Lab` and its `@vcs-lab` scenario in
`vcs_sources.feature`, and destroys the root, pass or fail. Nothing here
is part of infraweave or its reference deployments.

| Root | Creates |
|---|---|
| `github` | two private repositories in the lab organization, both added to the lab App's installation |
| `gitlab` | two private projects in the lab group, and the integration's group access token at Maintainer with `api`, expiring in two days |
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
  - an owner's classic token with `repo`, `delete_repo` and `admin:org`;
  - a GitHub App with read access to contents and metadata, installed on
    the organization for selected repositories, which the root adds the
    run's repositories to.
- **GitLab:** a group on GitLab.com, on a tier that allows group and
  project access tokens, and an owner's personal access token with `api`.
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
reports "not configured" and fails.

| Provider | Secret | What it holds |
|---|---|---|
| GitHub | `VCS_GITHUB_ORGANIZATION` | the organization's login |
| | `VCS_GITHUB_ADMIN_TOKEN` | the owner's token |
| | `VCS_GITHUB_APP_ID`, `VCS_GITHUB_APP_PRIVATE_KEY` | the App's ID and PEM private key |
| | `VCS_GITHUB_APP_INSTALLATION_ID` | its installation on the organization |
| GitLab | `VCS_GITLAB_GROUP` | the group's full path |
| | `VCS_GITLAB_ADMIN_TOKEN` | the owner's token |
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
cd test/terraform/vcs/gitlab
export GITLAB_TOKEN=… SEED_USER=oauth2 SEED_TOKEN=…
terraform init
terraform apply -var group=<group> -var suffix="$(date +%s)-local"
```

Then set the tests' variables from `terraform output`. Each output's
description names its variable.

```sh
INFRAWEAVE_TEST_VCS_HOST=… INFRAWEAVE_TEST_VCS_TOKEN=… … \
  go test -run '^TestAccGitLabLab$' ./internal/vcs/internal/adapters/gitlab
INFRAWEAVE_ACCEPT_TAGS=@lab:gitlab … make accept PARTITION=vcs-lab
terraform destroy -var group=<group> -var suffix=…
```

`make labs-vcs` validates every root against its lock file;
`make labs-cloud` does the same for `test/terraform/cloud`.
