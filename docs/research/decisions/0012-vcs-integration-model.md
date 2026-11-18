# 0012: VCS integration model

Status: Accepted

## Context

Every tracked and proposed run that is not started by hand starts as a VCS
event, and every result a developer sees in a pull request goes back through
the VCS. Spacelift supports six external providers and raw Git, each with its
own credentials, webhook authentication and API, behind one integration
envelope with a default per provider and any number of space-level
integrations ([11 sections 1 and 2](../reference/11-vcs-integrations.md#1-providers),
[2.3](../reference/11-vcs-integrations.md#23-default-and-space-level-integrations)).
Events are normalized into the push policy's input, fanned out to every stack
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

PD-04 asks which default the VCS agent follows where the docs (a strict allowlist)
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
| `Source` | `ResolveRef`, `AffectedFiles(base, head)`, `ReadFile(ref, path)` (for `.infraweave/config.yml`), `SourceCredential(run)` | all |
| `Browse` | `Repositories`, `Branches` | all |
| `Checks` | `SetIndividual`, `SetAggregated` | all but raw Git |
| `Comments` | `Upsert(pr, dedupKey, body)` | all but raw Git |
| `Deployments` | `Report(stack, run, state)` | GitHub, GitLab |
| `Tester` | `Test()` returning `CONNECTION`, `PERMISSIONS`, `WEBHOOK`, `GITHUB_APP_EVENTS` checks | all but raw Git |

- **Normalized events.** `Parse` turns a delivery into zero or more
  `vcs.Event` values: `push`, `tag`, `pull_request` (with the `action` values
  of the push policy input), `comment`, `check_action` (GitHub's **Preview**
  and **Deploy**), `repository_renamed`, `owner_renamed` and installation
  state changes. Each carries the repository
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
  `user_facing_host`, app ID), `credentials_sealed` (private key,
  token or password), `webhook_secret_sealed`, and
  `previous_webhook_secret_sealed` with `previous_secret_valid_until`. Sealing
  is [0013](0013-secrets-at-rest.md)'s.
- **One default per provider.** A partial unique index
  `(account_id, provider) where is_default` enforces it, and a `CHECK` ties a
  default to the account's top space ([0007](0007-authorization-spaces-and-roles.md#spaces)). `isDefault` and `name` are fixed after creation.
- **Non-default creation.** The effective `isDefault: false`, including an
  omitted or null value, requires the `multiple-vcs-integrations` option
  (0021). The body marker rejects an explicit false before the handler;
  the integration service checks the same entitlement for every create,
  including a manifest-created App after the account's first default.
  A tier drop does not gate an existing integration's reads, updates,
  connection tests, source use or deletion.
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
- **The `GITHUB` provider (D1).** infraweave has no built-in GitHub
  integration. Each account registers its own App as a `GITHUB_ENTERPRISE`
  integration, on github.com or GitHub Enterprise Server, by key or through
  the manifest flow. Every installation of that App belongs to that account.
  `githubIntegration` and `githubIntegrationUpdate` are not served. `GITHUB`
  remains an alias: a stack (and later module) write naming it, or omitting
  the provider, resolves to the account's default GitHub App integration
  when it names no integration, or to the named GitHub App integration in
  that account. Reads retain the provider and integration the write named;
  resolution does not copy a default onto the stack. A write with no
  integration to resolve answers `422` at `/provider`.
- **Installations (D22).** GitHub App installations and their repositories
  are integration state, separate from provider settings. Events keep this
  state current; they never create or rename an account.
- **Bitbucket Cloud's host.** A `BITBUCKET_CLOUD` integration names no
  host: every one reaches `https://api.bitbucket.org/2.0`, or the server
  setting `vcs.bitbucket_cloud.api_host` when the operator points it at a
  stand-in such as a proxy or a test fake.
- **Out of this record.** Which other providers are served is decision
  0021's. It builds `BLOB` and `ORIGIN` as schema-only interfaces (PD-03);
  the `INFRAWEAVE` provider, whose files sit in the database and which
  Spacelift calls `SPACELIFT` (0001's rename table), is documented, so its
  scope rule serves it; it does not name `SHOWCASE`. Each served provider implements `Source` and needs no
  other change here.

### Every provider is a peer

GitHub, GitLab, Bitbucket Cloud, Bitbucket Data Center and Azure DevOps are
built together: every capability, operation and acceptance scenario
infraweave gives one it gives the others, in the same phase, wherever the
provider's API has an equivalent. This goes beyond Spacelift, whose support
outside GitHub is thinner
([11 section 5](../reference/11-vcs-integrations.md#5-gitlab)). Where
GitHub has a capability, the others gain its equivalent:

| GitHub capability | Equivalent on the other providers | Where none exists |
|---|---|---|
| An installation token per run, limited to one repository | GitLab: a project access token with `read_repository`; Bitbucket Data Center: a repository access token with `REPO_READ`; each revoked when the run ends (below) | Bitbucket Cloud and Azure DevOps mint no repository token through their APIs, so their runs receive the integration's token |
| Preview and Deploy check-run actions, while the stack allows run promotion | | None: comment events reach P13's push policies on every provider |
| Renames followed through the `repository` event | Renames followed by the stable repository or project ID every event carries; an event whose path differs from the stack's under the same ID updates the stack | |
| Deployments | GitLab environments | Bitbucket and Azure DevOps: commit statuses only |

Each provider has a chi fake (`githubfake`, `gitlabfake`,
`bitbucketcloudfake`, `bitbucketdcfake`, `azuredevopsfake`) that holds its
client to the routes it calls in every pull request, and the scheduled
`vcs-lab` job checks every provider against real repositories
([0016](0016-testing-and-acceptance.md)).

### Webhook intake: stored before acknowledgement

**Controls (D2).** The one route is
`POST /webhooks/vcs/{provider}/{integrationId}`. The API surfaces record
owns the shared controls and intake's deviations. Each delivery, verified
or not, is an `expensive` draw on the client's address. Intake has no
feature gate; each job's kind is checked when claimed. Request IDs, access
logging, the route table, bounded connections and uncacheable answers
apply unchanged. Every answer, including a refusal or throttle, is a
status with an empty body; a failed verification has no `WWW-Authenticate`.

ref: [0003, API surfaces](0003-api-conventions.md#api-surfaces), [0011](0011-persistent-work-queue.md), [0022, protocol endpoints](0022-api-errors-and-update-semantics.md#errors-a-status-per-class-problem-details-spacelifts-words-kept-pd-01-option-b).

The handler does, in order:

1. **Bound the body.** Read at most 25 MiB; a larger body answers `413`.
2. **Find the integration.** An unknown or deleted one answers `404`.
3. **Verify.** Compare in constant time against the current secret and,
   until `previous_secret_valid_until`, the previous one:

   | Provider | Check | Delivery ID |
   |---|---|---|
   | GitHub App integrations | `X-Hub-Signature-256`: `sha256=` plus the hex HMAC-SHA256 of the body; SHA-1 alone is refused | `X-GitHub-Delivery` |
   | GitLab | `X-Gitlab-Token` equals the secret | `X-Gitlab-Event-UUID` |
   | Bitbucket Cloud | `X-Hub-Signature`: `sha256=` plus the hex HMAC-SHA256 of the body | `X-Request-UUID` |
   | Bitbucket Data Center | As Bitbucket Cloud; unsigned deliveries are refused | `X-Request-Id` |
   | Azure DevOps | HTTP Basic with an empty username and the webhook password | The payload's event `id` |

   Failure answers `401`, stores nothing, and increments
   `infraweave.vcs.webhook.rejected` by provider and reason. A provider
   without a delivery ID uses the body's SHA-256.
4. **Filter.** Unconsumed events answer `200` and are not stored:

   | Provider | Consumed events and actions |
   |---|---|
   | GitHub App integrations | `push`, `pull_request`, `pull_request_review`, `issue_comment` (`created` on a pull request), `check_run` (`requested_action`), `repository` (`renamed`), `installation`, `installation_repositories`, `installation_target` (`renamed`), `organization` (`renamed`) |
   | GitLab | Push, tag push, merge request, note on a merge request |
   | Bitbucket Cloud | Push; pull request created, updated, approved, approval removed, fulfilled, rejected, comment created |
   | Bitbucket Data Center | `repo:refs_changed`, `pr:opened`, `pr:from_ref_updated`, `pr:modified`, `pr:reviewer:approved`, `pr:reviewer:unapproved`, `pr:merged`, `pr:declined`, `pr:comment:added` |
   | Azure DevOps | Code pushed; pull request created, updated, merge attempted, commented on |

   GitLab's `approved` and `unapproved` notifications normalize to the policy
   action `edited`; the provider action remains in the retained body. Only
   the fully-approved notification proves `approved=true`. Payload-only
   mergeability requires resolved blocking discussions and a `mergeable`
   detailed status, or the legacy conflict-free status with that full
   approval notification. Drafts and explicit unapproval remain unmergeable;
   absent/checking facts stay false without an API enrichment call. This
   follows [GitLab's action meanings](https://docs.gitlab.com/user/project/integrations/webhook_events/#merge-request-events)
   and report 11's provider-specific approval and mergeability rules.

   Azure DevOps reads routing from the body. An authenticated invalid JSON
   envelope, or missing, empty or non-string `eventType`, is retained under a
   private invalid-envelope marker and fails parsing durably; a valid unsupported
   event type is filtered. Comment repository identity comes from
   `resource.pullRequest.repository`, as in the documented service-hook shape.
   Payload approval requires an approving reviewer vote (5 or 10). A successful
   preview merge is not proof that every blocking policy passed or that the
   source is up to date, so absent that evidence `mergeable` and `undiverged`
   remain false. The fake follows the documented transient retry schedule for
   408/502/503/504 (eight retries); enduring failures and subscription probation
   are separate from sending a delivery.

5. **Store and enqueue (D3).** In one transaction insert `vcs_deliveries`
   with integration, delivery ID, provider event, received time, selected
   headers, body and state `received`, and enqueue `vcs.process_delivery`.
   `ON CONFLICT (integration_id, delivery_id) DO NOTHING` deduplicates.
   Bodies up to 256 KiB are inline `bytea`; larger ones use an `inbox`
   object written before the transaction. The job uses queue `vcs-inbox`,
   unique key the row ID and ordering key integration plus repository.
   Stored headers are the event and delivery ID headers, `Content-Type`,
   `User-Agent`, and the signature header: `X-Hub-Signature-256` for
   GitHub, `X-Hub-Signature` for both Bitbucket providers. For GitLab and
   Azure DevOps store the handler's HMAC-SHA256 of the body under the
   secret that verified it instead. `Authorization` and `X-Gitlab-Token`
   are never stored or logged. Replay can verify the retained digest
   without retaining a secret-bearing request header.
6. **Acknowledge.** `202` for new, `200` for duplicate, `503` if the
   transaction fails. The handler makes no VCS call and evaluates no
   policy, staying inside GitHub's 10-second timeout.

**URLs (D6).** Each `<provider>WebhooksEndpoint` read answers the provider's
account-default integration URL; `githubEnterpriseWebhooksEndpoint?id=`
can name one integration instead. With none it answers `404 not-found`.
The integration's `SPACE_ADMIN` action decides access in its space, the
default's being the top space. The four default-only schemas have no
integration selector, so their subject is the account's top space.
GitHub's optional `id` resolves the named integration's own space, falling
back to the top space only when omitted. These are space-action checks,
not a separate account-admin or operator requirement. The URL and secret
are not reader data.

**Rotation (D4).** All five providers have a regenerate operation,
including `azureDevOpsRepoIntegrationRegenerateWebhookSecret`. In one
transaction a new 32-byte current secret is sealed, the former current
becomes previous, and its validity ends at now plus
`vcs.webhook_secret_overlap` (default 24 hours, validated at load). A second
regenerate within that window drops the older previous secret. The admin
receives the new secret; the audited change carries no secret.

**Test checks (D19).** `WEBHOOK` on GitLab, both Bitbucket providers and
Azure DevOps checks every repository the integration's stacks use for a
hook to this URL with all consumed events. `webHooks` metadata gives
`expected` and `actual`, names up to 10 failing repositories and counts the
rest. GitHub's `GITHUB_APP_EVENTS` reads `GET /app`: it checks subscribed
consumed events that require subscription, and checks, pull requests,
issues and deployments write plus organization members read. GitHub sends
`installation` and `installation_repositories` without subscription. The
manifest and check share one permissions/events list; GitHub feedback uses
the Checks API, so `statuses` is not requested. Registration stays with the
user: infraweave never creates or deletes provider hooks.

### Processing

- **Dispatch.** `vcs.process_delivery` parses the stored body, maintains
  repository and pull request state, resolves affected stacks and modules,
  and computes affected files once per event. Its completing transaction
  marks the delivery `dispatched` and enqueues `vcs.evaluate_event` per
  stack or module, unique by delivery and stack. Each child inherits the
  received-time-plus-10-minutes deadline; a job starting after it records
  `expired` and acts on nothing.
- **Default push behaviour (D9).** A tracked-branch push moves
  `trackedBranchHead`. If its full before-to-after diff touches the project
  root or an additional glob, it also moves `trackedCommit`, set by
  `infraweave`, and starts a tracked run on the head. Otherwise it records
  `NO_AFFECTED_FILES_IN_PROJECT_ROOT`. A new branch diffs from its merge
  base with the repository's default branch. Other branch pushes touching
  the root propose on the head, with no pull request needed. Pull request
  events record the request and its head commits and create no run. Fork
  pull requests get runs only through P13's `allow_fork`.

  Push comparisons use complete before/after trees rather than the head
  commit's changed-file list. GitHub walks single-level trees if its recursive
  response is truncated. Bitbucket Data Center enumerates both paged file sets
  and compares common paths individually, avoiding the global changes hard cap;
  binary or mode-only diff identities still count when no text hunks exist.
  New-branch comparisons there require the merge-base endpoint from Data Center
  8.16 or later. An unsupported or incomplete provider response fails preparation
  safely rather than silently substituting another comparison.
- **Ignored triggers (D9, D17).** Record tracked pushes with no affected
  root files, tag pushes on every stack of the repository
  (`TAGS_IGNORED_BY_DEFAULT`), and stacks beyond the cap
  (`RUN_LIMIT_REACHED`) on the tracked branch. Other-branch pushes, pull request events and
  comments record no ignored trigger. Record for every account;
  `stackSearchRunIgnoredTriggers` needs `SPACE_ADMIN` on the stack's space.
  Recording and queue acknowledgement share the completing child transaction,
  including its claim and replay-generation fences and live account/stack holds.
  Rows are unique per account, stack, canonical commit hash and reason; a tag
  ignore remains independent of a path ignore for that commit. Tag metadata is
  resolved once from the delivered immutable head, without a diff or mutable
  tag-name lookup. Azure annotated-tag object IDs are peeled through bounded
  native `annotatedtags/{objectId}` reads before commit metadata is fetched;
  lightweight tags use the delivered commit ID. Deleted tags with a zero head
  record nothing. Retain seven days, exclude older rows from reads immediately,
  and prune hourly; stack deletion and account purge remove their history.
  Reads use the existing VCS serving alias, shared expensive search controls and
  the `ignored-runs` option, with empty policy attachments. `createdAt` and
  `reasonType` filter the current stack's history, newest first.
  P13 owns `CUSTOM_POLICY*`; capture C46 remains open for those reasons.
- **Policy boundary (D24).** `vcs.evaluate_event` applies the default now
  and P13's push-policy outcome later, creating runs in the transaction
  that completes the job. Comment events are stored for those policies
  and create nothing before P13. Event runs have `triggered_by` `vcs/commit`;
  check-button runs have `github/<login>`.
- **Once per commit and type (D8).** Once a stack has any run of a type on
  a commit, a VCS event never creates another of that type on that commit,
  regardless of its state or trigger. Check under `lifecycle.Create`'s
  stack row lock, in the evaluate job's completing transaction, using a
  `runs` index on account, stack, type and commit hash. The type is in the
  key by the maintainer's choice of 2026-11-17: fast-forwarding a proposed
  commit onto the tracked branch still creates its tracked run. Manual
  triggers, check buttons and P13's comment-driven runs bypass this
  event-only check. The same ignored reason on the same stack and commit
  is recorded once.
- **Run cap (D18).** `vcs` owns the Account Settings operations
  `vcsEventTriggeredRunsLimit`, `accountUpdateVCSEventTriggeredRunsLimit`
  and their per-account table. The account has no cap unless set: `null`
  or 1 up to `vcs.event_runs_ceiling` (default 500), which always applies.
  Increment the delivery counter in the run-creating transaction. Every
  stack past the cap records `RUN_LIMIT_REACHED` and a failed check.

  One child job carries the ordered prepared events for one delivery/stack,
  preserving every ref of a multi-ref push. Full event and affected-file data
  is stored once beside the delivery; small child references keep it outside
  the queue's 64 KiB payload bound. Preparation and provider reads happen before
  the fenced transaction that stores those rows, enqueues children and completes
  dispatch. Admission runs under the stack's creation lock after stack refusals
  and any-state deduplication, immediately before insertion. A duplicate consumes
  no slot; a capped later event leaves the batch's earlier admitted runs intact.
  An unexpected failure rolls back the counter, run effects and job completion
  together. A capped/refused tracked push can update its branch head but never
  moves the tracked commit to a commit for which no run was admitted.
- **Superseded proposed runs.** A branch push stops its older proposed
  runs still in `PLANNING`.
- **Renames (D15).** `vcs` maps integration plus stable provider repository
  ID to path. A changed path renames every stack on the old path through
  a `stacksapi` method that bumps its version and appends `stack.changed`,
  without an audit entry. This identity-preserving change leaves the event's
  `source` flag false: background tracking must not advance commits on behalf of
  a capped or refused push. Initial and manually changed sources still initialize
  their pointers, including when native resolution first canonicalizes their
  coordinates. Native lookup stays outside the database transaction; subsequent
  canonicalization and pointer initialization share one held current integration
  binding. An implicit source resolved through a former default cannot update
  either coordinates or pointers. GitHub `installation_target` and `organization`
  renamed events re-point all stacks on the owner's repositories. Provider API
  resolution seeds the account/integration-scoped native-ID map. Event processing
  changes the map and calls the Stacks-owned repoint port in the same fenced
  transaction that stores prepared facts and enqueues stack children. Provider
  reads stay outside that transaction; affected stacks are selected afterward.
  The completion fence rechecks current coordinates, so a rename committed after
  preparation cannot lose a push. A path occupied by another native ID is not
  reassigned or used for fan-out; a later unambiguous observation can free it.
  UUID spelling differences are canonicalized only for native identity matching.
  Replay retains its original observation time and reaches the latest mapped
  path without undoing newer rename facts.
- **Installation state (D22).** Installation and repository events keep
  the GitHub App integration's installations and repositories current.
  Deleted or suspended installations mark the integration unhealthy;
  affected runs fail fast with that reason. No account is created or
  renamed by any VCS event. Availability belongs to the current installation ID
  for a stable provider owner. Reinstalling the same App creates a new current ID;
  late jobs for superseded IDs cannot poison that replacement. Removed repository
  membership is retained separately from the current map, so background tracking
  cannot restore removed access. A replacement installation's unknown membership
  keeps the ordinary native lookup. Membership presence and coordinates retain
  separate observation times: a delayed add can restore access without undoing
  an owner rename learned after removal dropped the current map. Known inactive
  or removed access fails
  before a source lookup or token mint. Connection testing refreshes installation
  availability through the App-authenticated installation list and reports a
  failed `PERMISSIONS` check naming the affected owner/state. No credential is
  needed in the identity, state or membership rows.
- **Pull requests (D16).** `vcs` owns `pull_requests` and the head commits
  each request has had. `stacksapi` declares the read port behind
  `Stack.pullRequests`, implemented by `vcs`: at most 50 open requests
  against the tracked branch that have a run on the stack, each with its
  runs matched by SHA, current head first. A run can appear on several
  requests sharing a commit; no run-to-request ownership column is implied.
  Parent delivery completion upserts the provider snapshot and every observed
  head in the same held job transaction, before marking it dispatched. Late
  snapshots contribute history without reverting newer metadata. Repository
  identity is stable; namespace/name is a separate stack-lookup projection
  maintained by repository rename handling. Records exist before any run.
  Reads resolve only the current integration's metadata, page candidates until
  fifty are eligible, and join account/stack-scoped Runs commit facts; no
  credential or provider request is needed. Equal request update times and run
  creation times are ordered by descending internal ID. Closed and merged
  requests expire after the delivery retention horizon; open requests remain
  live, and account purge removes both tables.
  Deleting their integration cascades its request records and head history.
  Only nonzero full Git commit IDs enter head membership; an empty commit
  from a local-workspace run never qualifies a request. A terminal notification
  without a valid head closes the request while preserving its last real head.
  GitLab's webhook actor is not its request creator: when only `author_id` is
  supplied, preparation reuses a stored creator or reads `GET /users/:id`
  outside the transaction, without refreshing the delivered head. Bitbucket
  Cloud `pullrequest:rejected` and Data Center `pr:declined` are consumed close
  events so declined requests leave the list. Provider-native timestamps are
  retained, including GitLab's legacy UTC format and DC's milliseconds.
  Cloud's native `SUPERSEDED` state also closes a request when observed in an
  already-consumed update; it does not introduce another webhook event key.
- **Retention and replay (D7).** Rows last 7 days
  (`vcs.deliveries.retention`), pruned hourly; `inbox` objects last 2 days.
  A dead job leaves the delivery `failed`. `vcsIntegrationDeliveries`,
  `GET /vcs-integrations/{vcsIntegrationId}/deliveries`, lists newest first
  with search cursor/page-size paging. Each `VCSDelivery` has `id`,
  `deliveryId`, `event`, `receivedAt`, `state` (`RECEIVED`, `DISPATCHED`,
  `FAILED`, `EXPIRED`), `lastError` and `runsCreated`. It is `standard`;
  replay, `vcsIntegrationDeliveryReplay` at
  `POST /vcs-integrations/{vcsIntegrationId}/deliveries/{deliveryId}/replay`,
  is `expensive`, honours idempotency keys, and answers `202` with the
  delivery. Both need the integration's `SPACE_ADMIN` and
  `vcs-integrations`, with every shared API control. Replay checks the
  stored signature again under the current or valid previous secret,
  resets state, deadline and cap counter, increments `replays`, and
  enqueues by delivery ID plus replay count in one transaction. Pending or
  running work, expired body, or a signature that no longer verifies
  answers `409 invalid-state`; missing or other-integration delivery is
  `404`. Replay is audited and obeys the once-per-commit rule: only runs
  that never existed are recovered. A replay generation fences the parent,
  stack children, prepared facts, cap counter and crash reconciliation. Replay
  refuses active work in any generation; old operator-retried jobs become
  no-ops after a generation change. A terminal child failure is sticky in the
  public delivery state with a safe VCS diagnostic, while the internal
  dispatched state permits its other children to finish. Replay clears that
  marker. Queue error strings and retained bodies never become public metadata.
  Inbox expiry is checked even before its sweep removes an object; inline
  bodies remain available for the retained row's lifetime. The listing uses
  fixed newest-first search edges/pageInfo with first/after/requestedPage;
  cursors are scoped to the account and integration.

New tables join their domain's initial `00001_*.sql` `CREATE`s; no `ALTER`
is added before v0.1.0.

ref: [0008](0008-run-execution-and-worker-protocol.md), [0009](0009-policy-engine-opa.md), [0011](0011-persistent-work-queue.md), [0013](0013-secrets-at-rest.md), [0015](0015-events-notifications-webhooks-audit.md), C46, C76, C269, C285.

### Checks, comments and deployments

**Feedback trigger (D13).** P12b builds the `events` domain's Commit checks
sink. From `run_updated` it enqueues `vcs` feedback jobs; no outbound effect
runs inline in a state transition.

ref: [0015, fan-out](0015-events-notifications-webhooks-audit.md#one-event-model).

- **Eligible runs (D11).** Tracked and proposed runs on repository commits
  post checks, whatever started them. Tasks, destroy runs, drift detection
  runs and local previews post none.
- **Modes and names (D11, D12).** `vcsChecks` is `INDIVIDUAL`, `AGGREGATED`
  or `ALL`: two toggles, at least one on. `aggregated-vcs-checks` gates both
  `AGGREGATED` and `ALL`. Individual names are
  `infraweave/<stack slug>/tracked` and `infraweave/<stack slug>` for
  proposed runs. Aggregated names are `infraweave/tracked`,
  `infraweave/proposed`, `infraweave/modules`.
- **In-place updates (D11).** GitHub keeps one individual check run per
  infraweave run, updated in place; the visible name stays fixed and
  `external_id` stays the stack ID. Recovery also matches the immutable run
  link or marker, so another run cannot take over that check. Other providers
  use their native status keyed by repository, commit and check name. The
  newest run owns that shared status, with creation time and ID as ties;
  older runs and stale transition sequences cannot overwrite it. Aggregates
  remain shared named resources on every provider. In `ALL`, a fixed
  aggregate name takes precedence when a proposed stack named `tracked`,
  `proposed`, or `modules` has the same native status key. `INDIVIDUAL`
  retains its individual name. GitHub keeps distinct individual and aggregate
  identities even when their visible names coincide. No run ID is appended
  to visible names or status keys, and no provider CI pipeline is created.
  `vcs.report_check` coalesces pending updates by run and check name and
  orders them by run; aggregate updates order by repository, commit and
  check name. Store provider IDs, and look up name/external ID before
  creating after an uncertain post, so a crash does not duplicate it.
  GitHub uses the Checks API only (`external_id` the stack ID); GitLab
  commit statuses; both Bitbucket providers build statuses; Azure DevOps
  commit statuses and, for recorded request heads, pull request statuses
  on the latest iteration.

  | Run state | Check state |
  |---|---|
  | `QUEUED`, `READY` | queued, or pending where queued is unavailable |
  | `PREPARING` through `APPLYING`, including `UNCONFIRMED` | in progress |
  | `FINISHED` | success with change counts; neutral “no changes” where supported, success elsewhere |
  | `FAILED`, `STOPPED`, `DISCARDED` | failure |
  | `CANCELED` | canceled where supported, failure elsewhere |

  `individualFailuresOnly` sends terminal failures alone;
  `individualSkipUnchanged` suppresses unchanged individual checks but
  still posts failures. Cap failures post a failed check with the run
  limit even though there is no run or `run_updated` event.
- **Aggregates (D11).** Read every run of the kind on that integration's
  repository and commit. Open at its first run; conclude when all have
  ended, failure if any failed, success otherwise, with per-stack counts.
  When a kind concludes, a kind with no runs concludes skipped with
  “0 runs”; modules stay skipped until P17. A later run updates that check;
  if the provider cannot reopen it, create another under the same name.
- **Buttons (D10).** GitHub checks carry **Preview** and **Deploy** only
  while `githubActionDeploy` is on. A press creates respectively a proposed
  or tracked run on that commit, triggered by `github/<login>`, bypassing
  event deduplication and using the delivery's run cap. The external ID
  must name this account's stack on this repository and integration; a
  forged or stale ID reaches no other stack. A Deploy sender needs
  repository write access, read through the App, and leaves the tracked
  commit unchanged. A disabled or refused action creates nothing and is
  logged and counted. Other providers have no equivalent check buttons.
- **Plan comments (D14).** Only `feature:add_plan_pr_comment` enables them,
  on every provider. Each run whose planning ends posts a new comment on
  each open request whose head is its commit: a heading linking stack and
  run, then a collapsed block of the masked planning log, cut to the
  provider's limit. Minimize that stack's previous comment as outdated on
  GitHub; leave it elsewhere. Hidden markers and `vcs_pr_comments`, keyed
  per run for plans, recover a post-before-recording crash without
  duplicating it. P18's notification-policy `pull_request` rules retain
  edit-in-place upserts with their own deduplication keys.
- **Deployments (D11).** A tracked run creates a GitHub deployment when
  reaching `UNCONFIRMED` or starting apply; proposed runs create none.
  The environment is `infraweave/<stack slug>`, overridden by
  `ghenv:<name>` or disabled by `ghenv:-`. Discard or failed apply marks
  failure, successful apply active. GitLab environments follow that rule.

  Deployment eligibility is VCS business logic over a Runs-owned fact reader:
  the run's identity and transitions through the event's exact state sequence.
  A later terminal state cannot erase whether it reached `UNCONFIRMED` or
  `APPLYING`, and a delayed earlier event cannot read future transitions. A
  tracked run that never reached either phase, including a no-change plan,
  reports no deployment. The first eligible report fixes its integration,
  source coordinates and environment without copying credentials. Jobs open
  the bound integration's current credentials; deleting it never falls back
  to a replacement default.

  GitHub creates the deployment with the full commit SHA, task `infraweave
  apply`, `production_environment: true`, `auto_merge: false`, and an explicit
  empty `required_contexts`. Its payload retains the run ID for recovery after
  an uncertain create. Statuses are `pending`, `queued`, `in_progress`,
  `success`, and `failure`; cancellation and discard map to failure. Recovery
  establishes the initial pending status even if the create response was lost.
  See the native [deployment](https://docs.github.com/en/rest/deployments/deployments)
  and [status](https://docs.github.com/en/rest/deployments/statuses) contracts.

  GitLab's native identity is environment, ref and SHA within a project; it
  offers no run-ID payload or ref/SHA list filters. Read environment-filtered
  pages and compare ref/SHA locally. Repeated runs at that same native identity
  can reuse a deployment, while GitHub retains one per run. GitLab states are
  `running`, `success`, `failed` and `canceled`; confirmation and applying both
  map to running. Find environments by exact name and create them on first
  use. See GitLab's [deployments](https://docs.gitlab.com/api/deployments/) and
  [environments](https://docs.gitlab.com/api/environments/) contracts.

  `vcs.report_deployment` has durable per-run/state uniqueness. GitHub jobs
  order by run; GitLab jobs order by the shared native resource, which also
  preserves each run's order. Its identity uses the canonical API host and
  installation path, project, environment, ref and SHA, scoped to the account;
  separate integrations reaching that same resource share its fence.
  Credentials and integration IDs do not distinguish GitLab resources. Host
  casing, default ports and equivalent API-prefix forms normalize together;
  distinct hosts or installation paths remain distinct. A bound integration
  retargeted to another API host is refused before any provider request, while
  credential rotation on the same host remains usable.

  The resource's newest eligible run is reserved atomically with enqueue.
  Its epoch is the authoritative first `UNCONFIRMED`/`APPLYING` timestamp,
  with run creation time and ID only breaking ties. Run creation order cannot
  establish deployment order: an older-created run can first become eligible
  later. A completed sequence prevents older states of that same run from
  regressing its reported status. Late reports from an older epoch are skipped.

  A report owns an outer `NoRetry` transaction, holding the account, queue
  generation and resource row across the provider call, bounded to 30 seconds.
  This bounded external-effect exception prevents claim recovery or another
  resource owner from overtaking the effect. An inherited transaction is
  refused because its outer retry policy would control replay. Cursor update
  and fenced job completion commit together. A provider effect whose response
  or local acknowledgement is lost is recovered by native lookup on a later
  queue attempt, never by retrying the transaction callback. Rate-limit
  responses with a reset snooze the job without spending an attempt; provider
  response bodies and credentials never enter retained queue diagnostics.

ref: [0015](0015-events-notifications-webhooks-audit.md), [0016](0016-testing-and-acceptance.md), RN-46, DV-047, C73, C74, C75, C79.

### Source access for runs

Workers fetch source directly; the server never proxies archives. Every
provider's source is the repository's HTTPS Git clone URL on its
user-facing host, from which the worker fetches the run's commit with Git,
so one fetch serves every provider, Bitbucket's included, which serves no
archive to an API token. The run spec
([0008](0008-run-execution-and-worker-protocol.md)) carries a source
credential, which Git presents as HTTP Basic auth and which is part of the
run's sealed secret material:

| Provider | Credential |
|---|---|
| GitHub, GitHub Enterprise | An installation token minted for the run, limited to that repository and `contents: read`, expiring after one hour |
| GitLab | A project access token minted for the run with `read_repository`, expiring the next day and revoked when the run reaches a terminal state; where the integration's token cannot create one (it needs the Maintainer role on the project, and GitLab refuses one to the bot user of a group or project access token), the integration's token, and the run's log says so |
| Bitbucket Data Center | A repository access token minted for the run with `REPO_READ`, expiring the next day and revoked when the run reaches a terminal state; where the integration's token cannot create one (it needs repository admin), the integration's token, and the run's log says so |
| Bitbucket Cloud, Azure DevOps | The integration's token, which these providers cannot narrow through their APIs |
| Raw Git | None; public HTTPS URLs only |

infraweave keeps only a minted token's ID, never the token, in
`vcs_run_tokens`. The transaction that ends a run enqueues one `vcs.revoke`
job, keyed by the run; it revokes each of the run's tokens through the
integration that minted it, treats a token already gone as revoked, and
leaves a token whose integration was deleted to expire.

A run behind a VCS agent pool needs a private worker that can reach the VCS;
the launcher's mapping of pool names to endpoints is 0008's.

**Checkout preference (D23).** `feature:enable_git_checkout` is ignored and
cannot change the integration's `useGitCheckout` setting; the integration
owns that preference. P12b adds no alternate archive transport: runs retain
the all-provider HTTPS Git clone and source/workspace contract. P19b owns
additional source providers and private-source reach.

ref: [plan, P19b](../../implementation/plan.md#p19b-vcs-agent-pools-and-other-source-providers), [0008](0008-run-execution-and-worker-protocol.md), DV-135, C72.

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
- **Validation (PD-04, VCS agent part).** The agent validates every request
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
  one can be redelivered by the provider. Doing no VCS calls and no policy work in the
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
  commit depend on push order; an ordering key gives that without serializing
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
- **PD-04: the strict allowlist, as the docs promise.** The research precedence
  rule ranks the docs above the source, and the docs' behaviour is also the
  safe one: the agent sits inside a private network, and without validation a
  compromised or misconfigured server could use it as a general proxy into that
  network. Deriving the allowlist from the client's own route table removes
  the reason a released agent might ship without one: an allowlist that falls
  behind the server breaks integrations. Capture C78 decides only whether this
  is also a departure from the released Spacelift agent.
- **Departures from Spacelift**, for the deviations register: a
  per-account GitHub App; per-integration webhook URLs; previous-secret
  window and Azure DevOps rotation; delivery listing and reverified replay;
  checks updated in place; full push diff; pull request events creating no
  run; label-only plan comments; the run ceiling; installation state without
  accounts; the ignored Git checkout label; per-run repository tokens on
  GitLab and Bitbucket Data Center; repository-ID renames; and the HTTP
  long-poll agent and strict allowlist.

## Constraints

- **Unverified provider facts.** The webhook header schemes in the table
  follow each provider's own documentation; report 11 marks them **[I]** for
  Spacelift. Check names and the absence of automatic PR-event runs are
  recorded as observed 2026-11-17; branch protection uses the renamed prefix.
- **Provider-wide tokens.** Bitbucket Cloud and Azure DevOps runs, and
  GitLab and Bitbucket Data Center runs whose integration cannot mint a
  repository token, receive a token with the integration's full scope, sealed to the worker pool's key and
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
- `sourcetest`: every provider's `Source` passes one contract suite: raw Git
  against Gitea and every other provider against its fake in every pull
  request, and against the real repositories the scheduled `vcs-lab` job
  creates.
- Gherkin features under `test/acceptance/features/`, against PostgreSQL in a
  container and a fake VCS served by chi:
  - `vcs/webhooks.feature`: a signed push creates one tracked run; a bad
    signature answers `401` and stores no row; the same delivery ID twice
    stores one row and one run; a replica stopped after acknowledging, before
    processing, leaves the job to another replica, which creates the run once.
  - `vcs/feedback.feature`: all provider fakes record individual and
    aggregated checks in state order, updated in place, with the state map,
    run exclusions, cap failure and option gate; GitHub buttons obey run
    promotion; labelled plan comments recover a simulated post-before-record
    crash and minimize the prior GitHub comment; deployments follow tracked
    runs. `@vcs-lab` variants verify the configured real providers.
  - `vcs/agent.feature`: an integration with `api_host` `private://pool`
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
- [30, VCS captures](../reference/30-open-questions.md#captures-needed):
  C78 and the other open VCS questions.
- spacelift-io/vcs-agent at `553aba3b3e54` (MIT): the agent that PD-04 compares.
