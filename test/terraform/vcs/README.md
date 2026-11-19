# VCS test labs

The repositories that decision 0016's scheduled `vcs-lab` job
(`.github/workflows/vcs-lab.yml`) runs against, as one Terraform root per
VCS provider. Each run applies its provider's root, runs that provider's
`TestAcc<Provider>Lab`, then its `@vcs-lab` source, webhook and feedback
scenarios in that order, and destroys the root, pass or fail. Nothing here
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
  - a GitHub App owned by the organization, registered through the API
    as the lab account's default GitHub App integration, with read access to
    contents and metadata, installed on all the
    organization's repositories; the lab holds nothing else for it to
    reach.
- **GitLab:** nothing. GitLab.com offers group and project access tokens
  only on paid tiers, so the job starts GitLab Community Edition
  (`start-gitlab.sh`), which offers them on every tier, and makes its
  administrator's token and HTTPS certificate for each run.
- **Bitbucket Cloud:** a workspace with a project, and an Atlassian
  account's API token with repository admin/delete/read/write and pull request
  read/write scopes on it. Repository hooks also require
  `read:webhook:bitbucket`, `write:webhook:bitbucket` and
  `delete:webhook:bitbucket` on that existing token.
- **Azure DevOps:** an organization with a project, and a personal access
  token with Code (full).
- **Bitbucket Data Center:** a licence; a developer licence will do. The
  job makes the system administrator's password and the HTTPS certificate
  for each run.

## Secrets

### One-time GitHub App webhook setup and key rotation

The dedicated lab App has no consumers outside this lab. Give it Checks,
Pull requests, Issues and Deployments write, Contents and Metadata read, and
Organization members read. Subscribe to `push`, `pull_request`,
`pull_request_review`, `issue_comment`, `check_run`, `repository` and
`organization`; installation events need no subscription. Approve the updated
permissions on the installation before running the lab.

Activate its webhook once, with a maintainer-controlled HTTPS parking URL,
JSON content type and certificate verification enabled. Activation and event
subscriptions are registration settings; the hook-configuration REST endpoint
does not expose them. This permanent App hook is restored after a lab, not
deleted. Never use an App whose previous secret or other consumers are unknown.

The signing secret is derived from the **existing dedicated App private key**;
there is no additional long-lived workflow secret. The verification helper
parses and validates RSA, canonicalizes PKCS1 or PKCS8 PEM to PKCS8 DER, then
derives 32 bytes with HKDF-SHA256, no salt, and the context
`infraweave/vcs-lab/github-app-webhook/v1/<canonical numeric App ID>`. The secret
is the 64 lowercase hex characters of those bytes. Readiness is `v1:` followed
by SHA256 of that same context, a zero byte and the PKIX public-key DER. The
separately printed public-key fingerprint is SHA256 of the PKIX DER alone.

On a trusted local machine, put the existing App ID and PEM key into
`INFRAWEAVE_TEST_VCS_USER` and `INFRAWEAVE_TEST_VCS_TOKEN` without printing them,
then run:

```sh
GOWORK=off go run ./test/acceptance/cmd/vcs-lab app-setup --secret-file /private/local/lab-hook-secret
```

The requested file must not exist: files and symlinks are refused. It is created
with mode 0600. The command makes no API calls and prints only public metadata.
Use a private local editor to enter that file's value as the dedicated App's
webhook secret, then remove the file. Set the `vcs-lab` environment **variable**
`VCS_GITHUB_HOOK_READY` to the printed readiness value, and
`VCS_GITHUB_HOOK_PARKED_URL` to the exact configured parking URL, only after completing the
setup. This attests the operator setup; an actual signed delivery proves that
GitHub uses the matching secret. Do not store the derived secret in another
environment secret, Terraform source, workflow output or log.

Changing the App ID, private key or derivation version requires repeating this
procedure and updating readiness together. Re-encoding the same private key
does not. Missing or mismatched readiness fails before any App hook mutation.
Runtime refuses acquisition unless the current URL/media/TLS exactly match that
parking baseline, so a lost previous cleanup cannot become a new baseline.
Runtime supplies the same secret explicitly to each served integration create,
but **never sends a `secret` member to GitHub**, even as null, empty or a masked
value. Its separate PATCH document contains only URL, content type and TLS
verification. A private cleanup journal precedes mutation; uncertain responses
still attempt restoration using fresh bounded cleanup time. Cleanup refuses a
current configuration belonging to another session and keeps its journal for
the workflow's always-cleanup step. Workflow concurrency serializes the App;
GitHub provides no atomic compare-and-swap for this endpoint. Abrupt runner loss
can prevent both in-process and workflow cleanup, so the parking baseline and
retained private journal remain the recovery contract.

