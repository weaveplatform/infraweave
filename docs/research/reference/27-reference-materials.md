# 27: Reference materials

Checked against: each project's documentation site read 2026-10-28; Atlantis `9a10cfd470c0`, OTF `cf7f7836d859`, Terrakube `2a7ddfb1bfa1`, Digger `7c237c093d72`, Burrito `ed21360c45e6`, Terramate `1f2f2d72a5c1`, tofu-controller `e2db61bdd264`, OPA `359db769deb0`, Temporal `41d9f3b60536`, River `fc6c1d57cc72`; Spacelift repositories at the commits in report 01.

Research baseline: **2026-10-28**.

This report records the open-source tools that already solve parts of what
infraweave builds, what each does, and which of its patterns infraweave can
take or must avoid. It covers seven Terraform and OpenTofu automation tools
(Atlantis, OTF, Terrakube, Digger, Burrito, Terramate and the Flux
tofu-controller), the policy engine Spacelift itself embeds (OPA), and two
candidate engines for queues and workflows (Temporal and River). For each it
records the licence, the language, the latest release, the commit read, the
architecture, the Spacelift features it covers, and a **Borrow** and a **Do
not copy** list with citations at the commit. Section 11 points to the
Spacelift open-source pieces that reports 10 and 24 already describe, and
section 12 is a cross-tool table by concern. The Spacelift behaviour each
comparison is measured against is in
[08](08-run-lifecycle.md) (runs and queueing),
[09](09-policies.md) (policies),
[10](10-workers-and-execution.md) (workers),
[16](16-state-management.md) (state),
[18](18-scheduling-drift-dependencies.md) (scheduling and drift) and
[24](24-self-hosted-reference.md) (self-hosted topology). Report 30 collects
the open questions.

| Label | Means |
|---|---|
| **[D]** | The project's own documentation site, cited by URL, read 2026-10-28; where the site is built from the repository, the page source at the commit read was used alongside the fetched page |
| **[S]** | Source code at the commit read, cited by a GitHub link pinned to that commit |
| **[I]** | Inferred; the text says from what |
| *(unverified)* | No primary source confirms it; listed in 30-open-questions |

The repositories were cloned shallowly and read, never built or run. The
commit read for each is in its section and in the summary table. Commit dates and latest releases come from the GitHub release
list on 2026-10-28.

## Summary

| Project | Language | Licence | Latest release | Commit read | Shape | Closest Spacelift area |
|---|---|---|---|---|---|---|
| Atlantis | Go | Apache-2.0 | v0.49.0, 2026-10-25 | `9a10cfd470c0` (2026-10-27) | One server process driven by PR comments | PR workflow, proposed runs, plan policies |
| OTF | Go | MPL-2.0 | v0.6.3, 2026-06-11 | `cf7f7836d859` (2026-07-16) | Terraform Cloud API clone: `otfd` plus `otf-agent` | Stacks, run queue, agents, state, registry |
| Terrakube | Java (Spring Boot), React UI | Apache-2.0 | 2.33.3, 2026-10-21 | `2a7ddfb1bfa1` (2026-10-27) | API, executor, registry and UI services | Templates, schedules, OPA policy sets, agents |
| Digger (now OpenTaco) | Go | MIT, with `backend/` Apache-2.0 and `ee/` under a proprietary licence | v0.6.154, 2026-10-07 | `7c237c093d72` (2026-10-22) | Orchestrator that dispatches jobs to the customer's CI | PR workflow, locking, drift, state backend |
| Burrito | Go | Apache-2.0 | v0.15.0, 2026-10-23 | `ed21360c45e6` (2026-10-26) | Kubernetes operator with runner pods | Drift detection, tracked runs, PR plans |
| Terramate | Go | MPL-2.0 | v0.17.3, 2026-09-08 | `1f2f2d72a5c1` (2026-10-16) | CLI orchestrator; commercial cloud for dashboards and drift | Stack dependencies, change detection |
| tofu-controller | Go | Apache-2.0 | v0.16.6, 2026-10-26 | `e2db61bdd264` (2026-10-28) | Flux controller driving runner pods over gRPC | Drift and reconcile, plan approval |
| OPA | Go | Apache-2.0 | v1.21.4, 2026-10-23 | `359db769deb0` (2026-10-28) | Embeddable Rego engine | The policy engine (report 09) |
| Temporal | Go | MIT | v1.32.1, 2026-10-08 | `41d9f3b60536` (2026-10-28) | Durable workflow service with polling workers | Run orchestration, schedules |
| River | Go | MPL-2.0 | v0.50.0, 2026-10-27 | `fc6c1d57cc72` (2026-10-26) | Postgres job queue library | Self-hosted Postgres queue (report 24) |

## 1. Licence position

infraweave's own licence is permissive. The table classifies each project by
what infraweave may take from it.

