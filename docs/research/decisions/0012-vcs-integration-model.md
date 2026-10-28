# 0012: VCS integration model

Status: Proposed

## Context

Every tracked and proposed run that is not started by hand starts as a VCS
event, and every result a developer sees in a pull request goes back through
the VCS. Spacelift supports six external providers and raw Git, each with its
own credentials, webhook authentication and API, behind one integration
envelope with a default per provider and any number of space-level
integrations ([11 sections 1 and 2](../reference/11-vcs-integrations.md#1-providers),
[2.3](../reference/11-vcs-integrations.md#23-default-and-space-level-integrations)).
Events are normalised into the push policy's input, fanned out to every stack
on the repository, deduplicated and capped
([11 section 11](../reference/11-vcs-integrations.md#11-webhook-events-and-what-they-trigger)).
Results go back as commit checks, pull request comments and deployments
([11 section 12](../reference/11-vcs-integrations.md#12-commit-checks-pull-request-comments-and-deployments)).
A VCS agent pool lets the server reach a VCS that the internet cannot
([11 section 14](../reference/11-vcs-integrations.md#14-vcs-agent-pools)).

Two reference facts shape intake. Self-hosted Spacelift puts inbound webhooks
on a queue with a dead-letter queue and processes them asynchronously
([24 section 3.3](../reference/24-self-hosted-reference.md#33-message-queues)).
Atlantis and Digger process deliveries in a goroutine after answering, so a
crash loses the event, while Terrakube commits each delivery before answering
and claims it with fencing
([27 section 2.4](../reference/27-reference-materials.md#24-do-not-copy),
[section 5.4](../reference/27-reference-materials.md#54-do-not-copy),
[section 4.3](../reference/27-reference-materials.md#43-borrow)).

P4 asks which default the VCS agent follows where the docs (a strict allowlist)
and the agent source at the pin (no validation, because the flags are never
registered) disagree
([11 section 14.6](../reference/11-vcs-integrations.md#146-what-it-proxies-request-validation);
[30, product decisions](../reference/30-open-questions.md#product-decisions)).

## Decision

### Provider abstraction

`internal/vcs` defines the contract; each provider is a package under it
(`github`, `gitlab`, `bitbucketcloud`, `bitbucketdc`, `azuredevops`, `rawgit`).
A provider implements the capabilities it has; the server checks capabilities,
not provider names.

| Interface | Methods (summary) | Providers |
|---|---|---|
| `Webhooks` | `Verify(req, body, secrets)`, `DeliveryID(req, body)`, `Parse(req, body) ([]Event, error)` | all but raw Git |
| `Source` | `ResolveRef`, `AffectedFiles(base, head)`, `ReadFile(ref, path)` (for `.spacelift/config.yml`), `SourceCredential(run)` | all |
| `Browse` | `Repositories`, `Branches` | all |
| `Checks` | `SetIndividual`, `SetAggregated` | all but raw Git |
| `Comments` | `Upsert(pr, dedupKey, body)` | all but raw Git |
| `Deployments` | `Report(stack, run, state)` | GitHub, GitLab |
| `Tester` | `Test()` returning `CONNECTION`, `PERMISSIONS`, `WEBHOOK`, `GITHUB_APP_EVENTS` checks | all but raw Git |

- **Normalised events.** `Parse` turns a delivery into zero or more
  `vcs.Event` values: `push`, `tag`, `pull_request` (with the `action` values
  of the push policy input), `comment`, `check_action` (GitHub's **Preview**
  and **Deploy**), `repository_renamed`. Each carries the repository
  (namespace and name), branch or tag, head SHA, author and, for pull requests,
  the provider-specific `approved`, `mergeable`, `undiverged` and `head_owner`
  values defined in [11 section 13](../reference/11-vcs-integrations.md#13-push-policy-interplay).
  The push policy input (0009) is built from `vcs.Event`, never from a raw
  payload.
- **Clients.** GitHub uses `github.com/google/go-github` with
  `github.com/bradleyfalzon/ghinstallation/v2` for App installation tokens;
  GitLab uses `gitlab.com/gitlab-org/api/client-go`; Bitbucket Cloud, Bitbucket
  Data Center and Azure DevOps use small hand-written clients over `net/http`
  that call only the routes in their allowlist table (below). Every client's
  transport is a `vcs.Transport` that sends `private://` hosts through a VCS
  agent pool.
- **Outbound limits.** A provider rate-limit answer (`403` or `429` with
  `Retry-After`, `X-RateLimit-Reset` or `RateLimit-Reset`) reschedules the job
  for the reset time without counting an attempt (0011). Each call has a
  30-second timeout.

### Integrations, the default and spaces

- **Rows.** `vcs_integrations` holds every integration, per
  [28 section 12.6](../reference/28-data-model.md#126-vcs-and-cloud-integrations):
  provider, `slug` (from the immutable name), space, labels, `is_default`, check
  mode and options, `use_git_checkout`, provider settings (`api_host`,
  `user_facing_host`, app ID, installations), `credentials_sealed` (private key,
  token or password), `webhook_secret_sealed`, and
  `previous_webhook_secret_sealed` with `previous_secret_valid_until`. Sealing
  is [0013](0013-secrets-at-rest.md)'s.
- **One default per provider.** A partial unique index
  `(account_id, provider) where is_default` enforces it, and a `CHECK` ties a
  default to the root space. `isDefault` and `name` are fixed after creation.
- **Resolution at use time.** A stack or module with no
  `vcs_integration_id` uses its provider's current default whenever an event
  arrives or a run starts; the default is never copied onto the stack.
- **Space reach.** A stack may bind an integration in its own space or in a
  space it inherits from, by [0007](0007-authorization-spaces-and-roles.md)'s
  inheritance rule. Readers of the space see name, description and labels;
  provider details, webhook URL and secret need the integration admin action
  in that space (0007).
- **Deletion detaches.** Deleting an integration in use sets
  `stacks.vcs_detached = true` (and the same on modules) in the same
  transaction; a detached stack receives no events and refuses manual triggers
  with `409 invalid-state` ([0022](0022-api-errors-and-update-semantics.md))
  until it is rebound.
- **The `GITHUB` provider.** infraweave operates no Marketplace app. The
  `GITHUB` value names one GitHub App that the operator registers for the
  installation (server settings `vcs.github_app.app_id`,
  `vcs.github_app.private_key_file`, `vcs.github_app.webhook_secret_file`);
  `githubIntegration` and `githubIntegrationUpdate` read and set its check
  options. `GITHUB_ENTERPRISE` is the per-account custom App on github.com or
  GitHub Enterprise Server, including the manifest flow
  ([11 section 4.2](../reference/11-vcs-integrations.md#42-operations)).
- **Out of this record.** Which other providers are served is decision
  0021's. It builds `BLOB` and `ORIGIN` as schema-only capabilities (P3);
  `SPACELIFT` repos are documented, so its scope rule serves them; it does
  not name `SHOWCASE`. Each served provider implements `Source` and needs no
  other change here.

### Webhook intake: stored before acknowledgement

Each integration has one endpoint, `POST /webhooks/vcs/{provider}/{integrationId}`;
the operator's GitHub App uses `POST /webhooks/vcs/github`. The
`<provider>WebhooksEndpoint` reads return these URLs. They answer what the
provider expects (status and an empty body), never problem details.

The handler does, in order:

1. **Bound the body.** Read at most 25 MiB; a larger body answers `413`.
2. **Find the integration.** An unknown or deleted one answers `404`.
3. **Verify.** Per provider, with constant-time comparison (`hmac.Equal`,
   `subtle.ConstantTimeCompare`), against the current secret and, until
   `previous_secret_valid_until`, the previous one:

   | Provider | Check | Delivery ID |
   |---|---|---|
   | GitHub, GitHub Enterprise | `X-Hub-Signature-256` is `sha256=` and the hex HMAC-SHA256 of the body; the SHA-1 `X-Hub-Signature` alone is refused | `X-GitHub-Delivery` |
   | GitLab | `X-Gitlab-Token` equals the secret | `X-Gitlab-Event-UUID` |
   | Bitbucket Cloud | `X-Hub-Signature` is `sha256=` and the hex HMAC-SHA256 of the body | `X-Request-UUID` |
   | Bitbucket Data Center | as Bitbucket Cloud; a delivery without a signature is refused, as Spacelift refuses it | `X-Request-Id` |
   | Azure DevOps | HTTP Basic with an empty user name and the webhook password | the payload's event `id` |

   A failed check answers `401` and stores nothing, so an unauthenticated
   sender cannot fill the database. It is logged and counted
   (`infraweave.vcs.webhook.rejected` by provider and reason; metric names are
   decision 0017's). A provider that sends no delivery ID gets the SHA-256 of
   the body as its ID.
4. **Filter.** Event types that [11 section 11.1](../reference/11-vcs-integrations.md#111-events-consumed-per-provider)
   does not list answer `200` and are not stored.
5. **Store and enqueue in one transaction.** Insert into `vcs_deliveries`
   (`integration_id`, `delivery_id`, provider event name, received time,
   selected headers, the body inline as `bytea` up to 256 KiB or else an
   object key in the `inbox` store written before the transaction, state
   `received`) with `ON CONFLICT (integration_id, delivery_id) DO NOTHING`. When
   the row is new, `queue.EnqueueTx` adds a `vcs.process_delivery` job on the
   `vcs-inbox` queue with `unique_key` the delivery row ID and `ordering_key`
   the integration plus repository, so one repository's events run in arrival
   order ([0011](0011-persistent-work-queue.md)).
6. **Acknowledge.** `202` for a new delivery, `200` for a duplicate. If the
   transaction fails the answer is `503`, so providers that retry do so.

The handler makes no call to the VCS and evaluates no policy, so it answers
well inside GitHub's 10-second delivery timeout.

### Processing

- **`vcs.process_delivery`** parses the stored body, updates `pull_requests`
  and repository renames, resolves the affected stacks and modules through the
  `(vcs_integration, repository, branch)` index of report 28, and computes the
  affected files once per event. In the transaction that completes the job it
  sets the delivery to `dispatched` and enqueues one `vcs.evaluate_event` job
  per stack or module (unique key: delivery plus stack), so one stack's
  failure is retried alone and never repeats another's run.
- **`vcs.evaluate_event`** evaluates the push policy (0009) and applies its
  outcome (`track`, `propose`, `ignore`, `notrigger`, `cancel`, `lock`), and
  creates runs ([0008](0008-run-execution-and-worker-protocol.md)) in the
  transaction that completes the job. Run deduplication on stack, commit SHA
  and run type is a unique index on `runs`, so a retried job cannot create a
  second run. A `/spacelift` comment that proposes bypasses it.
- **Budgets.** Every child job carries the delivery's deadline, received time
  plus 10 minutes; a job that starts after it completes with outcome `expired`
  and acts on nothing. The `vcsEventTriggeredRunsLimit` cap is a counter on the
  delivery row incremented in each run-creating transaction; a run past the cap
  is not created and the cap failure is reported as a check.
- **Superseded proposed runs.** A push to a branch stops that branch's
  proposed runs still in `PLANNING` on an older commit (T19 in
  [08 section 3](../reference/08-run-lifecycle.md#transition-table)).
- **Retention.** Delivery rows are kept 7 days (`vcs.deliveries.retention`),
  then deleted by the hourly periodic task `vcs.deliveries.prune`; `inbox` objects expire after 2 days
  ([0005](0005-persistence-postgresql.md)). A delivery whose job is dead stays
  visible with state `failed`. `GET /vcs-integrations/{id}/deliveries` lists
  deliveries and `POST /vcs-integrations/{id}/deliveries/{deliveryId}/replay`
  enqueues one again, both needing the integration admin action.
- **Webhook registration** stays with the user, as in Spacelift: infraweave
  shows the URL and secret, and the `WEBHOOK` test check compares expected
  and actual hooks. infraweave does not create or delete hooks on the VCS.

### Checks, comments and deployments

All feedback runs as queue jobs triggered by run state changes (the event
model is decision 0015's), never inline in a transition.

- **Checks.** `vcs.report_check` jobs carry the run and check, with unique key
  `(run, check name)` so a newer state coalesces into a pending job, and
  ordering key the run so states post in order. Individual checks are named
  `spacelift/<stack slug>`; aggregated checks are `spacelift/tracked`,
  `spacelift/proposed` and `spacelift/modules`. `vcs_checks` (`INDIVIDUAL`,
  `AGGREGATED`, `ALL`) and the options `individualFailuresOnly` and
  `individualSkipUnchanged` apply as
  [11 section 12.1](../reference/11-vcs-integrations.md#121-vcs-check-modes) says.
  A check update sets a state, so a repeat is harmless.
- **Check actions.** A GitHub `check_run` requested action creates a proposed
  (**Preview**) or tracked (**Deploy**) run with `triggered_by`
  `github/<login>`, unless `githubActionDeploy` is off.
- **Pull request comments.** Comments from the `feature:add_plan_pr_comment`
  label and from notification policy `pull_request` rules go through
  `Comments.Upsert`. `vcs_pr_comments` maps `(integration, repository, pull
  request, dedup key)` to the provider's comment ID. Every body ends with a
  hidden marker `<!-- infraweave:<dedup key> -->`; before creating a comment
  the job searches the pull request for the marker, so a crash between
  creation and recording edits the first comment instead of posting a second.
- **Deployments.** GitHub Deployments and GitLab environments follow
  [11 section 12.4](../reference/11-vcs-integrations.md#124-deployments-and-environments),
  including the `ghenv:<name>` and `ghenv:-` labels.

### Source access for runs

Workers fetch source directly; the server never proxies archives. The run
spec ([0008](0008-run-execution-and-worker-protocol.md)) carries a source
credential that is part of the run's sealed secret material:

| Provider | Credential |
|---|---|
| GitHub, GitHub Enterprise | An installation token minted for the run, limited to that repository and `contents: read`, expiring after one hour |
| GitLab, Bitbucket, Azure DevOps | The integration's token, which these providers cannot narrow |
| Raw Git | None; public HTTPS URLs only |

A run behind a VCS agent pool needs a private worker that can reach the VCS;
the launcher's mapping of pool names to endpoints is 0008's.

### VCS agent pools

- **Rows.** `vcs_agent_pools` (name unique in the account, description,
  `key_hash`), `vcs_agent_connections` (pool, connection ID, metadata, last
  poll), and `vcs_agent_requests`, an `UNLOGGED` table holding proxied
  requests and responses in flight.
- **Token.** Base64 of JSON `{"version":1,"endpoint":"<server URL>","pool":"<ULID>","key":"<secret>"}`.
  The key is 32 random bytes, stored only as its SHA-256 (0006);
  `vcsAgentPoolReset` replaces it and drops current connections.
- **Protocol.** HTTP long-poll on the same chi server, not gRPC. The agent
  (`infraweave vcs-agent`) holds `parallelism` polls open (default 4):
  - `POST /vcs-agent/v1/poll`, authenticated with the key, held up to 30
    seconds; answers `200` with one request envelope `{id, method, path,
    headers, body}` (body base64) or `204`.
  - `POST /vcs-agent/v1/responses/{id}` with `{status, headers, body}` or
    `{error}`.
- **Routing across replicas.** A replica that needs the agent inserts a
  request row and notifies channel `infraweave_vcs_agent` with the pool ID; a
  replica holding a poll for that pool claims the row with
  `FOR UPDATE SKIP LOCKED` and hands it out. The response is written to the
  row and notified on the same channel with the request ID. A request not
  answered in 25 seconds fails with `upstream-error`; with no connection seen
  for the pool in 90 seconds it fails at once. Bodies are limited to 16 MiB.
- **Addressing.** `private://<pool>[/<path>]` in `api_host` or
  `user_facing_host` routes API calls through the pool; the host shown in links
  is the other field when it is not `private://`. Accepting either field covers
  both the docs' and the provider examples' forms
  ([11 section 14.2](../reference/11-vcs-integrations.md#142-addressing)).
- **Credentials stay on the server.** The server adds the VCS credential
  headers to each request; the agent holds only its pool key and network reach.
- **Validation (P4, VCS agent part).** The agent validates every request
  against an allowlist by default. `--validation` (`INFRAWEAVE_VCS_AGENT_VALIDATION`)
  takes `allowlist` (default), `blocklist` with `--blocklist-path`, or `none`.
  The allowlist for each vendor is the route table that infraweave's own
  provider client uses (`internal/vcs/<provider>/routes.go`), compiled into the
  agent, so the two cannot drift. `--allowed-projects` (default `.*`) narrows
  it by project. Unmatched requests are refused on every vendor, Azure DevOps
  included. A refused request is answered `{error}` and logged with its method
  and path.

## Rationale

- **Store before acknowledging.** Committing the delivery and its job in one
  transaction means an acknowledged event is never lost and an unacknowledged
  one is retried by the provider. Doing no VCS calls and no policy work in the
  handler keeps the answer fast whatever the fan-out.
- **Verify before storing.** Storing unverified bodies would let anyone who
  learns a URL write to the database and consume queue capacity.
- **Dedupe on the provider's delivery ID.** Providers redeliver on timeouts and
  on manual redelivery; the unique key makes a redelivery a no-op instead of a
  second fan-out. Run-level deduplication on SHA and type remains the
  Spacelift rule for distinct deliveries about one commit.
- **Fan-out as child jobs.** Following Terrakube's isolation, one stack's
  failing policy cannot block or repeat another's run, and no transaction stays
  open across the fan-out
  ([27 section 4.3](../reference/27-reference-materials.md#43-borrow)).
- **Ordering per repository.** The supersede rule and the stack's tracked
  commit depend on push order; an ordering key gives that without serialising
  unrelated repositories.
- **A previous-secret window.** Regenerating a secret otherwise drops events
  until the user updates the provider side; accepting the previous secret for
  a bounded window (`vcs.webhook_secret_overlap`, default 24 hours) closes that
  gap without accepting an old secret for ever.
- **Rejected: managing hooks on the VCS** (OTF's synchroniser,
  [27 section 3.3](../reference/27-reference-materials.md#33-borrow-patterns-only-mpl-20)).
  It needs hook-admin scopes beyond the token scopes Spacelift documents.
- **Rejected: Spacelift's gRPC agent protocol.** Serving it would let the
  released `spacelift-vcs-agent` connect, but it needs a gRPC server outside
  chi and HTTP/2 end to end through load balancers, which is why self-hosted
  Spacelift runs a separate gateway service and load balancer
  ([11 section 14.7](../reference/11-vcs-integrations.md#147-workers-and-the-gateway)).
  Long-poll reuses the worker protocol's shape (0008) and any HTTPS load
  balancer, and 0001 already excludes running Spacelift's clients unchanged.
- **P4: the strict allowlist, as the docs promise.** The research precedence
  rule ranks the docs above the source, and the docs' behaviour is also the
  safe one: the agent sits inside a private network, and without validation a
  compromised or misconfigured server could use it as a general proxy into that
  network. Deriving the allowlist from the client's own route table removes
  the reason a released agent might ship without one: an allowlist that falls
  behind the server breaks integrations. Capture C78 decides only whether this
  is also a departure from the released Spacelift agent.
- **Departures from Spacelift**, for the deviations register: an
  operator-registered GitHub App in place of the Marketplace app; webhook URLs
  under `/webhooks/vcs/`; acceptance of the previous webhook secret for a
  window; the delivery listing and replay routes; the HTTP long-poll agent
  protocol and token layout; refusal of unmatched Azure DevOps requests by the
  agent; and `spacelift/<stack slug>` as the individual check name, which
  Spacelift does not document.

## Constraints

- **Unverified provider facts.** The webhook header schemes in the table
  follow each provider's own documentation; report 11 marks them **[I]** for
  Spacelift. The individual check name is unverified in Spacelift; branch
  protection rules copied from Spacelift may need the name changed.
- **Provider-wide tokens.** GitLab, Bitbucket and Azure DevOps runs receive a
  token with the integration's full scope, sealed to the worker pool's key and
  masked in logs; a narrower credential needs provider support that does not
  exist.
- **Spacelift's VCS agent does not connect** to infraweave, nor does
  infraweave's agent connect to Spacelift.
- **GitHub does not retry** failed deliveries on its own. A delivery answered
  `503` during a database outage was never stored, so the replay route cannot
  recover it; it needs a redelivery from GitHub's UI or API.
- **Tier gates** on space-level integrations, aggregated checks and agent pools
  are decision 0021's.

## Verification

- `internal/vcs/<provider>/verify_test.go`: valid, missing, wrong and
  previous-secret signatures for each provider, from fixtures that follow each
  provider's documented examples; the SHA-1-only GitHub case is refused.
- `internal/vcs/<provider>/parse_test.go`: each consumed event type parses into
  the expected `vcs.Event`; unconsumed types are filtered.
- `internal/vcs/agent/allowlist_test.go`: every route in each provider's
  `routes.go` is allowed, and a request outside it is refused, Azure DevOps
  included; a test client records every request each provider client makes in
  its own tests and fails on any that the allowlist refuses.
- Gherkin features under `test/acceptance/features/`, against PostgreSQL in a
  container and a fake VCS served by chi:
  - `vcs_webhooks.feature`: a signed push creates one tracked run; a bad
    signature answers `401` and stores no row; the same delivery ID twice
    stores one row and one run; a replica stopped after acknowledging, before
    processing, leaves the job to another replica, which creates the run once.
  - `vcs_feedback.feature`: individual and aggregated checks post in state
    order; a comment upsert after a simulated crash edits the existing comment.
  - `vcs_agent.feature`: an integration with `api_host` `private://pool`
    lists branches through a connected agent; a disallowed path is refused; no
    connected agent answers `502 upstream-error`.

## References

- [11: VCS integrations](../reference/11-vcs-integrations.md), sections 1 to
  14 as linked above.
- [24 section 3.3](../reference/24-self-hosted-reference.md#33-message-queues):
  the self-hosted `webhooks` queue.
- [27 section 2.3](../reference/27-reference-materials.md#23-borrow) (Atlantis
  validators), [section 4.3](../reference/27-reference-materials.md#43-borrow)
  (Terrakube's inbox), [section 12](../reference/27-reference-materials.md#12-cross-tool-comparison).
- [28 section 12.6](../reference/28-data-model.md#126-vcs-and-cloud-integrations):
  the integration tables.
- [30, VCS captures](../reference/30-open-questions.md#vcs-integrations-report-11):
  C78 and the other open VCS questions.
- spacelift-io/vcs-agent at `553aba3b3e54` (MIT): the agent that P4 compares.