### Existing environment credentials

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

### Verification phases and cleanup

The workflow opens one checksum-pinned, account-free cloudflared quick tunnel
before a hosted provider's servers start. Its temporary URL is their public
origin; only the canonical POST webhook path is forwarded to the current
scenario. GitLab CE and licensed Bitbucket Data Center instead reach that
webhook-only listener directly on the runner's Docker bridge. The dedicated
GitLab container permits local hook destinations; this does not change a hosted
GitLab installation. The latest tool/image pins live in `test/deps`; the
dependency refresh workflow updates cloudflared from its official release
checksums alongside the other tools.

The immutable source/credential contract and `vcs/sources.feature` finish before
any webhook scenario mutates `main`. A scenario creates its own integration
through the served API, immediately captures the admin-only hook URL and signing
material, and supplies them through a private Terraform variable file. The
four repository-hook roots keep their optional hooks absent until `hook_url`
is supplied; GitHub uses its separate owned App-hook lease. Source scopes and
credentials are verified independently of those native webhook/feedback effects.

Each provider's webhook case performs genuine Git pushes, proves one tracked
and one proposed run, checks tracked-commit movement, opens a native pull request
without another run, and posts an ordinary pull request comment whose verified
delivery is stored without starting a run. Real launchers use the pinned
Terraform release to plan/apply a built-in `terraform_data` resource. The relay
briefly holds their first reports so native checks can be observed before and
after completion. The feedback case enables `feature:add_plan_pr_comment`,
plans twice on the open request, reads exactly one native comment per run and
checks GitHub's actual minimization state for the previous plan. No comment
commands, action buttons, deployment or environment proofs are part of this lab.

GitHub's individual check retains its per-run resource ID; Bitbucket retains its
native named key. GitLab and Azure may retain native POST history for the same
logical named context, so the lab asserts the latest context's owner and result,
never an older successful history row or a fabricated one-row-per-run guarantee.
GitHub reads filter the current App and reject duplicate owned check resources.

Only missing configured secrets, the absent optional Data Center licence, and a
fully joined tunnel startup failure are skips, with an explicit summary reason.
Configured authentication, permissions, native assertions and cleanup failures
fail and use the existing provider-issue reporting. An established tunnel's
unexpected exit also fails. A fresh private session has a random identity and
complete readiness/stop/result receipts: cleanup asks the owning helper to stop
its actual child, then checks the terminal result written after both process
and stream-reader joins. It never signals a persisted PID or adopts stale files.

After each scenario the hook is removed, or the permanent App hook is restored
to its attested parking baseline. Independent cleanup deadlines bound Terraform
and App restoration. Workflow `always()` steps retry owned App cleanup, detach
repository hooks, destroy the disposable repositories and close the run's
tunnel/local containers even after failure. Variable files, local Terraform
state, captured tool diagnostics and cleanup journals are private; no secret or
masked secret is copied into an App PATCH or public failure message.

The environment's main-only deployment rule can prevent a pre-merge dispatch.
In that case the code PR references the ticket rather than closing it: a new
successful main lab run on every configured provider is still required evidence,
and the earlier source-only run does not satisfy these webhook/feedback proofs.

### Manual source contract

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
INFRAWEAVE_ACCEPT_FEATURES=features/vcs/sources.feature INFRAWEAVE_ACCEPT_TAGS=@lab:gitlab … make accept PARTITION=vcs-lab
terraform destroy -var suffix=…
docker rm -f gitlab
```

For the full webhook/feedback proof, configure the delivery listener, public
origin, private hook-journal directory and absolute Terraform root exactly as
the workflow does, then select **one provider and one feature per process**:

```sh
for topic in sources webhooks feedback; do
  INFRAWEAVE_ACCEPT_FEATURES="features/vcs/${topic}.feature" \
    INFRAWEAVE_ACCEPT_TAGS=@lab:gitlab make accept PARTITION=vcs-lab || exit 1
done
```

The generic combined lab corpus is not the manual recipe: the harness runs
scenarios concurrently and in randomized order. Combining these features can
race the single hook listener and run source-history assertions after mutations.
For a local container, `LAB_GITLAB_CONTAINER` and `LAB_GITLAB_PORT` optionally
name its run-owned container and host port; CI keeps the default name/port. On
Docker Desktop, a direct webhook URL uses `host.docker.internal` to reach the
host's webhook-only listener; Linux runners use their Docker bridge gateway.

`make labs-vcs` validates every root against its lock file;
`make labs-cloud` does the same for `test/terraform/cloud`.