| Project | Licence evidence | Class | What may be taken |
|---|---|---|---|
| Atlantis | [`LICENSE`](https://github.com/runatlantis/atlantis/blob/9a10cfd470c0/LICENSE); files carry `SPDX-License-Identifier: Apache-2.0` [S] | Permissive | Patterns; brief quotes with attribution |
| OTF | [`LICENSE`](https://github.com/leg100/otf/blob/cf7f7836d859/LICENSE) is MPL-2.0 [S] | **Weak copyleft** (file-level) | Patterns only; no code |
| Terrakube | [`LICENSE`](https://github.com/AzBuilder/terrakube/blob/2a7ddfb1bfa1/LICENSE) is Apache-2.0 [S] | Permissive | Patterns; brief quotes with attribution |
| Digger, root | [`LICENSE`](https://github.com/diggerhq/digger/blob/7c237c093d72/LICENSE) is MIT [S] | Permissive | Patterns; brief quotes |
| Digger, `backend/` | [`backend/LICENSE`](https://github.com/diggerhq/digger/blob/7c237c093d72/backend/LICENSE) is Apache-2.0 [S] | Permissive | Patterns; brief quotes |
| Digger, `ee/` | [`ee/LICENSE`](https://github.com/diggerhq/digger/blob/7c237c093d72/ee/LICENSE) is the "Digger Enterprise license": production use needs a valid Digger Enterprise licence; patches may be made, but Digger owns them and they may be used only under such a licence; without one, copying and modifying are allowed for development and testing only, and distribution is forbidden [S] | **Source-available, proprietary** | Nothing; not read beyond the directory listing |
| Burrito | [`LICENSE`](https://github.com/padok-team/burrito/blob/ed21360c45e6/LICENSE) is Apache-2.0 [S] | Permissive | Patterns; brief quotes |
| Terramate | [`LICENSE`](https://github.com/terramate-io/terramate/blob/1f2f2d72a5c1/LICENSE) is MPL-2.0; files carry `SPDX-License-Identifier: MPL-2.0` [S] | **Weak copyleft** | Patterns only; no code |
| Terramate Cloud | A hosted service; the CLI only carries its client ([`cloud/client.go`](https://github.com/terramate-io/terramate/blob/1f2f2d72a5c1/cloud/client.go)) [S] | Proprietary service | Nothing beyond documented behaviour |
| tofu-controller | [`LICENSE`](https://github.com/flux-iac/tofu-controller/blob/e2db61bdd264/LICENSE) is Apache-2.0 [S] | Permissive | Patterns; brief quotes |
| OPA | [`LICENSE`](https://github.com/open-policy-agent/opa/blob/359db769deb0/LICENSE) is Apache-2.0 [S] | Permissive | Usable as a library dependency |
| Temporal server | [`LICENSE`](https://github.com/temporalio/temporal/blob/41d9f3b60536/LICENSE) is MIT [S] | Permissive | Usable as a dependency; the Go SDK `temporalio/sdk-go` is also MIT (GitHub licence metadata, latest v1.50.0 on 2026-10-15) [S] |
| River | [`LICENSE`](https://github.com/riverqueue/river/blob/fc6c1d57cc72/LICENSE) is MPL-2.0 [S] | **Weak copyleft** | Patterns; use as an unmodified dependency is a licence question for the plan (see below) |
| River Pro | A private Go module sold by subscription; it adds concurrency limits, a dead letter queue, durable periodic jobs, encrypted jobs, ephemeral jobs, sequences, workflows, batching and per-queue retention [D] [River Pro](https://riverqueue.com/docs/pro) | Proprietary | Nothing beyond documented behaviour |

- MPL-2.0 is copyleft per file: a modified covered file stays under MPL-2.0,
  while a larger work that only links the files may carry other terms
  (MPL-2.0 sections 3.2 and 3.3, in each `LICENSE` above) [S]. That copying
  code from OTF, Terramate or River into infraweave's own files would bring
  those files under MPL-2.0 follows from that [I]. This report therefore
  cites MPL-2.0 sources and describes their patterns in its own words, and
  quotes none of them.
- No project in scope uses BUSL or AGPL. The Digger `ee/` licence is the only
  source-available licence found, and River Pro, Terramate Cloud, Temporal
  Cloud and Spacelift itself are proprietary services.
- Quotes below come only from Apache-2.0 and MIT files, and stay within a
  few lines.

## 2. Atlantis

| Item | Value |
|---|---|
| Repository | <https://github.com/runatlantis/atlantis> at `9a10cfd470c0` |
| Licence, language | Apache-2.0; Go (`go 1.27.3` in `go.mod`) |
| Latest release | v0.49.0, 2026-10-25 |
| Docs | <https://www.runatlantis.io/docs/> |

### 2.1 Architecture

| Concern | Atlantis | Evidence |
|---|---|---|
| Processes | One `atlantis server` process: it receives VCS webhooks, parses PR comments, clones repositories into `--data-dir`, runs Terraform, and posts results back | [D] [FAQ](https://www.runatlantis.io/docs/faq); [S] [`events_controller.go#L101-L170`](https://github.com/runatlantis/atlantis/blob/9a10cfd470c0/server/controllers/events/events_controller.go#L101-L170) |
| Persistence | A `Database` interface for project locks, pull status and command locks, with BoltDB (default, one process only) or Redis (single node or cluster) behind it | [S] [`db.go#L18-L36`](https://github.com/runatlantis/atlantis/blob/9a10cfd470c0/server/core/db/db.go#L18-L36); [D] [server configuration, `--locking-db-type`](https://www.runatlantis.io/docs/server-configuration) |
| Queueing | None durable. The webhook handler validates the event and starts the command in a goroutine (`go e.CommandRunner.RunAutoplanCommand(...)`, `go e.CommandRunner.RunCommentCommand(...)`) | [S] [`events_controller.go#L593`](https://github.com/runatlantis/atlantis/blob/9a10cfd470c0/server/controllers/events/events_controller.go#L593), [`#L742`](https://github.com/runatlantis/atlantis/blob/9a10cfd470c0/server/controllers/events/events_controller.go#L742) |
| How runs execute | Projects of one command run in parallel through a sized wait group of `--parallel-pool-size` | [S] [`project_command_pool_executor.go#L37-L47`](https://github.com/runatlantis/atlantis/blob/9a10cfd470c0/server/events/project_command_pool_executor.go#L37-L47); [D] [server configuration](https://www.runatlantis.io/docs/server-configuration) |
| Worker model | The server is the worker; Terraform runs as a child process of the server | [S] [`server/core/runtime`](https://github.com/runatlantis/atlantis/tree/9a10cfd470c0/server/core/runtime) |
| Locking | A lock per repository, path, workspace and project name, taken on plan and released on merge, close or `atlantis unlock`; separate from Terraform's state lock | [S] [`models.go#L355-L357`](https://github.com/runatlantis/atlantis/blob/9a10cfd470c0/server/events/models/models.go#L355-L357); [D] [locking](https://www.runatlantis.io/docs/locking) |
| Plan storage | A `PlanStore` interface: local disk by default, or S3-compatible storage (`--enable-external-stores`) so that a different replica can apply | [S] [`plan_store.go#L33-L50`](https://github.com/runatlantis/atlantis/blob/9a10cfd470c0/server/core/planstore/plan_store.go#L33-L50); [D] [FAQ](https://www.runatlantis.io/docs/faq) |
| High availability | Several replicas need Redis locking plus a shared data directory or the external plan store; the deployment guide uses one replica | [D] [FAQ](https://www.runatlantis.io/docs/faq), [deployment](https://www.runatlantis.io/docs/deployment) |
| State backend | None; projects use their own Terraform backends | [D] [how Atlantis works](https://www.runatlantis.io/docs/how-atlantis-works) |
| Policy engine | conftest, downloaded as a binary and run against the plan JSON in a `policy_check` step; failures block apply until a policy owner approves | [S] [`conftest_client.go#L32-L33`](https://github.com/runatlantis/atlantis/blob/9a10cfd470c0/server/core/runtime/policy/conftest_client.go#L32-L33); [D] [policy checking](https://www.runatlantis.io/docs/policy-checking) |
| VCS integration | Webhooks from GitHub, GitLab, Bitbucket Cloud and Server, Azure DevOps and Gitea, each with its own signature validator | [S] [`server/controllers/events`](https://github.com/runatlantis/atlantis/tree/9a10cfd470c0/server/controllers/events) |
| Drift detection | Alpha API endpoints (`--enable-drift-detection`) that plan without a PR and store results in memory; remediation apply needs `--enable-drift-remediation` | [D] [server configuration](https://www.runatlantis.io/docs/server-configuration), [API endpoints](https://www.runatlantis.io/docs/api-endpoints); [S] [`drift/storage.go#L17-L38`](https://github.com/runatlantis/atlantis/blob/9a10cfd470c0/server/core/drift/storage.go#L17-L38) |
| Multi-tenancy | Repository allowlist, server-side repo config with allowed overrides, and team allowlists per command | [S] [`global_cfg.go#L93-L117`](https://github.com/runatlantis/atlantis/blob/9a10cfd470c0/server/core/config/valid/global_cfg.go#L93-L117), [`team_authz.go`](https://github.com/runatlantis/atlantis/blob/9a10cfd470c0/server/core/config/valid/team_authz.go) |

### 2.2 Spacelift coverage

| Spacelift feature | Atlantis | Note |
|---|---|---|
| Proposed runs on PRs, PR comments | Covered | Its core workflow |
| Tracked runs on merge to the tracked branch | Not covered | Apply happens from the PR before merge |
| Plan policies | Partly | conftest only; no push, trigger, approval, login or notification policy types |
| Approval | Partly | Apply requirements (`approved`, `mergeable`) and policy owner approval |
| Private workers | Not covered | Terraform runs inside the server |
| Managed state | Not covered | |
| Module registry | Not covered | |
| Spaces, RBAC | Not covered | Allowlists only |
| Scheduled runs, drift | Partly | Drift API, alpha; no scheduler |
| Stack dependencies | Partly | `depends_on` between projects in `atlantis.yaml` (`MergedProjectCfg.DependsOn`) [S] [`global_cfg.go#L119-L130`](https://github.com/runatlantis/atlantis/blob/9a10cfd470c0/server/core/config/valid/global_cfg.go#L119-L130) |

### 2.3 Borrow

- **Lock key shape.** A lock keyed on repository, path, workspace and project
  name, held across the PR's lifetime, is a working model for Spacelift's
  per-stack exclusive lock and the push-policy lock keys in report 08. The
  key is one format string, quoted from the Apache-2.0 source [S]
  [`models.go#L355-L357`](https://github.com/runatlantis/atlantis/blob/9a10cfd470c0/server/events/models/models.go#L355-L357):

  ```go
  return fmt.Sprintf("%s/%s/%s/%s", project.RepoFullName, project.Path, workspace, project.ProjectName)
  ```
- **Server-side config with allowed overrides.** `Repo` holds what the
  administrator fixes per repository (by exact ID or regex), and
  `AllowedOverrides` names what the in-repository `atlantis.yaml` may change
  [S] [`global_cfg.go#L93-L117`](https://github.com/runatlantis/atlantis/blob/9a10cfd470c0/server/core/config/valid/global_cfg.go#L93-L117).
  This is the same split as Spacelift's stack settings against the runtime
  `.spacelift/config.yml` (report 07) [I].
- **Plan artefact store as an interface.** `PlanStore` with `Save`, `Load`,
  `Remove` and restore separates "where the plan file lives between plan and
  apply" from the runner [S]
  [`plan_store.go#L33-L50`](https://github.com/runatlantis/atlantis/blob/9a10cfd470c0/server/core/planstore/plan_store.go#L33-L50).
  Spacelift's paused runs resume in a new container with the workspace
  restored from storage (report 08), which needs the same seam [I].
- **Drift storage contract.** The `drift.Storage` interface keys results by
  repository, project, path, workspace and ref, and documents that plan
  output is never persisted [S]
  [`drift/storage.go#L17-L60`](https://github.com/runatlantis/atlantis/blob/9a10cfd470c0/server/core/drift/storage.go#L17-L60).
- **Graceful drain.** `Drainer` refuses new operations once shutdown starts
  and blocks until in-flight ones finish [S]
  [`drainer.go#L10-L60`](https://github.com/runatlantis/atlantis/blob/9a10cfd470c0/server/events/drainer.go#L10-L60).
- **Webhook validators per VCS**, each in its own file with tests, as a
  checklist for report 11's providers [S]
  [`server/controllers/events`](https://github.com/runatlantis/atlantis/tree/9a10cfd470c0/server/controllers/events).

### 2.4 Do not copy

- **Goroutine dispatch from the webhook handler.** Work started with `go` after
  the webhook returns has no durable record, so a crash between the response
  and the command loses the event [I] from
  [`events_controller.go#L593`](https://github.com/runatlantis/atlantis/blob/9a10cfd470c0/server/controllers/events/events_controller.go#L593).
  Spacelift puts webhooks on a `webhooks` queue with a dead-letter queue
  (report 24, section 3.3).
- **Running Terraform inside the API process.** It ties API availability to
  run load, needs persistent disk, and gives runs the server's credentials
  [I] from [deployment](https://www.runatlantis.io/docs/deployment), which
  recommends a StatefulSet because data lives on disk [D].
- **Shelling out to a downloaded policy binary.** conftest is fetched from
  GitHub releases at run time [S]
  [`conftest_client.go#L32-L33`](https://github.com/runatlantis/atlantis/blob/9a10cfd470c0/server/core/runtime/policy/conftest_client.go#L32-L33);
  Spacelift evaluates Rego in-process with per-type timeouts (report 09),
  which needs an embedded engine (section 9).
- **BoltDB as the default store**, which permits one process only [D]
  [server configuration](https://www.runatlantis.io/docs/server-configuration).

## 3. OTF

| Item | Value |
|---|---|
| Repository | <https://github.com/leg100/otf> at `cf7f7836d859` |
| Licence, language | **MPL-2.0** (patterns only); Go |
| Latest release | v0.6.3, 2026-06-11 |
| Docs | <https://docs.otf.ninja/> |

### 3.1 Architecture

| Concern | OTF | Evidence |
|---|---|---|
| Processes | `otfd` (API, web UI, scheduler, embedded runner) and optional `otf-agent` processes; `otf-job` runs one job for the Kubernetes executor | [S] [`cmd/`](https://github.com/leg100/otf/tree/cf7f7836d859/cmd); [D] [runners](https://docs.otf.ninja/runners/) |
| Persistence | PostgreSQL only; 24 migrations under `internal/sql/migrations` | [S] [`internal/sql/migrations`](https://github.com/leg100/otf/tree/cf7f7836d859/internal/sql/migrations) |
| Events between replicas | Table triggers call `pg_notify('events', ...)`; each `otfd` holds a `LISTEN` connection and forwards events to in-process brokers | [S] [`001_migrate_migrator.sql#L20-L40`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/sql/migrations/001_migrate_migrator.sql#L20-L40), [`db.go#L181-L210`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/sql/db.go#L181-L210), [`listener.go`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/sql/listener.go) |
| Singletons | Subsystems marked `Exclusive` (scheduler, allocator, manager) run on one replica, guarded by a session-level `pg_advisory_lock` on a dedicated connection | [S] [`subsystem.go#L22-L33`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/daemon/subsystem.go#L22-L33), [`db.go#L159-L180`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/sql/db.go#L159-L180) |
| Queueing | Two levels: a per-workspace queue of runs (head blocks the rest; speculative runs skip it) and a global queue of phases (plan, apply) for runners | [S] [`ALGORITHMS.md`](https://github.com/leg100/otf/blob/cf7f7836d859/ALGORITHMS.md), [`run/scheduler.go#L18-L33`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/run/scheduler.go#L18-L33) |
| Allocation | One allocator assigns unallocated jobs to runners in the job's pool (server runners for remote mode, pool agents for agent mode), skips full `fork` runners, prefers the most recent ping, and reallocates from unhealthy runners | [S] [`allocator.go#L158-L268`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/runner/allocator.go#L158-L268) |
| Worker model | Runners register, then long-poll for allocated jobs; executors are `fork` (child processes, `--concurrency` default 5) or `kubernetes` (a Kubernetes Job per job) | [S] [`runner/api/api.go#L58-L68`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/runner/api/api.go#L58-L68); [D] [executors](https://docs.otf.ninja/executors/), [flags](https://docs.otf.ninja/config/flags/) |
| Runner health | Status `idle`, `busy`, `exited`, `errored`, `unknown`; no ping for 30 s gives `unknown`, five more minutes gives `errored`, and terminal runners are purged after an hour | [S] [`status.go`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/runner/status.go), [`manager.go#L13-L14`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/runner/manager.go#L13-L14), [`#L80-L103`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/runner/manager.go#L80-L103) |
| Cancellation | Cancel and force-cancel signals travel by Postgres notification to a one-shot `await-signal` long-poll per job | [S] [`job_signaler.go#L17-L85`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/runner/job_signaler.go#L17-L85) |
| Credentials | Agent tokens per pool (no expiry); a short-lived job token per job, which the job uses against the API | [S] [`tokens.go#L40-L63`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/runner/tokens.go#L40-L63) |
| Stuck runs | A timeout daemon checks every minute for runs whose plan or apply phase exceeds its limit | [S] [`timeout.go#L14-L60`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/run/timeout.go#L14-L60) |
| State backend | The Terraform Cloud state-version API, so the `cloud` block and `remote` backend work unchanged | [S] [`internal/state`](https://github.com/leg100/otf/tree/cf7f7836d859/internal/state); [D] [TFE API](https://docs.otf.ninja/tfe_api/) |
| Policy engine | None | [S] no Sentinel or OPA code under `internal/` |
| VCS integration | GitHub (including a GitHub App), GitLab and Forgejo; one webhook per repository shared by all workspaces and modules that use it, created and deleted by a synchroniser | [S] [`internal/repohooks`](https://github.com/leg100/otf/tree/cf7f7836d859/internal/repohooks), [`service.go#L60-L100`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/repohooks/service.go#L60-L100) |
| Module registry | The Terraform module registry protocol; versions publish from VCS tag events; downloads go through signed URLs | [S] [`module/registry.go#L34-L39`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/module/registry.go#L34-L39), [`publisher.go#L24-L40`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/module/publisher.go#L24-L40); [D] [registry](https://docs.otf.ninja/registry/) |
| Multi-tenancy | Organizations with an `owners` team; team roles per organization and per workspace (read, plan, write, admin); site admins | [S] [`authz/role.go`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/authz/role.go); [D] [RBAC](https://docs.otf.ninja/rbac/) |
| Drift, scheduling | None | [S] no drift or cron code under `internal/` |

### 3.2 Spacelift coverage

| Spacelift feature | OTF | Note |
|---|---|---|
| Stacks, tracked and proposed runs | Covered | As TFC workspaces and speculative runs |
| Run queue per stack | Covered | Workspace queue |
| Private worker pools | Covered | Agent pools and tokens |
| Managed state, history | Covered | TFC state versions |
| Module registry | Covered | Modules only; no provider registry |
| Policies | Not covered | |
| Spaces | Not covered | Flat organizations |
| Drift, schedules, stack dependencies | Not covered | Run triggers exist (migration `022_add_run_triggers_table.sql`) [S] |
| Dynamic cloud credentials | Covered | `/jobs/{job_id}/dynamic-credentials` [S] [`api.go#L65`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/runner/api/api.go#L65) |

### 3.3 Borrow (patterns only, MPL-2.0)

- **A worker protocol of seven HTTP calls.** Register, report status,
  long-poll for allocated jobs, start, finish, get a job, and long-poll for a
  cancel signal [S]
  [`api.go#L58-L68`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/runner/api/api.go#L58-L68).
  It matches Spacelift's surviving transport, worker-initiated HTTP
  long-poll with job assignment plus stop and kill signals (report 10,
  section 4.2) [I].
- **Two-level queue.** A per-stack queue of runs, where the head blocks the
  rest, and a pool-wide queue of phases, matches Spacelift's stack lock plus
  per-pool scheduler (report 08, section 4) [S]
  [`ALGORITHMS.md`](https://github.com/leg100/otf/blob/cf7f7836d859/ALGORITHMS.md) [I].
- **Server-side allocation with health-driven reallocation.** Jobs are
  pushed to a chosen runner rather than raced for, and a job on a runner
  that stops pinging is moved [S]
  [`allocator.go#L158-L268`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/runner/allocator.go#L158-L268).
  Spacelift's `Worker` statuses and drain (report 10, section 3) need the
  same heartbeat-to-status rules [I].
- **Postgres as the only dependency.** `LISTEN`/`NOTIFY` fan-out between
  replicas and advisory-lock singletons avoid a broker, which suits the
  self-hosted shape in report 24 where Postgres and object storage are the
  only hard dependencies [S]
  [`db.go#L159-L210`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/sql/db.go#L159-L210) [I].
- **Job-scoped tokens.** A short-lived token per job, separate from the
  long-lived pool token, mirrors Spacelift's per-run state credentials
  checked against the run's liveness (report 16, section 1.1) [S]
  [`tokens.go#L44-L49`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/runner/tokens.go#L44-L49) [I].
- **Shared, reference-counted repository webhooks** with a synchroniser that
  repairs drift between the database and the VCS [S]
  [`repohooks/service.go#L60-L100`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/repohooks/service.go#L60-L100).
- **A run-timeout daemon** as a backstop for runs orphaned by a crash [S]
  [`timeout.go#L14-L60`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/run/timeout.go#L14-L60);
  Spacelift's 2, 7 and 30 day timers (report 08) need the same sweeper [I].

### 3.4 Do not copy

- **Any code**, because of MPL-2.0 (section 1).
- **The Terraform Cloud API as the public contract.** infraweave's contract
  is Spacelift's surface projected to REST (decision 0001); TFC's JSON:API
  shapes would be a second surface [I].
- **One in-memory allocator** that holds runner and job maps and must be the
  only active one [S]
  [`allocator.go#L13-L15`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/runner/allocator.go#L13-L15):
  failover waits for the advisory lock to move, and the allocator rebuilds
  its cache from the database [I] from `seed` at
  [`allocator.go#L141`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/runner/allocator.go#L141).
- **Agent tokens without expiry** [S]
  [`tokens.go#L63`](https://github.com/leg100/otf/blob/cf7f7836d859/internal/runner/tokens.go#L63);
  Spacelift pool credentials are a CSR-signed certificate and a key the
  customer holds (report 10).

## 4. Terrakube

| Item | Value |
|---|---|
| Repository | <https://github.com/AzBuilder/terrakube> at `2a7ddfb1bfa1` |
| Licence, language | Apache-2.0; Java (Spring Boot, Elide), React UI |
| Latest release | 2.33.3, 2026-10-21 |
| Docs | <https://docs.terrakube.io/> |

### 4.1 Architecture

| Concern | Terrakube | Evidence |
|---|---|---|
| Processes | API (JSON:API and GraphQL), executor, Open Registry, UI; Dex for authentication; a Go CLI | [D] [architecture](https://docs.terrakube.io/getting-started/design-and-architecture) |
| Persistence | Any relational database Liquibase supports; cloud storage (Azure, S3, GCS, MinIO) for state, outputs and modules; Redis for executor coordination and log streams | [D] [architecture](https://docs.terrakube.io/getting-started/design-and-architecture), [self-hosted agents](https://docs.terrakube.io/getting-started/deployment/self-hosted-agents) |
| Queueing and scheduling | Quartz with a clustered JDBC job store (`isClustered=true`) so that several API replicas share triggers; each job is a Quartz job that repeats on an interval to advance its flow | [S] [`application.properties#L97-L106`](https://github.com/AzBuilder/terrakube/blob/2a7ddfb1bfa1/api/src/main/resources/application.properties#L97-L106), [`ScheduleJobService.java#L56-L96`](https://github.com/AzBuilder/terrakube/blob/2a7ddfb1bfa1/api/src/main/java/io/terrakube/api/plugin/scheduler/ScheduleJobService.java#L56-L96) |
| Job states | `pending`, `waitingApproval`, `approved`, `queue`, `running`, `completed`, `noChanges`, `notExecuted`, `rejected`, `cancelled`, `failed`, `unknown`, `NeverExecuted`; five states hold the workspace queue | [S] [`JobStatus.java#L7-L30`](https://github.com/AzBuilder/terrakube/blob/2a7ddfb1bfa1/api/src/main/java/io/terrakube/api/rs/job/JobStatus.java#L7-L30) |
| How runs execute | A job follows a template written in the Terrakube Configuration Language: a YAML flow of steps of type `terraformPlan`, `terraformPlanDestroy`, `terraformApply`, `terraformDestroy`, `customScripts`, `approval`, `disableWorkspace`, `scheduleTemplates`, `policyEvaluation` | [S] [`FlowType.java`](https://github.com/AzBuilder/terrakube/blob/2a7ddfb1bfa1/api/src/main/java/io/terrakube/api/plugin/scheduler/job/tcl/model/FlowType.java) |
| Worker model | Push: the API POSTs each step to an executor URL (per organization agent, or the default); a busy executor answers `503` and the API retries elsewhere. Ephemeral mode creates a Kubernetes `batch/v1` Job per step | [S] [`PersistentExecutorService.java#L93-L120`](https://github.com/AzBuilder/terrakube/blob/2a7ddfb1bfa1/api/src/main/java/io/terrakube/api/plugin/scheduler/job/tcl/executor/persistent/PersistentExecutorService.java#L93-L120), [`EphemeralExecutorService.java#L388-L436`](https://github.com/AzBuilder/terrakube/blob/2a7ddfb1bfa1/api/src/main/java/io/terrakube/api/plugin/scheduler/job/tcl/executor/ephemeral/EphemeralExecutorService.java#L388-L436); [D] [self-hosted agents](https://docs.terrakube.io/getting-started/deployment/self-hosted-agents), [ephemeral agents](https://docs.terrakube.io/getting-started/deployment/ephemeral-agents) |
| State backend | The executor writes state to cloud storage; the API also serves the Terraform Cloud remote API so that CLI-driven workspaces work | [S] [`tfstate`](https://github.com/AzBuilder/terrakube/tree/2a7ddfb1bfa1/executor/src/main/java/io/terrakube/executor/plugin/tfstate), [`RemoteTfeService.java`](https://github.com/AzBuilder/terrakube/blob/2a7ddfb1bfa1/api/src/main/java/io/terrakube/api/plugin/state/RemoteTfeService.java) |
| Policy engine | OPA, run as the `opa eval` binary in the executor against the plan JSON; policy sets carry advisory, soft-mandatory and hard-mandatory results, and the API records evaluations per job and step | [S] [`OpaExecutorServiceImpl.java#L238-L260`](https://github.com/AzBuilder/terrakube/blob/2a7ddfb1bfa1/executor/src/main/java/io/terrakube/executor/service/opa/OpaExecutorServiceImpl.java#L238-L260), [`TerraformExecutorServiceImpl.java#L324-L337`](https://github.com/AzBuilder/terrakube/blob/2a7ddfb1bfa1/executor/src/main/java/io/terrakube/executor/service/terraform/TerraformExecutorServiceImpl.java#L324-L337), [`PolicyEvaluationService.java`](https://github.com/AzBuilder/terrakube/blob/2a7ddfb1bfa1/api/src/main/java/io/terrakube/api/plugin/policy/PolicyEvaluationService.java); [D] [policy enforcement](https://docs.terrakube.io/user-guide/policy-enforcement-opa) |
| VCS integration | GitHub (including apps), GitLab, Bitbucket and Azure DevOps providers; scheduled VCS and GitHub App token refresh | [S] [`plugin/vcs/provider`](https://github.com/AzBuilder/terrakube/tree/2a7ddfb1bfa1/api/src/main/java/io/terrakube/api/plugin/vcs/provider), [`ScheduleGitHubAppTokenService.java`](https://github.com/AzBuilder/terrakube/blob/2a7ddfb1bfa1/api/src/main/java/io/terrakube/api/plugin/scheduler/ScheduleGitHubAppTokenService.java) |
| Webhook intake | Each repository webhook delivery is committed as a row before the response; a worker claims it with an atomic conditional `UPDATE` from pending to processing, fans out to workspaces outside any transaction, and records the result keyed on the claim's `lastAttemptAt`, so a delivery reclaimed by the stuck-row sweep cannot be overwritten by the older attempt | [S] [`RepoWebhookDeliveryTransactions.java#L36-L83`](https://github.com/AzBuilder/terrakube/blob/2a7ddfb1bfa1/api/src/main/java/io/terrakube/api/plugin/vcs/RepoWebhookDeliveryTransactions.java#L36-L83), [`RepoWebhookDispatchService.java#L41-L68`](https://github.com/AzBuilder/terrakube/blob/2a7ddfb1bfa1/api/src/main/java/io/terrakube/api/plugin/vcs/RepoWebhookDispatchService.java#L41-L68) |
| Module registry | A separate Open Registry service for modules and providers, behind Dex | [S] [`registry/`](https://github.com/AzBuilder/terrakube/tree/2a7ddfb1bfa1/registry); [D] [private registry](https://docs.terrakube.io/user-guide/private-registry) |
| Drift detection | Not a built-in feature: a documented recipe of a scheduled template, a Rego policy that counts changes, and a Slack step | [D] [drift detection](https://docs.terrakube.io/user-guide/drift-detection) |
| Multi-tenancy | Organizations; teams mapped from Dex groups, each with booleans such as `manageState`, `manageWorkspace`, `manageModule`, `managePolicies`, `planJob`, `approveJob` | [S] [`Team.java#L39-L73`](https://github.com/AzBuilder/terrakube/blob/2a7ddfb1bfa1/api/src/main/java/io/terrakube/api/rs/team/Team.java#L39-L73) |

### 4.2 Spacelift coverage

| Spacelift feature | Terrakube | Note |
|---|---|---|
| Stacks, runs with approval | Covered | Workspaces and templates |
| Custom phases and hooks | Covered | Template steps, broader than Spacelift hooks |
| Plan policies | Covered | Policy sets with three enforcement levels |
| Other policy types | Not covered | No push, trigger, login or notification policies |
| Private workers | Covered | Agents per organization, push model |
| Managed state | Covered | Cloud storage plus TFC remote API |
| Module and provider registry | Covered | Separate service |
| Scheduled runs | Covered | Workspace schedules with cron |
| Drift detection | Partly | Recipe, not a feature |
| Spaces | Not covered | Organizations and teams only |

### 4.3 Borrow

- **A durable webhook inbox with claim and fencing.** Commit the delivery,
  acknowledge the VCS, claim the row with a conditional update, do slow
  fan-out with no transaction open, and record the outcome only if the claim
  is still current [S]
  [`RepoWebhookDeliveryTransactions.java#L36-L83`](https://github.com/AzBuilder/terrakube/blob/2a7ddfb1bfa1/api/src/main/java/io/terrakube/api/plugin/vcs/RepoWebhookDeliveryTransactions.java#L36-L83).
  The dispatch service also isolates per-workspace failures so that a
  retried delivery never creates a second job for a workspace already
  handled [S]
  [`RepoWebhookDispatchService.java#L63-L68`](https://github.com/AzBuilder/terrakube/blob/2a7ddfb1bfa1/api/src/main/java/io/terrakube/api/plugin/vcs/RepoWebhookDispatchService.java#L63-L68).
  The source notes that holding one transaction across the fan-out made a
  webhook shared by 30 workspaces exceed GitHub's 10-second delivery timeout
  [S] [`#L41-L48`](https://github.com/AzBuilder/terrakube/blob/2a7ddfb1bfa1/api/src/main/java/io/terrakube/api/plugin/vcs/RepoWebhookDispatchService.java#L41-L48).
  Spacelift's push policies fan one event out to many stacks (report 11)
  [I].
- **A clustered scheduler on the database.** Quartz's JDBC store with
  `isClustered=true` lets several API replicas share cron triggers without
  duplicates [S]
  [`application.properties#L97-L104`](https://github.com/AzBuilder/terrakube/blob/2a7ddfb1bfa1/api/src/main/resources/application.properties#L97-L104).
  Spacelift's drain also fires cron since v6.4.0 (report 24) [I].
- **Explicit "holds the queue" set on the job state enum.** The states that
  block newer jobs on a workspace are a named set next to the terminal set
  [S]
  [`JobStatus.java#L22-L30`](https://github.com/AzBuilder/terrakube/blob/2a7ddfb1bfa1/api/src/main/java/io/terrakube/api/rs/job/JobStatus.java#L22-L30).
  Report 08's blocking and non-blocking run types need the same predicate
  [I].
- **Policy evaluation records per run and step**, with enforcement levels and
  owner overrides, as a model for Spacelift policy receipts (report 09) [S]
  [`PolicyEvaluationService.java`](https://github.com/AzBuilder/terrakube/blob/2a7ddfb1bfa1/api/src/main/java/io/terrakube/api/plugin/policy/PolicyEvaluationService.java).
- **Kubernetes Job controls** documented as settings: active deadline,
  backoff limit (set `0` for at-most-once), TTL after finish [D]
  [ephemeral agents](https://docs.terrakube.io/getting-started/deployment/ephemeral-agents).
  They map onto report 10's Kubernetes workers [I].
- **Operational runbooks in the repository**, such as the output delivery
  contract and zero-pending job reconciliation [S]
  [`docs/README.md`](https://github.com/AzBuilder/terrakube/blob/2a7ddfb1bfa1/docs/README.md).

### 4.4 Do not copy

- **Pushing work to an executor URL.** The API must reach the executor over
  HTTP [S]
  [`PersistentExecutorService.java#L93-L107`](https://github.com/AzBuilder/terrakube/blob/2a7ddfb1bfa1/api/src/main/java/io/terrakube/api/plugin/scheduler/job/tcl/executor/persistent/PersistentExecutorService.java#L93-L107),
  so a private agent needs inbound connectivity from the API [I]. Spacelift
  workers need none (report 10, section 4.3).
- **A Quartz job per run that polls job state.** Each job repeats on a fixed
  interval to move its flow on [S]
  [`ScheduleJobService.java#L85-L96`](https://github.com/AzBuilder/terrakube/blob/2a7ddfb1bfa1/api/src/main/java/io/terrakube/api/plugin/scheduler/ScheduleJobService.java#L85-L96),
  which adds latency and database load proportional to active runs [I].
- **The OPA binary as a subprocess** per evaluation [S]
  [`OpaExecutorServiceImpl.java#L238-L260`](https://github.com/AzBuilder/terrakube/blob/2a7ddfb1bfa1/executor/src/main/java/io/terrakube/executor/service/opa/OpaExecutorServiceImpl.java#L238-L260);
  see section 9 for the embedded API.
- **Drift as a user-assembled recipe.** Spacelift drift detection is a
  per-stack integration with schedule, reconcile and pool limits (report 18,
  section 3).
- **Boolean-per-capability teams.** Spacelift has roles with actions bound
  to spaces (report 14) [I].

## 5. Digger (OpenTaco)

| Item | Value |
|---|---|
| Repository | <https://github.com/diggerhq/digger> at `7c237c093d72` |
| Licence, language | MIT at the root, Apache-2.0 in `backend/`, **proprietary "Digger Enterprise license" in `ee/`**; Go |
| Latest release | v0.6.154, 2026-10-07 |
| Docs | <https://docs.digger.dev/> redirects to <https://docs.opentaco.dev/>, which presents Digger as rebranded to OpenTaco [D] [introduction](https://docs.opentaco.dev/readme/introduction) |

### 5.1 Architecture

| Concern | Digger | Evidence |
|---|---|---|
| Processes | An orchestrator backend (gin) that receives GitHub App webhooks and serves policies and job status; a drift service; OpenTaco `statesman` (state service) and a token service; the `digger` CLI that runs inside CI | [S] [`backend/bootstrap/main.go#L136-L229`](https://github.com/diggerhq/digger/blob/7c237c093d72/backend/bootstrap/main.go#L136-L229), [`drift/`](https://github.com/diggerhq/digger/tree/7c237c093d72/drift), [`taco/cmd`](https://github.com/diggerhq/digger/tree/7c237c093d72/taco/cmd) |
| Persistence | Postgres for the backend, with Atlas migrations | [S] [`backend/atlas.hcl`](https://github.com/diggerhq/digger/blob/7c237c093d72/backend/atlas.hcl), [`backend/migrations`](https://github.com/diggerhq/digger/tree/7c237c093d72/backend/migrations) |
| Queueing | The webhook handler validates the payload and processes it in a goroutine | [S] [`controllers/github.go#L36`](https://github.com/diggerhq/digger/blob/7c237c093d72/backend/controllers/github.go#L36), [`#L70`](https://github.com/diggerhq/digger/blob/7c237c093d72/backend/controllers/github.go#L70) |
| How runs execute | The backend triggers the customer's CI: a `CiBackend` interface with `TriggerWorkflow` and `GetWorkflowUrl`; GitHub Actions uses `workflow_dispatch`; GitLab, Bitbucket Pipelines and Buildkite are in `ee/` | [S] [`ci_backends.go`](https://github.com/diggerhq/digger/blob/7c237c093d72/backend/ci_backends/ci_backends.go), [`github_actions.go#L19-L35`](https://github.com/diggerhq/digger/blob/7c237c093d72/backend/ci_backends/github_actions.go#L19-L35), [`ee/backend/ci_backends`](https://github.com/diggerhq/digger/tree/7c237c093d72/ee/backend/ci_backends) |
| Worker model | The CI runner is the worker; jobs report status back through `POST /repos/:repo/projects/:projectName/jobs/:jobId/set-status` | [S] [`main.go#L213`](https://github.com/diggerhq/digger/blob/7c237c093d72/backend/bootstrap/main.go#L213) |
| Locking | A `Lock` interface (`Lock`, `Unlock`, `GetLock`) with DynamoDB, Azure and GCP implementations, plus backend locks | [S] [`libs/locking/core.go#L3-L14`](https://github.com/diggerhq/digger/blob/7c237c093d72/libs/locking/core.go#L3-L14), [`libs/locking/aws`](https://github.com/diggerhq/digger/tree/7c237c093d72/libs/locking/aws) |
| State backend | OpenTaco statesman speaks Terraform's `http` backend, including the `LOCK` and `UNLOCK` methods and the `X-Terraform-Lock-ID` header or `ID` query parameter, over a `UnitStore` with memory and S3 implementations | [S] [`backend/handler.go#L122-L139`](https://github.com/diggerhq/digger/blob/7c237c093d72/taco/internal/backend/handler.go#L122-L139), [`#L181-L195`](https://github.com/diggerhq/digger/blob/7c237c093d72/taco/internal/backend/handler.go#L181-L195), [`storage/interface.go#L50-L66`](https://github.com/diggerhq/digger/blob/7c237c093d72/taco/internal/storage/interface.go#L50-L66) |
| Policy engine | OPA embedded with `rego.New(rego.Query("data.digger.allow"), ...)`; access, plan, apply and drift policies fetched from the backend per organisation or per project | [S] [`libs/policy/policy.go#L14`](https://github.com/diggerhq/digger/blob/7c237c093d72/libs/policy/policy.go#L14), [`#L504-L514`](https://github.com/diggerhq/digger/blob/7c237c093d72/libs/policy/policy.go#L504-L514), [`main.go#L202-L209`](https://github.com/diggerhq/digger/blob/7c237c093d72/backend/bootstrap/main.go#L202-L209) |
| VCS integration | GitHub App in the open part; other providers partly in `ee/` | [S] [`ee/backend/providers`](https://github.com/diggerhq/digger/tree/7c237c093d72/ee/backend/providers) |
| Drift detection | Scheduled checks with Slack or GitHub Issues notifications | [D] [introduction](https://docs.opentaco.dev/readme/introduction); [S] [`drift/`](https://github.com/diggerhq/digger/tree/7c237c093d72/drift) |

### 5.2 Spacelift coverage

| Spacelift feature | Digger | Note |
|---|---|---|
| PR plans and applies | Covered | In the customer's CI |
| Policies | Partly | Access, plan, apply and drift Rego policies |
| Private workers | Covered by design | CI runners |
| Managed state | Covered | OpenTaco statesman |
| Drift detection | Covered | |
| Module registry, spaces, contexts | Not covered | |

### 5.3 Borrow

- **The `http` backend lock contract** as implemented by statesman: `LOCK`
  and `UNLOCK` methods, the lock ID taken from a header or a query
  parameter, and a store that rejects writes under a different lock [S]
  [`handler.go#L122-L139`](https://github.com/diggerhq/digger/blob/7c237c093d72/taco/internal/backend/handler.go#L122-L139),
  [`interface.go#L50-L66`](https://github.com/diggerhq/digger/blob/7c237c093d72/taco/internal/storage/interface.go#L50-L66).
  Report 16 records that Spacelift's managed state speaks the same protocol
  and leaves its lock semantics undocumented.
- **A narrow interface for "start this job somewhere else"** (`CiBackend`
  with two methods) [S]
  [`ci_backends.go`](https://github.com/diggerhq/digger/blob/7c237c093d72/backend/ci_backends/ci_backends.go).
- **Policy lookup at two levels**, organisation and project, as plain HTTP
  reads the runner makes [S]
  [`main.go#L202-L209`](https://github.com/diggerhq/digger/blob/7c237c093d72/backend/bootstrap/main.go#L202-L209).

### 5.4 Do not copy

- **Anything under `ee/`**, which is proprietary (section 1).
- **The deprecated OPA import path.** `libs/policy` imports
  `github.com/open-policy-agent/opa/rego` with OPA v1.4.0 [S]
  [`policy.go#L14`](https://github.com/diggerhq/digger/blob/7c237c093d72/libs/policy/policy.go#L14),
  [`libs/go.mod#L39`](https://github.com/diggerhq/digger/blob/7c237c093d72/libs/go.mod#L39);
  OPA marks that package as a v0 transition path (section 9).
- **CI as the executor.** Runs inherit CI quotas, logs and secrets, and the
  platform cannot enforce run isolation, runner images or worker-side
  policies (report 10) [I].
- **Goroutine webhook processing**, for the reason given under Atlantis.

## 6. Burrito

| Item | Value |
|---|---|
| Repository | <https://github.com/padok-team/burrito> at `ed21360c45e6` |
| Licence, language | Apache-2.0; Go, with a TypeScript UI |
| Latest release | v0.15.0, 2026-10-23 |
| Docs | <https://docs.burrito.tf/latest/> |

### 6.1 Architecture

| Concern | Burrito | Evidence |
|---|---|---|
| Processes | Kubernetes controllers for `TerraformRepository`, `TerraformLayer`, `TerraformRun` and `TerraformPullRequest`; a server that receives Git webhooks and serves the UI; a datastore; runner pods | [D] [architecture](https://docs.burrito.tf/latest/operator-manual/architecture/); [S] [`api/v1alpha1`](https://github.com/padok-team/burrito/tree/ed21360c45e6/api/v1alpha1), [`internal/controllers`](https://github.com/padok-team/burrito/tree/ed21360c45e6/internal/controllers) |
| Persistence | Kubernetes resources, conditions and annotations; the datastore (an HTTP proxy over S3, GCS or Azure, with optional encryption) holds plans, logs and Git bundles | [D] [architecture](https://docs.burrito.tf/latest/operator-manual/architecture/); [S] [`datastore/storage`](https://github.com/padok-team/burrito/tree/ed21360c45e6/internal/datastore/storage) |
| Queueing | Controller reconciliation; the layer controller picks a state from seven conditions | [S] [`terraformlayer/states.go#L24-L56`](https://github.com/padok-team/burrito/blob/ed21360c45e6/internal/controllers/terraformlayer/states.go#L24-L56) |
| Locking | A `coordination.k8s.io` Lease per layer | [S] [`lock/lock.go#L24-L68`](https://github.com/padok-team/burrito/blob/ed21360c45e6/internal/lock/lock.go#L24-L68) |
| How runs execute | A runner pod per run uses `tenv` to fetch the IaC binary, pulls the bundle from the datastore and uploads plan and logs | [D] [architecture](https://docs.burrito.tf/latest/operator-manual/architecture/) |
| State backend | None of its own; the datastore keeps plans, logs and bundles only, so state stays in each layer's own backend | [D] [architecture](https://docs.burrito.tf/latest/operator-manual/architecture/); [I] from the datastore's scope |
| Policy engine | None | [S] no Rego code |
| VCS integration | Webhooks on push and PR events, with repository polling as a fallback; commit statuses per layer | [S] [`internal/webhook/event`](https://github.com/padok-team/burrito/tree/ed21360c45e6/internal/webhook/event), [`polling.go`](https://github.com/padok-team/burrito/blob/ed21360c45e6/internal/controllers/terraformrepository/polling.go) |
| Drift detection | Every layer is re-planned when its last plan is older than `driftDetection` (default 20 minutes); a remediation strategy with `autoApply` decides whether an apply follows | [S] [`config.go#L89`](https://github.com/padok-team/burrito/blob/ed21360c45e6/internal/burrito/config/config.go#L89), [`#L252`](https://github.com/padok-team/burrito/blob/ed21360c45e6/internal/burrito/config/config.go#L252), [`common.go#L47-L53`](https://github.com/padok-team/burrito/blob/ed21360c45e6/api/v1alpha1/common.go#L47-L53) |
| Scheduling | Sync windows: `allow` or `deny`, per action, with a cron `schedule`, a `duration` and layer selectors | [S] [`terraformrepository_types.go#L54-L67`](https://github.com/padok-team/burrito/blob/ed21360c45e6/api/v1alpha1/terraformrepository_types.go#L54-L67) |
| Multi-tenancy | Kubernetes namespaces and RBAC | [I] from the CRD-based design |

### 6.2 Spacelift coverage

| Spacelift feature | Burrito | Note |
|---|---|---|
| Tracked runs on push | Covered | `ApplyNeeded` after a plan on a new commit |
| Proposed runs on PRs | Covered | `TerraformPullRequest` |
| Drift detection with reconcile | Covered | Interval-based, not cron |
| Retries | Covered | Exponential backoff, `MaxRetriesReached` state |
| Policies, state, registry, spaces | Not covered | |

### 6.3 Borrow

- **State selection from named conditions.** The layer controller computes
  conditions such as `IsRunning`, `IsLastPlanTooOld` and `IsApplyUpToDate`,
  stores them on the resource, and chooses the next state from them in one
  switch [S]
  [`states.go#L24-L56`](https://github.com/padok-team/burrito/blob/ed21360c45e6/internal/controllers/terraformlayer/states.go#L24-L56).
  The conditions double as an explanation of why a stack is or is not
  running [I].
- **Commit statuses only on new commits.** A re-plan of a commit already
  planned is drift detection, so it posts no commit status, which keeps
  checks from flapping and stays under the provider's per-commit status cap
  [S]
  [`states.go#L85-L102`](https://github.com/padok-team/burrito/blob/ed21360c45e6/internal/controllers/terraformlayer/states.go#L85-L102).
- **Sync windows** (allow or deny, per action, cron plus duration) as a
  shape for blackout windows on applies [S]
  [`terraformrepository_types.go#L54-L67`](https://github.com/padok-team/burrito/blob/ed21360c45e6/api/v1alpha1/terraformrepository_types.go#L54-L67).
  Spacelift has no equivalent beyond policies and stack locks [I] from
  reports 08 and 18.
- **Repository-then-layer defaults** resolved by one helper per setting
  (`chooseBool(repo, layer, default)`) [S]
  [`common.go#L138-L142`](https://github.com/padok-team/burrito/blob/ed21360c45e6/api/v1alpha1/common.go#L138-L142).
- **Encryption in the artefact store** for plans and logs [S]
  [`storage/encryption.go`](https://github.com/padok-team/burrito/blob/ed21360c45e6/internal/datastore/storage/encryption.go).

### 6.4 Do not copy

- **Kubernetes as the database.** Status in annotations and Leases as locks
  tie the product to one cluster's API server; Spacelift's self-hosted
  topology is Postgres plus object storage (report 24) [I].
- **Interval drift without a schedule or a pool limit.** Spacelift drift
  uses cron schedules, a timezone and a per-pool concurrency cap (report 18,
  section 3) [I].

## 7. Terramate

| Item | Value |
|---|---|
| Repository | <https://github.com/terramate-io/terramate> at `1f2f2d72a5c1` |
| Licence, language | **MPL-2.0** (patterns only); Go |
| Latest release | v0.17.3, 2026-09-08 |
| Docs | <https://terramate.io/docs/> |

### 7.1 Architecture

| Concern | Terramate | Evidence |
|---|---|---|
| Processes | A CLI; Terramate Cloud is a separate hosted service the CLI reports to | [S] [`cloud/client.go`](https://github.com/terramate-io/terramate/blob/1f2f2d72a5c1/cloud/client.go); [D] [docs index](https://terramate.io/docs/) |
| Persistence | None in the CLI; configuration is HCL in the repository | [S] [`config/stack.go`](https://github.com/terramate-io/terramate/blob/1f2f2d72a5c1/config/stack.go) |
| Ordering | Stacks declare `after`, `before`, `wants`, `wanted_by`, `tags` and `watch`; a DAG gives the run order, ties broken lexicographically | [S] [`config/stack.go#L35-L54`](https://github.com/terramate-io/terramate/blob/1f2f2d72a5c1/config/stack.go#L35-L54), [`run/order.go#L22-L52`](https://github.com/terramate-io/terramate/blob/1f2f2d72a5c1/run/order.go#L22-L52) |
| Change detection | Git comparison against a base branch; uncommitted and untracked files count; changed local modules mark their callers; dependency filters such as `--include-all-dependencies` | [S] [`stack/manager.go#L122-L128`](https://github.com/terramate-io/terramate/blob/1f2f2d72a5c1/stack/manager.go#L122-L128); [D] [change detection](https://terramate.io/docs/cli/change-detection/) |
| How runs execute | `terramate run` executes a command in each selected stack, sequentially or in parallel | [S] [`scheduler/`](https://github.com/terramate-io/terramate/tree/1f2f2d72a5c1/scheduler) |
| Worker model, state, policy | None of its own; it runs inside CI with the stacks' own backends | [D] [docs index](https://terramate.io/docs/) |
| Drift | A Terramate Cloud feature; the CLI reads drift status from the cloud API | [S] [`cloud/client.go#L257-L258`](https://github.com/terramate-io/terramate/blob/1f2f2d72a5c1/cloud/client.go#L257-L258); [D] [docs index](https://terramate.io/docs/) |

### 7.2 Spacelift coverage

| Spacelift feature | Terramate | Note |
|---|---|---|
| Stack dependencies, ordering | Covered | Order-only and data (output sharing) dependencies |
| Project globs, change detection | Covered | Git-based |
| Runs, workers, state, policies | Not covered | CI does the running |
| Drift, dashboards | Commercial cloud only | |

### 7.3 Borrow (patterns only, MPL-2.0)

- **Order-only and data dependencies kept apart.** Ordering (`after`,
  `before`) does not widen a selection, while data dependencies (output
  sharing) do [D]
  [change detection](https://terramate.io/docs/cli/change-detection/).
  Report 18's stack dependencies combine both in one edge with output
  references [I].
- **Deterministic tie-breaking** in the run order [S]
  [`order.go#L22-L24`](https://github.com/terramate-io/terramate/blob/1f2f2d72a5c1/run/order.go#L22-L24).
- **Changed local modules mark callers as changed**, a reference for
  Spacelift's project globs and module-triggered runs (reports 07 and 11)
  [D] [change detection](https://terramate.io/docs/cli/change-detection/).

### 7.4 Do not copy

- **Any code**, because of MPL-2.0.
- **Features that exist only in the commercial cloud** cannot be studied
  beyond their documentation.

## 8. Flux tofu-controller

| Item | Value |
|---|---|
| Repository | <https://github.com/flux-iac/tofu-controller> at `e2db61bdd264` |
| Licence, language | Apache-2.0; Go |
| Latest release | v0.16.6, 2026-10-26 |
| Docs | <https://flux-iac.github.io/tofu-controller/> |

### 8.1 Architecture

| Concern | tofu-controller | Evidence |
|---|---|---|
| Processes | A Flux controller reconciling `Terraform` resources (`infra.contrib.fluxcd.io/v1alpha2`); runner pods per run; an optional branch planner | [D] [overview](https://flux-iac.github.io/tofu-controller/); [S] [`terraform_types.go`](https://github.com/flux-iac/tofu-controller/blob/e2db61bdd264/api/v1alpha2/terraform_types.go) |
| Source | Flux source-controller artefacts, not a VCS integration of its own | [D] [overview](https://flux-iac.github.io/tofu-controller/) |
| Worker protocol | The controller dials the runner pod's IP over gRPC with mTLS and drives it step by step: `UploadAndExtract`, `WriteBackendConfig`, `Init`, `Plan`, `SaveTFPlan`, `Apply`, `Destroy`, `Output`, `ForceUnlock` and others | [S] [`runner.proto#L7-L41`](https://github.com/flux-iac/tofu-controller/blob/e2db61bdd264/runner/runner.proto#L7-L41), [`runner_pod.go#L127-L152`](https://github.com/flux-iac/tofu-controller/blob/e2db61bdd264/controllers/runner_pod.go#L127-L152), [`#L498-L499`](https://github.com/flux-iac/tofu-controller/blob/e2db61bdd264/controllers/runner_pod.go#L498-L499) |
| Certificates | A rotator issues and renews per-namespace TLS material for runners | [S] [`mtls/rotator.go#L73-L124`](https://github.com/flux-iac/tofu-controller/blob/e2db61bdd264/mtls/rotator.go#L73-L124) |
| State backend | Default `kubernetes` backend: state in a Secret named with a `secret_suffix` | [S] [`setup.go#L356-L358`](https://github.com/flux-iac/tofu-controller/blob/e2db61bdd264/controllers/setup.go#L356-L358); [D] [overview](https://flux-iac.github.io/tofu-controller/) |
| Plan approval | `spec.approvePlan`: `auto`, or the ID of a plan to apply | [S] [`terraform_types.go#L96-L99`](https://github.com/flux-iac/tofu-controller/blob/e2db61bdd264/api/v1alpha2/terraform_types.go#L96-L99); [D] [overview](https://flux-iac.github.io/tofu-controller/) |
| Drift detection | On by default at each `spec.interval`; `disableDriftDetection` turns it off; with `approvePlan: auto` drift is fixed automatically | [S] [`terraform_types.go#L144`](https://github.com/flux-iac/tofu-controller/blob/e2db61bdd264/api/v1alpha2/terraform_types.go#L144), [`#L197`](https://github.com/flux-iac/tofu-controller/blob/e2db61bdd264/api/v1alpha2/terraform_types.go#L197), [`drift.go`](https://github.com/flux-iac/tofu-controller/blob/e2db61bdd264/controllers/drift.go) |
| Retries | `retryInterval`, with a static or exponential-backoff strategy capped by `maxRetryInterval` | [S] [`terraform_types.go#L149-L164`](https://github.com/flux-iac/tofu-controller/blob/e2db61bdd264/api/v1alpha2/terraform_types.go#L149-L164) |
| Dependencies, outputs | `dependsOn` other `Terraform` objects; `writeOutputsToSecret` | [S] [`terraform_types.go#L61-L62`](https://github.com/flux-iac/tofu-controller/blob/e2db61bdd264/api/v1alpha2/terraform_types.go#L61-L62), [`#L191`](https://github.com/flux-iac/tofu-controller/blob/e2db61bdd264/api/v1alpha2/terraform_types.go#L191), [`#L277`](https://github.com/flux-iac/tofu-controller/blob/e2db61bdd264/api/v1alpha2/terraform_types.go#L277) |
| PR plans | Branch planner polls the VCS API instead of receiving webhooks, so the cluster needs no ingress | [S] [ADR 0001](https://github.com/flux-iac/tofu-controller/blob/e2db61bdd264/docs/adr/0001-pr-polling-workflow.md) |
| Multi-tenancy | Runner pods in the object's namespace with its service account; cross-namespace references denied by default (`enable-cross-namespace-refs`) | [D] [overview](https://flux-iac.github.io/tofu-controller/); [S] [ADR 0002](https://github.com/flux-iac/tofu-controller/blob/e2db61bdd264/docs/adr/0002-deny-cross-ns-by-default.md) |
| Policy engine, registry | None | [S] |

### 8.2 Spacelift coverage

| Spacelift feature | tofu-controller | Note |
|---|---|---|
| Tracked runs with confirm | Covered | Plan ID approval |
| Drift detection and reconcile | Covered | Interval-based |
| Stack dependencies, outputs | Covered | `dependsOn`, outputs to Secrets |
| Kubernetes workers | Covered | Runner pods |
| PR plans | Partly | Branch planner, technology preview |
| Policies, registry, spaces, managed state service | Not covered | |

### 8.3 Borrow

- **Approve a specific plan by ID.** Apply proceeds only when the approval
  names the plan that was produced, so a newer plan cannot be applied under
  an older approval [S]
  [`terraform_types.go#L96-L99`](https://github.com/flux-iac/tofu-controller/blob/e2db61bdd264/api/v1alpha2/terraform_types.go#L96-L99).
  Spacelift's `UNCONFIRMED` confirm and targeted replan (report 08) need the
  same guard [I].
- **Retry policy fields** (static or exponential, with a cap) as a shape for
  autoretry settings [S]
  [`terraform_types.go#L149-L164`](https://github.com/flux-iac/tofu-controller/blob/e2db61bdd264/api/v1alpha2/terraform_types.go#L149-L164).
- **Deny cross-tenant references by default** with an opt-in flag whose
  default means the safe thing [S]
  [ADR 0002](https://github.com/flux-iac/tofu-controller/blob/e2db61bdd264/docs/adr/0002-deny-cross-ns-by-default.md).
  Report 14's space inheritance needs the same default [I].
- **Short decision records in the repository** for choices such as polling
  over webhooks [S]
  [`docs/adr`](https://github.com/flux-iac/tofu-controller/tree/e2db61bdd264/docs/adr).

### 8.4 Do not copy

- **Controller-to-runner RPC.** The controller must reach each runner pod's
  IP [S]
  [`runner_pod.go#L498-L499`](https://github.com/flux-iac/tofu-controller/blob/e2db61bdd264/controllers/runner_pod.go#L498-L499),
  which works inside one cluster but not for private workers behind NAT;
  Spacelift's workers initiate every connection (report 10) [I].
- **A chatty step-by-step RPC surface** where the controller orchestrates
  each Terraform call; Spacelift hands a whole run to the worker, which
  reports state changes back (report 10, section 1) [I].
- **State in Kubernetes Secrets** by default, which has a size limit and
  couples state to the cluster [I] from
  [`setup.go#L356-L358`](https://github.com/flux-iac/tofu-controller/blob/e2db61bdd264/controllers/setup.go#L356-L358).

## 9. OPA as the embedded policy engine

| Item | Value |
|---|---|
| Repository | <https://github.com/open-policy-agent/opa> at `359db769deb0` |
| Licence, language | Apache-2.0; Go (`go 1.26.0` in `go.mod`) |
| Latest release | v1.21.4, 2026-10-23 |
| Docs | <https://www.openpolicyagent.org/docs/> |

Spacelift evaluates Rego with OPA, reports the version through
`policyRuntime { openPolicyAgentVersion }`, supports `REGO_V0` and `REGO_V1`,
disables five built-ins, and applies per-type timeouts (report 09, section 2).

### 9.1 The Go API

| Need | API at the commit | Evidence |
|---|---|---|
| Import path | `github.com/open-policy-agent/opa/v1/rego` and `.../v1/sdk`; the root `rego` package is a v0 transition path that is deprecated | [S] [`rego/rego.go#L5-L7`](https://github.com/open-policy-agent/opa/blob/359db769deb0/rego/rego.go#L5-L7); [D] [integration](https://www.openpolicyagent.org/docs/integration) |
| Build a query | `rego.New(opts...)` with `rego.Query`, `rego.Module`, `rego.Store` | [S] [`v1/rego/rego.go#L954`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/rego/rego.go#L954), [`#L1071`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/rego/rego.go#L1071), [`#L1143`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/rego/rego.go#L1143), [`#L1456`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/rego/rego.go#L1456) |
| Compile once | `PrepareForEval(ctx)` returns a `PreparedEvalQuery` that is safe to share across goroutines; `Eval(ctx, rego.EvalInput(input))` per decision | [S] [`v1/rego/rego.go#L1824`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/rego/rego.go#L1824), [`#L587`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/rego/rego.go#L587), [`#L220`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/rego/rego.go#L220); [D] [integration](https://www.openpolicyagent.org/docs/integration) |
| Rego version per module | `rego.SetRegoVersion(ast.RegoVersion)`; versions `RegoV0`, `RegoV0CompatV1`, `RegoV1`; `DefaultRegoVersion = RegoV1` | [S] [`v1/rego/rego.go#L1426`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/rego/rego.go#L1426), [`v1/ast/rego_version.go#L5-L31`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/ast/rego_version.go#L5-L31) |
| Rego v2 | `ast.RegoV2` (adds `and`, `or` and `not` bodies) is on `main` under "Unreleased", not in v1.21.4 | [S] [`CHANGELOG.md#L6-L37`](https://github.com/open-policy-agent/opa/blob/359db769deb0/CHANGELOG.md#L6-L37), [`rego_version.go#L24-L27`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/ast/rego_version.go#L24-L27) |
| Restrict built-ins | `rego.Capabilities(*ast.Capabilities)`; `ast.CapabilitiesForThisVersion()` gives the full set to filter; `AllowNet` limits `http.send` hosts | [S] [`v1/rego/rego.go#L1372`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/rego/rego.go#L1372), [`v1/ast/capabilities.go#L104-L120`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/ast/capabilities.go#L104-L120), [`#L170`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/ast/capabilities.go#L170) |
| Strictness | `rego.Strict(true)`, `rego.StrictBuiltinErrors(true)` | [S] [`v1/rego/rego.go#L1420`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/rego/rego.go#L1420), [`#L1323`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/rego/rego.go#L1323) |
| `print` output | `rego.EnablePrintStatements`, `rego.PrintHook` | [S] [`v1/rego/rego.go#L1396-L1413`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/rego/rego.go#L1396-L1413) |
| Timeouts | Evaluation honours context cancellation; `topdown.NewCancel` for explicit cancellation | [S] [`v1/topdown/cancel.go#L22-L27`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/topdown/cancel.go#L22-L27) |
| Managed engine | `sdk.New` plus `(*OPA).Decision(ctx, DecisionOptions{Path, Input, Now, DecisionID, StrictBuiltinErrors, ...})` brings bundles, decision logs and status | [S] [`v1/sdk/opa.go#L66`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/sdk/opa.go#L66), [`#L302`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/sdk/opa.go#L302), [`#L362-L381`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/sdk/opa.go#L362-L381); [D] [integration](https://www.openpolicyagent.org/docs/integration) |

### 9.2 Bundles and decision logs

| Feature | Behaviour | Evidence |
|---|---|---|
| Bundles | Policy and data packaged as a tarball, polled from a service or read from `file://`; manual trigger available; manifests may declare a Rego version | [D] [integration](https://www.openpolicyagent.org/docs/integration); [S] [`CHANGELOG.md#L30-L37`](https://github.com/open-policy-agent/opa/blob/359db769deb0/CHANGELOG.md#L30-L37) |
| Bundle signing | `.signatures.json` holds exactly one JWT that lists file hashes; verification checks each file | [S] [`v1/bundle/verify.go#L70-L102`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/bundle/verify.go#L70-L102), [`#L211`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/bundle/verify.go#L211) |
| Decision log fields | `decision_id`, `trace_id`, `span_id`, `labels`, `req_id`, `path`, `query`, `bundles`, `input`, `result`, `requested_by`, `timestamp`, `metrics`, `nd_builtin_cache`, `erased`, `masked` | [D] [decision logs](https://www.openpolicyagent.org/docs/management-decision-logs) |
| Masking and dropping | A `data.system.log.mask` policy returns JSON pointers under `/input`, `/result` or `/nd_builtin_cache` to erase or upsert; a `drop` rule suppresses whole events | [D] [decision logs](https://www.openpolicyagent.org/docs/management-decision-logs); [S] [`v1/plugins/logs/mask.go#L18-L31`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/plugins/logs/mask.go#L18-L31) |
| Upload | Gzipped JSON batches by POST, under a size limit, requeued with backoff on failure; a rate cap drops excess events; console output needs no server | [D] [decision logs](https://www.openpolicyagent.org/docs/management-decision-logs) |

### 9.3 Borrow

- **Prepared queries per policy and engine version.** Compile each policy
  once with its `SetRegoVersion` and reuse the prepared query across
  evaluations; this is how report 09's per-policy `engineType` can be
  honoured without reparsing [S]
  [`v1/rego/rego.go#L1426`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/rego/rego.go#L1426),
  [`#L1824`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/rego/rego.go#L1824) [I].
- **Capabilities to disable built-ins.** Removing `http.send`,
  `opa.runtime`, `rego.parse_module`, `time.now_ns` and `trace` from the
  capability set makes their use a compile error, which matches Spacelift's
  rejection at save time (report 09, section 2.2) [S]
  [`capabilities.go#L104-L170`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/ast/capabilities.go#L104-L170) [I].
- **Context deadlines for timeouts**, one per evaluation and one per event,
  for report 09's per-type and combined limits [S]
  [`cancel.go`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/topdown/cancel.go) [I].
- **The decision log event shape and masking rules** as the model for
  Spacelift's policy receipts and sampled evaluations with sensitive values
  removed (report 09, section 5.3 and 6) [D]
  [decision logs](https://www.openpolicyagent.org/docs/management-decision-logs) [I].
- **`DecisionOptions.Now`** to pin the clock, since Spacelift disables
  `time.now_ns` and passes the timestamp in the input [S]
  [`opa.go#L363`](https://github.com/open-policy-agent/opa/blob/359db769deb0/v1/sdk/opa.go#L363).

### 9.4 Do not copy

- **The root `rego` package** (deprecated v0 path), as Digger does
  (section 5.4).
- **Running the `opa` binary per evaluation**, as Atlantis (through conftest)
  and Terrakube do.
- **Bundle polling as the policy distribution path.** Spacelift policies are
  API objects attached to stacks (report 09, section 7), so the policies to
  evaluate are known from the database at evaluation time [I].
- **Rego v2 before it is released** [S]
  [`CHANGELOG.md#L6`](https://github.com/open-policy-agent/opa/blob/359db769deb0/CHANGELOG.md#L6).

## 10. Work-queue and workflow engines

Spacelift self-hosted runs eight logical queues on SQS or on Postgres, with
visibility timeouts, a maximum receive count of 3, dead-letter queues, a FIFO
queue and per-queue drain concurrency, and fires cron from the drain
(report 24, section 3.3). Run priority within a pool has seven tiers (report
08, section 4). Temporal and River are the two candidates the ticket names.

### 10.1 Temporal

| Item | Value |
|---|---|
| Repository | <https://github.com/temporalio/temporal> at `41d9f3b60536` |
| Licence, language | MIT; Go |
| Latest release | v1.32.1, 2026-10-08 (marked latest; patch releases of 1.30 and 1.31 shipped 2026-10-12) |
| Docs | <https://docs.temporal.io/> |

| Concern | Temporal | Evidence |
|---|---|---|
| Processes | Four server services: frontend (stateless gateway with rate limiting and authorization), history (persists workflow state and schedules tasks), matching (hosts task queues and matches workers to tasks), worker (internal system workflows) | [D] [Temporal Server](https://docs.temporal.io/temporal-service/temporal-server); [S] [`service/`](https://github.com/temporalio/temporal/tree/41d9f3b60536/service) |
| Persistence | Schemas for Cassandra, MySQL 8, PostgreSQL 12 and SQLite, plus Elasticsearch for visibility | [S] [`schema/`](https://github.com/temporalio/temporal/tree/41d9f3b60536/schema) |
| Worker model | Workers long-poll task queues (`PollWorkflowTaskQueue`, `PollActivityTaskQueue`) through the frontend | [S] [`matchingservice/v1/service.proto#L18-L24`](https://github.com/temporalio/temporal/blob/41d9f3b60536/proto/internal/temporal/server/api/matchingservice/v1/service.proto#L18-L24); [D] [Temporal Server](https://docs.temporal.io/temporal-service/temporal-server) |
| Programming model | Deterministic workflow code replayed from an event history; side effects in activities with retries and heartbeats | [D] [Temporal docs](https://docs.temporal.io/) |
| Testing | `temporaltest.NewServer` runs a server on loopback for end-to-end tests | [S] [`temporaltest/server.go#L19-L50`](https://github.com/temporalio/temporal/blob/41d9f3b60536/temporaltest/server.go#L19-L50) |

**Borrow.**
- The worker contract: long-poll a named queue, heartbeat while working,
  and let the server time out and reassign work whose heartbeat stops. It is
  the same shape as Spacelift's long-poll transport and stale-run cleanup
  (report 10) [I].
- Durable, step-by-step run orchestration where each phase transition is an
  event in a history, which fits report 08's 43 transitions and the paused
  run that resumes in a new container [I].
- An in-process test server for acceptance tests against a real engine [S]
  [`temporaltest/server.go`](https://github.com/temporalio/temporal/blob/41d9f3b60536/temporaltest/server.go).

**Do not copy.**
- **A second stateful cluster.** Self-hosting Temporal adds four services
  and its own database schema beside infraweave's Postgres [S]
  [`service/`](https://github.com/temporalio/temporal/tree/41d9f3b60536/service),
  [`schema/`](https://github.com/temporalio/temporal/tree/41d9f3b60536/schema);
  report 24 shows Spacelift's self-hosted product with Postgres and object
  storage as the only hard dependencies [I].
- **Run state owned by the engine.** Spacelift's run states are API data
  (`RunState`, report 08); keeping them in an event history the API cannot
  query directly would need a projection [I].
- **Determinism constraints in product code.** Workflow code must replay
  identically, so changing run logic needs versioning of in-flight
  workflows [D] [Temporal docs](https://docs.temporal.io/) *(unverified:
  the versioning page was not read)*.

### 10.2 River

| Item | Value |
|---|---|
| Repository | <https://github.com/riverqueue/river> at `fc6c1d57cc72` |
| Licence, language | **MPL-2.0**; Go (with Java, JavaScript, Ruby and Rust clients in the repository) |
| Latest release | v0.50.0, published 2026-10-27 UTC; its changelog entry, which the commit read carries, is dated 2026-10-26 |
| Docs | <https://riverqueue.com/docs> |

| Concern | River | Evidence |
|---|---|---|
| Storage | Postgres through `pgx` v5 or `database/sql`, or SQLite; one `river_job` table | [S] [`riverdriver/`](https://github.com/riverqueue/river/tree/fc6c1d57cc72/riverdriver) |
| Fetching | `JobGetAvailable` locks available jobs of one queue `FOR UPDATE SKIP LOCKED`, ordered by `priority`, `scheduled_at`, `id`, and marks them `running` | [S] [`river_job.sql#L203-L225`](https://github.com/riverqueue/river/blob/fc6c1d57cc72/riverdriver/riverpgxv5/internal/dbsqlc/river_job.sql#L203-L225) |
| Job states | `available`, `cancelled`, `completed`, `discarded`, `pending`, `retryable`, `running`, `scheduled` | [S] [`river_type.go#L166-L220`](https://github.com/riverqueue/river/blob/fc6c1d57cc72/rivertype/river_type.go#L166-L220) |
| Transactional insert | `InsertTx` and `InsertManyTx` enqueue in the caller's transaction, so the job exists only if the data it refers to commits | [S] [`client.go#L1942`](https://github.com/riverqueue/river/blob/fc6c1d57cc72/client.go#L1942), [`#L2009`](https://github.com/riverqueue/river/blob/fc6c1d57cc72/client.go#L2009); [D] [transactional enqueueing](https://riverqueue.com/docs/transactional-enqueueing) |
| Uniqueness | `UniqueOpts` (by args, period, queue, state) | [S] [`insert_opts.go#L102`](https://github.com/riverqueue/river/blob/fc6c1d57cc72/insert_opts.go#L102) |
| Periodic jobs | `NewPeriodicJob` with a cron or `PeriodicInterval` schedule, enqueued by the elected leader | [S] [`periodic_job.go#L74-L100`](https://github.com/riverqueue/river/blob/fc6c1d57cc72/periodic_job.go#L74-L100), [`internal/leadership/elector.go`](https://github.com/riverqueue/river/blob/fc6c1d57cc72/internal/leadership/elector.go) |
| Timeouts and rescue | `JobTimeout` defaults to 1 minute; the rescuer treats jobs running longer than `RescueStuckJobsAfter` (default 1 hour) as stuck | [S] [`client.go#L53`](https://github.com/riverqueue/river/blob/fc6c1d57cc72/client.go#L53), [`#L379-L397`](https://github.com/riverqueue/river/blob/fc6c1d57cc72/client.go#L379-L397), [`job_rescuer.go#L30`](https://github.com/riverqueue/river/blob/fc6c1d57cc72/internal/maintenance/job_rescuer.go#L30) |
| Pro-only features | Concurrency limits, dead letter queue, sequences (strict order), workflows (job DAGs), encrypted jobs and others | [D] [River Pro](https://riverqueue.com/docs/pro) |

**Borrow (patterns; any dependency decision is the plan's).**
- **`SKIP LOCKED` fetching ordered by priority then time**, which is the
  core of a Postgres queue with the seven-tier pool order of report 08
  [S]
  [`river_job.sql#L203-L225`](https://github.com/riverqueue/river/blob/fc6c1d57cc72/riverdriver/riverpgxv5/internal/dbsqlc/river_job.sql#L203-L225) [I].
- **Transactional enqueue**, so that creating a run and queueing its work
  commit together; it removes the lost-event failure of the goroutine
  dispatch in Atlantis and Digger [D]
  [transactional enqueueing](https://riverqueue.com/docs/transactional-enqueueing) [I].
- **Unique jobs** for Spacelift's event deduplication on commit SHA plus run
  type (report 08, section 4) [S]
  [`insert_opts.go#L102`](https://github.com/riverqueue/river/blob/fc6c1d57cc72/insert_opts.go#L102) [I].
- **Leader-elected maintenance** (rescuer, cleaner, periodic enqueuer) for
  cron firing and stale-run sweeps on multiple replicas [S]
  [`internal/maintenance`](https://github.com/riverqueue/river/tree/fc6c1d57cc72/internal/maintenance).
- **A test helper package** for asserting inserted jobs [S]
  [`rivertest/`](https://github.com/riverqueue/river/tree/fc6c1d57cc72/rivertest).

**Do not copy.**
- **Defaults sized for short jobs.** A 1-minute job timeout and a 1-hour
  rescue window do not fit runs that last hours or wait days for
  confirmation (report 08 timers) [S]
  [`client.go#L53`](https://github.com/riverqueue/river/blob/fc6c1d57cc72/client.go#L53) [I].
- **Queue jobs as the run.** Spacelift runs wait in `QUEUED`, `UNCONFIRMED`
  or `PENDING_REVIEW` for people and policies; a queue row is a delivery
  mechanism, not the run record [I] from report 08.
- **Relying on Pro features** (dead letter queue, strict ordering,
  concurrency limits) that Spacelift's queue semantics need (report 24,
  section 3.3), since they are proprietary [D]
  [River Pro](https://riverqueue.com/docs/pro).
- **Copying River code**, because of MPL-2.0.

## 11. Spacelift's own open-source pieces

Reports 10 and 24 describe these at their pinned commits; this table names
the pattern each shows and where it is recorded. Every one is MIT except
spacelift-intent (Apache-2.0), as report
[01](01-spacelift-landscape.md) lists.

| Repository | Pattern worth copying | Recorded in |
|---|---|---|
| ec2-workerpool-autoscaler | Drain before terminate: scale in only idle workers, oldest first, using `busy`, `drained` and `availableAt`, and undrain a worker that turned busy | [10, section 9](10-workers-and-execution.md#9-the-worker-pool-autoscaler) |
| signed-run-reference-implementation | A worker-side initialization policy that verifies a signature on the run before the worker executes it | [10, section 10](10-workers-and-execution.md#10-signed-runs-and-worker-side-policy) |
| local-worker-pool | A launcher plus a Docker-in-Docker sidecar in Compose, as a local worker for development and acceptance tests | [10, section 7.1](10-workers-and-execution.md#71-docker-based-workers) |
| spacelift-self-hosted-helm-chart and the Terraform modules | Two stateless processes from one binary (server, and a drain that also fires cron), a Postgres queue option, and readiness on a separate port | [24, section 2.1](24-self-hosted-reference.md#21-process-inventory), [24, section 3.3](24-self-hosted-reference.md#33-message-queues) |
| vcs-agent | An outbound gRPC tunnel that lets the service reach a VCS inside a private network | [11, section 14](11-vcs-integrations.md#14-vcs-agent-pools) |
| kmsjwt | JWT signing through a cloud KMS key, for OIDC tokens issued to runs | [12](12-cloud-integrations-and-oidc.md) |
| spacelift-policies-example-library | 67 policy templates and 61 tests as a parity corpus for the policy engine | [09, section 10](09-policies.md#10-the-example-library-at-6ff5edb08bc8) |
| prometheus-exporter | Metrics derived from the public API rather than from internals | [19](19-events-notifications-audit.md) |

## 12. Cross-tool comparison

| Concern | Best reference | Why |
|---|---|---|
| Run queue | OTF for the model; River for the mechanism | OTF separates a per-workspace run queue from a pool-wide phase queue, matching Spacelift's stack lock plus pool scheduler (section 3.3). River shows a Postgres queue with `SKIP LOCKED`, priority ordering, uniqueness and transactional insert (section 10.2), which Spacelift's self-hosted Postgres queue option needs (report 24) |
| Worker protocol | OTF | Worker-initiated HTTP long-poll for jobs and cancel signals, registration, heartbeats with timed status decay, and job-scoped tokens (section 3.1); Terrakube and tofu-controller push to the worker and need inbound reach |
| State backend | OpenTaco (Digger) statesman | A Terraform `http` backend with `LOCK`/`UNLOCK` and lock-ID checks over a pluggable store (section 5.1), the protocol Spacelift's managed state speaks (report 16); OTF is the reference if TFC's state API is also wanted |
| Policy engine embedding | OPA v1 Go API | `PrepareForEval`, `SetRegoVersion` for `REGO_V0` and `REGO_V1`, capabilities to remove built-ins, context timeouts (section 9); Digger shows embedding but on the deprecated path |
| VCS webhooks | Terrakube for durable intake; Atlantis for validators; OTF for hook lifecycle | Terrakube commits each delivery, claims it with a conditional update and fences the result (section 4.3); Atlantis has per-provider signature validators for six providers (section 2); OTF shares one webhook per repository and reconciles hooks (section 3). Atlantis and Digger show the failure to avoid: processing in a goroutine that outlives the request |
| Module registry | OTF | Registry protocol endpoints, publish on VCS tag, and signed download URLs (section 3.1); Terrakube's separate registry service also covers providers (section 4.1) |
| Multi-tenancy and RBAC | OTF for roles; tofu-controller for defaults | OTF's organization, team and workspace roles are the closest to Spacelift roles (section 3.1); tofu-controller's deny-cross-namespace default fits space isolation (section 8.3). None models Spacelift's space inheritance (report 14) |
| Drift detection | Burrito | Condition-driven re-plan, remediation strategy with `autoApply`, and no commit-status noise from drift plans (section 6.3); tofu-controller adds plan-ID approval for reconcile (section 8.3). Neither has Spacelift's cron schedules and pool limits (report 18) |
| Scheduling | Terrakube for cron at scale; River for leader-elected periodic jobs | Terrakube runs cron triggers on a clustered JDBC scheduler (section 4.3); River enqueues periodic jobs from one elected leader (section 10.2); Burrito's sync windows model blackout periods (section 6.3) |

## What this means for infraweave

- No reference tool covers Spacelift's whole surface. The closest
  architecture is OTF (Postgres only, server plus long-polling agents), and
  the plan must say which of its patterns infraweave adopts and how it
  reaches parity where OTF has nothing: policies, spaces, drift, schedules
  and stack dependencies.
- Licence limits what can be reused: OTF, Terramate and River are MPL-2.0,
  Digger `ee/` is proprietary, and River Pro, Terramate Cloud and Temporal
  Cloud are services. The plan must record, per dependency, whether infraweave
  links it, and must keep MPL-2.0 code out of its own files.
- A queue and workflow engine choice is open: an in-repository Postgres
  queue, River as a dependency, or Temporal as a separate cluster. Each must
  meet report 24's queue semantics (visibility timeout, receive count,
  dead-letter, FIFO, per-queue concurrency) and report 08's long waits and
  timers.
- The worker transport must be worker-initiated; the push models in
  Terrakube and tofu-controller conflict with private workers behind NAT.
- The policy engine must embed OPA through the `v1` packages, with per-policy
  Rego versions, a restricted capability set and context timeouts.
- Event intake must be durable from the moment a webhook is acknowledged.
- Drift detection, plan approval by ID, sync windows and order-only versus
  data dependencies are patterns with working implementations to compare
  against reports 08 and 18.

## Inferred and undocumented

- That a crash loses Atlantis and Digger commands started from webhook
  goroutines is inferred from the `go` statements; neither project documents
  it.
- That Terrakube agents in a private network need inbound reach from the API
  is inferred from the executor URL POST.
- That Burrito leaves Terraform state in each layer's own backend is
  inferred from the datastore's documented scope.
- The MPL-2.0 consequence for copied code is an inference from the licence
  text, not legal advice.
- Temporal workflow versioning requirements were not read *(unverified)*.
- Whether Terramate Cloud features are sold only by subscription was not
  confirmed from a pricing page *(unverified)*.
- Whether the Digger orchestrator continues under the OpenTaco name, beyond
  the docs redirect, was not confirmed *(unverified)*.
- How Spacelift's own Postgres queue is implemented is not published, so the
  River comparison is by behaviour only *(unverified)*.

## Versions at the research baseline

| Project | Commit read | Commit date | Latest release |
|---|---|---|---|
| runatlantis/atlantis | `9a10cfd470c0` | 2026-10-27 | v0.49.0 (2026-10-25) |
| leg100/otf | `cf7f7836d859` | 2026-07-16 | v0.6.3 (2026-06-11) |
| AzBuilder/terrakube | `2a7ddfb1bfa1` | 2026-10-27 | 2.33.3 (2026-10-21) |
| diggerhq/digger | `7c237c093d72` | 2026-10-22 | v0.6.154 (2026-10-07) |
| padok-team/burrito | `ed21360c45e6` | 2026-10-26 | v0.15.0 (2026-10-23) |
| terramate-io/terramate | `1f2f2d72a5c1` | 2026-10-16 | v0.17.3 (2026-09-08) |
| flux-iac/tofu-controller | `e2db61bdd264` | 2026-10-28 | v0.16.6 (2026-10-26) |
| open-policy-agent/opa | `359db769deb0` | 2026-10-28 | v1.21.4 (2026-10-23) |
| temporalio/temporal | `41d9f3b60536` | 2026-10-28 | v1.32.1 (2026-10-08) |
| riverqueue/river | `fc6c1d57cc72` | 2026-10-26 | v0.50.0 (2026-10-27) |
| temporalio/sdk-go | not cloned | | v1.50.0 (2026-10-15) |

## Sources

Documentation, read 2026-10-28:
- <https://www.runatlantis.io/docs/locking>, <https://www.runatlantis.io/docs/faq>, <https://www.runatlantis.io/docs/deployment>, <https://www.runatlantis.io/docs/server-configuration>, <https://www.runatlantis.io/docs/policy-checking>, <https://www.runatlantis.io/docs/api-endpoints>, <https://www.runatlantis.io/docs/how-atlantis-works>
- <https://docs.otf.ninja/runners/>, <https://docs.otf.ninja/executors/>, <https://docs.otf.ninja/config/flags/>, <https://docs.otf.ninja/rbac/>, <https://docs.otf.ninja/registry/>, <https://docs.otf.ninja/tfe_api/>
- <https://docs.terrakube.io/getting-started/design-and-architecture>, <https://docs.terrakube.io/getting-started/deployment/self-hosted-agents>, <https://docs.terrakube.io/getting-started/deployment/ephemeral-agents>, <https://docs.terrakube.io/user-guide/policy-enforcement-opa>, <https://docs.terrakube.io/user-guide/drift-detection>, <https://docs.terrakube.io/user-guide/private-registry>
- <https://docs.opentaco.dev/readme/introduction>
- <https://docs.burrito.tf/latest/operator-manual/architecture/>
- <https://terramate.io/docs/>, <https://terramate.io/docs/cli/change-detection/>
- <https://flux-iac.github.io/tofu-controller/>
- <https://www.openpolicyagent.org/docs/integration>, <https://www.openpolicyagent.org/docs/management-decision-logs>
- <https://docs.temporal.io/temporal-service/temporal-server>, <https://docs.temporal.io/>
- <https://riverqueue.com/docs/pro>, <https://riverqueue.com/docs/transactional-enqueueing>

Repositories at the commits read:
- <https://github.com/runatlantis/atlantis/tree/9a10cfd470c0>
- <https://github.com/leg100/otf/tree/cf7f7836d859>
- <https://github.com/AzBuilder/terrakube/tree/2a7ddfb1bfa1>
- <https://github.com/diggerhq/digger/tree/7c237c093d72>
- <https://github.com/padok-team/burrito/tree/ed21360c45e6>
- <https://github.com/terramate-io/terramate/tree/1f2f2d72a5c1>
- <https://github.com/flux-iac/tofu-controller/tree/e2db61bdd264>
- <https://github.com/open-policy-agent/opa/tree/359db769deb0>
- <https://github.com/temporalio/temporal/tree/41d9f3b60536>
- <https://github.com/riverqueue/river/tree/fc6c1d57cc72>

Related reports: [08](08-run-lifecycle.md), [09](09-policies.md),
[10](10-workers-and-execution.md), [16](16-state-management.md),
[18](18-scheduling-drift-dependencies.md),
[24](24-self-hosted-reference.md).
