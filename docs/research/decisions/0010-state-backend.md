# 0010: Terraform and OpenTofu state backend

Status: Accepted

## Context

A stack created with managed state keeps its Terraform or OpenTofu state in Spacelift.
Report [16](../reference/16-state-management.md) records how that works: runs reach an
HTTP endpoint that speaks Terraform's `http` backend protocol with one-off credentials
checked against the run's liveness and phase; other stacks and offline tools read state
through external access; state can be imported at creation or later, rolled back to an
earlier version, and listed as a history; Terragrunt units each get a workspace with its
own lock. The endpoint paths, the lock semantics and the credential format are not
documented ([16 section 1.1](../reference/16-state-management.md#11-how-a-run-reaches-it)),
so infraweave defines them.

State files hold secrets in plaintext: provider credentials, generated passwords, private
keys. Self-hosted Spacelift relies on bucket versioning and server-side encryption for
history and confidentiality ([24 section 3.2](../reference/24-self-hosted-reference.md#32-object-storage-buckets)).
infraweave runs on any of decision [0005](0005-persistence-postgresql.md)'s object store
drivers, including local disk, where neither can be assumed.

## Decision

### Endpoints

The flat `state` domain ([0004](0004-repository-architecture.md)) serves the backend from
`internal/state/internal/backend` on chi under `/state/v1`, with `LOCK` and `UNLOCK`
registered as methods through `chi.RegisterMethod`. The routes sit on the API listener
behind the controls every API surface shares, and depart from them only where decision
[0003](0003-api-conventions.md) records it.

| Method and path | Purpose |
|---|---|
| `GET /state/v1/stacks/{stack}` | Current state of the stack's default workspace |
| `POST /state/v1/stacks/{stack}` | Write a new state version |
| `LOCK /state/v1/stacks/{stack}` | Take the lock |
| `UNLOCK /state/v1/stacks/{stack}` | Release the lock |
| the same four under `/state/v1/stacks/{stack}/workspaces/{workspace}` | One Terragrunt unit's workspace |
| `GET /state/v1/download/{token}` | Signed download of the current state (`stateDownloadUrl`) |
| `PUT /state/v1/uploads/{token}` | Signed upload of a state file (`stateUploadUrl`) |

- `{stack}` is the stack's ULID, or its slug when the segment is not a canonical upper-case
  ULID. Runs always receive the ULID, so a reslug never breaks a running run.
- `{workspace}` is the unit's workspace name, URL-encoded, at most 512 bytes.
- `DELETE` and any other method answer `405`. Terraform's `http` backend has no workspace
  support of its own, so nothing calls them.

### How a run reaches it

- **Backend file.** For a stack with managed state, the runner (decision
  [0008](0008-run-execution-and-worker-protocol.md)) writes `infraweave_backend.tf` at the
  project root with a `backend "http"` block naming `address`, `lock_address` and
  `unlock_address` (all the stack URL), `lock_method = "LOCK"` and
  `unlock_method = "UNLOCK"`. Code that declares its own backend fails initialization, as
  in Spacelift ([16 section 1.1](../reference/16-state-management.md#11-how-a-run-reaches-it)).
- **Credentials.** The username is `run` and the password is the run token (decision
  [0006](0006-authentication-and-tokens.md)), passed through `TF_HTTP_USERNAME` and
  `TF_HTTP_PASSWORD` in the phase environment, never written into the workspace, so the
  token is not in a saved workspace or a log.
- **Lock timeout.** The runner passes `-lock-timeout=5m` to commands that lock (setting
  `runner.state_lock_timeout`). The stack environment variable
  `INFRAWEAVE_DISABLE_STATE_LOCK`, the counterpart of Spacelift's `SPACELIFT_DISABLE_STATE_LOCK`
  (report [13](../reference/13-configuration.md#22-user-tunable-spacelift_-variables)), makes the
  runner pass `-lock=false` to `plan`.

### Who may call what

A request authenticates with HTTP Basic. The password is either a run token or a credential
decision [0006](0006-authentication-and-tokens.md) accepts as a bearer (user API token,
session token, in-run API token).

| Caller | `GET` | `LOCK`, `UNLOCK` | `POST` |
|---|---|---|---|
| Run token, run in `INITIALIZING`, `PLANNING` | yes | shared lock | no |
| Run token, run in `APPLYING`, `DESTROYING`, `PERFORMING` | yes | exclusive lock | yes, holding the exclusive lock |
| Run token, any other state, or a terminal run | no | no | no |
| Run token for another stack | no | no | no |
| Other credential, stack with external access on, caller holds `STACK_STATE_READ` and `STACK_STATE_DOWNLOAD` on the stack's space (decision [0007](0007-authorization-spaces-and-roles.md)) | yes | no | no |
| Other credential, any other case | no | no | no |

- The run's state is read on every request, so a run that ends or is stopped loses access
  at once, whatever the token's expiry ([16 section 1.1](../reference/16-state-management.md#11-how-a-run-reaches-it)).
- External access is read-only and requires managed state
  ([16 section 3](../reference/16-state-management.md#3-external-state-access)). A
  `terraform_remote_state` data source uses `backend = "http"` with the stack URL and the
  caller's token as password.

### Locks

- **Table `state_locks`**: `(account_id, stack_id, workspace, lock_id)` key, `mode` (`shared` or
  `exclusive`), `run_id`, `info` (the `LockInfo` JSON Terraform sent, at most 16 KiB),
  `created_at`. The lock ID is the `ID` field of the `LockInfo` body.
- **Taking a lock** runs in one transaction that locks the `state_workspaces` row
  `FOR UPDATE`, then:

  | Held | Requested | Result |
  |---|---|---|
  | nothing | either | granted, `200` |
  | shared locks only | shared | granted, `200` |
  | shared locks | exclusive | `423`, body the oldest holder's `LockInfo` |
  | exclusive | either | `423`, body the holder's `LockInfo` |
  | the same `lock_id`, held by the same run | the same mode | `200` (Terraform retries are idempotent) |
  | the same `lock_id`, held by another run or in the other mode | either | `423`, body that lock's `LockInfo` |

  Terraform's `http` backend reads a `423` body as the current lock and reports its holder
  to the user.
- **Releasing.** `UNLOCK` deletes the row whose `lock_id` matches the body's `ID`. When
  locks are held and none has that ID, it answers `423` with the holder's `LockInfo`; when
  none is held, `200`, so a repeated release is idempotent. A client sends back the
  `LockInfo` of the lock it took. `tofu force-unlock <id>` sends a `LockInfo` carrying the
  given ID. Releasing a lock that another run holds, by its ID, needs the exclusive grant,
  as a force-unlock does; any other caller gets `403`, so a planning run cannot release an
  apply's lock.
- **Force-unlock.** `terraform force-unlock` sends an `UNLOCK` with an empty body,
  because Terraform's `http` client ignores the ID argument and sends only the lock its own
  process took, which a new process does not have. An `UNLOCK` with an empty body is a
  force-unlock: it releases every lock held on the workspace, and only a caller that the
  table above lets take the exclusive lock may send it, so in practice a task running
  `terraform force-unlock`. Any other caller gets `403`. A user releases a stuck lock
  through `tofuWorkspaceUnlock` instead.
- **Audit.** Releasing a lock that another run holds, by an empty body or by
  `tofu force-unlock <id>`, records an audit entry (decision 0015) naming the caller, the
  workspace and each released `LockInfo`.
- **Writes under a lock.** `POST` carries the lock ID in the `ID` query parameter, as
  Terraform sends it. A `POST` whose ID does not name an exclusive lock held by the same run
  answers `423` with the holder's `LockInfo`, or `{}` when no lock is held, both before
  the body streams and as the version commits.
- **Locks die with their run.** A run transition writes only the `runs` domain's tables, so
  `state` deletes a run's locks when it consumes the run's terminal `run_updated`, and
  `runsapi.StateAccess` refuses a lock to a run that is no longer active
  (decision [0008](0008-run-execution-and-worker-protocol.md)). A stopped, killed or failed
  run never keeps a lock.
- **Operator release.** `tofuWorkspaceUnlock` and `tofuWorkspaceUnlockAll`
  ([16 section 8](../reference/16-state-management.md#8-terragrunt-units-and-workspace-locks))
  delete lock rows for one workspace or every workspace of a stack, and need
  `STACK_WORKSPACE_UNLOCK`.

### Storage

- **Table `state_workspaces`**: `(account_id, stack_id, name)` unique, `name` empty for the
  stack's default workspace; `current_version_id`; `created_at`. It is the `TofuWorkspace`
  entity, with `isLocked` and `lockedBy` derived from `state_locks`.
- **Table `managed_state_versions`** (report [28 section 12.3](../reference/28-data-model.md#123-stacks-and-runs)):
  `id` (ULID), `account_id`, `stack_id`, `workspace`, `object_key`, `size`, `sha256`,
  `serial`, `lineage`, `client_encrypted`, `run_id` (null for imports), `rollback`,
  `rollback_username`, `rolled_back_from`, `imported`, `imported_username`,
  `imported_at`, `created_at` (answered as `lastModified`). Index
  `(account_id, stack_id, workspace, created_at)` serves `searchManagedStateVersions` and
  the version's key to its workspace.
- **Objects.** Each version is its own object in the `states` store (decision
  [0005](0005-persistence-postgresql.md)) under
  `<account>/<stack ulid>/<sha256 of workspace name>/<version ulid>`, written once and
  never overwritten. History does not depend on bucket versioning.
- **Sealing.** Every object is sealed in the sealed-object format of decision
  [0013](0013-secrets-at-rest.md), under its own content key held in
  `blob_objects.content_key_sealed`, so rotating data keys never rewrites a state object.
  Plaintext state exists only in server memory while streaming and on the worker.

### Writing a version

1. A `POST` declares its `Content-Length`, since a sealed object's size is fixed before
   it streams: one without it answers `400`, and one over `state.max_size` (default
   512 MiB) answers `413` before anything streams. A `blob_objects` row for the new object
   is then inserted as `pending` (decision [0005](0005-persistence-postgresql.md)), and
   the body streams through a SHA-256 hasher and the sealer into the object store.
2. **`Content-MD5`.** Terraform's and OpenTofu's `http` clients send a `Content-MD5`
   header on every write. Normally the body also streams through an MD5 hasher, and a
   header that disagrees with the body answers `400`. When the process runs with
   `GODEBUG=fips140=only`, Go refuses MD5 (`crypto/md5.Sum` panics and a hash's `Write`
   returns an error; see [0013](0013-secrets-at-rest.md#fips-140-3)), so the header is
   accepted and ignored. TLS protects the body in transit and the recorded SHA-256 protects
   it at rest.
3. The body must be a JSON object, scanned as it streams. A body with `version`, `serial`
   and `lineage` is a plain state and those are recorded. A body without `serial` and
   `lineage`, as OpenTofu's client-side encryption produces, is stored as
   `client_encrypted` and never parsed again
   ([16 section 9](../reference/16-state-management.md#9-opentofu-state-encryption)). Any
   other body answers `400`. A body refused for its `Content-MD5` or its JSON has its
   object deleted.
4. When the SHA-256 equals the current version's, no version is added and the object is
   deleted: a run with no changes creates none
   ([16 section 6.2](../reference/16-state-management.md#62-behaviour)).
5. Otherwise one transaction, under the stack's hold and the workspace row's lock, checks
   the write's lock again, marks the object `live`, inserts the version row and moves
   `current_version_id`. A run whose lock went while its body streamed gets `423` and its
   object is deleted. A consumer that reads state, such as the resources view, adds its
   `state.indexed` job to this transaction: a job kind is enqueued only where a handler
   runs it (decision [0011](0011-persistent-work-queue.md)).
6. An object whose transaction never commits keeps its `pending` row, and decision
   [0005](0005-persistence-postgresql.md)'s cleanup deletes both after 24 hours, so no
   bucket listing is needed.

The server does not compare lineage or serial: Terraform enforces both on the client, and
`terraform state push -force` from a task must keep working.

### Reading

`GET` opens the current version's object, unseals while streaming, and answers `200` with
`Content-Type: application/json` and no `Content-MD5`, in every mode: the header is
optional, and the client computes the MD5 itself when it is absent. A workspace with no
version, or one no lock has yet made, answers `204`, which Terraform reads as no state.
Each write of the answer moves the connection's deadlines, as each read of a `POST`'s body
does (decision [0003](0003-api-conventions.md)).

### Upload, import and download URLs

- **`stateUploadUrl`** answers `objectId` (a ULID), `url` (`/state/v1/uploads/{token}`,
  absolute) and an empty `uploadHeaders`. The token is `(objectId, account,
  expiry)`, signed with HMAC-SHA256 under `crypt.DeriveKey("state-upload-url")`
  ([0013](0013-secrets-at-rest.md)) and valid 15 minutes. The upload seals into the `uploads` store; an upload not
  imported within a day expires through `blob.expire`.
- **Import** (`stackCreate` with `stackObjectID`, `stackManagedStateImport`) checks, in
  order:

  | Check | Failure |
  |---|---|
  | The caller holds `STACK_MANAGED_STATE_IMPORT` | `403`, problem `forbidden` |
  | The stack manages state | `409`, problem `invalid-state` |
  | The stack is locked (`stackLock`), except at creation | `409`, problem `invalid-state` |
  | No non-terminal tracked run, task or destroy run on the stack, except at creation | `409`, problem `invalid-state` |
  | The upload exists and is at most 100 MiB | `422`, problem `validation-failed` |
  | It is a JSON state with `version` 4, or a client-encrypted envelope | `422`, problem `validation-failed` |

  It reseals the file into `states` as a new version with `imported` set, then deletes the
  upload ([16 section 5](../reference/16-state-management.md#5-importing-state)). The
  problem slugs are those of decision [0022](0022-api-errors-and-update-semantics.md);
  the `detail` names the failed check.
- **`stateDownloadUrl`** needs external access on and both state permissions, and answers a
  signed `/state/v1/download/{token}` URL valid 15 minutes for the current version. A
  `version` argument answers `422`, problem `validation-failed`, as Spacelift's schema
  documents.

### Rollback and retention

- **`stackManagedStateRollback`** checks `STACK_MANAGED_STATE_ROLLBACK` and the same
  locked, idle conditions as import, then unseals the target version, reseals it as a new
  object and inserts a version with `rollback`, `rollback_username`, `rolled_back_from`
  and the target's `run_id` ([16 section 6](../reference/16-state-management.md#6-state-history-and-rollback)).
  `Stack.isStateRollback` is true while the current version has `rollback` set; the next
  write clears it.
- **Retention.** Non-current versions are deleted once `state.noncurrent_retention` has
  passed since they stopped being current (default 90 days; `0` keeps them forever). The
  `state.retention` periodic task (daily, decision [0011](0011-persistent-work-queue.md))
  deletes their rows and objects. The current version and versions referenced by a
  non-terminal run are never deleted. Rolling back to a deleted version answers `404`,
  problem `not-found`.
- **Stack deletion.** `state` registers its `stacksapi.StackGuard` with the `stacks` domain
  ([0004](0004-repository-architecture.md)). A stack's delete enqueues a `state.purge` job
  in its own transaction, so a delete that rolls back enqueues nothing, and the job deletes
  the stack's workspaces, locks, version rows and objects once the stack row is gone. A
  deleted account's state goes the same way, through `state`'s purge of `account.deleted`.

### Errors on the backend paths

Every error on the backend paths is a problem detail of decision
[0022](0022-api-errors-and-update-semantics.md), as on every other surface, but a lock
conflict, whose body Terraform reads:

| Status | When | Body |
|---|---|---|
| `200` | success | state, or empty |
| `204` | `GET` with no state | empty |
| `400` | `POST` without `Content-Length`, bad `Content-MD5` (not under `fips140=only`), body not a plain or client-encrypted state, a body cut short, `LockInfo` not JSON | problem `invalid-request` |
| `401` | missing or invalid credentials, or another surface's credential, with `WWW-Authenticate: Basic realm="infraweave-state"` | problem `unauthenticated` |
| `403` | valid credentials without the right in the table above, or an empty-body `UNLOCK` or an `UNLOCK` of another run's lock from a caller not granted the exclusive lock | problem `forbidden` |
| `404` | unknown stack or workspace, or the stack does not manage state | problem `not-found` |
| `405` | any other method | problem `method-not-allowed` |
| `413` | body over `state.max_size`, or a `LockInfo` over 16 KiB | problem `payload-too-large` |
| `423` | lock conflict, or a `POST` without the run's exclusive lock | the holder's `LockInfo` JSON, or `{}` for a `POST` when no lock is held |

The shared controls' own refusals, such as `429` and a panic's `500`, are problems here as
on every surface.

The public API operations (import, rollback, the URL operations, workspace unlock) are
ordinary REST operations and answer the problem details of decision
[0022](0022-api-errors-and-update-semantics.md).

## Rationale

- **The `http` backend** is the protocol Spacelift's runs speak and the one Terraform and
  OpenTofu ship without plugins; OpenTaco's statesman shows the lock contract over a
  pluggable store ([27 section 5.3](../reference/27-reference-materials.md#53-borrow)).
- **Run tokens checked against the run's state on each request** reproduce Spacelift's
  one-off credentials and OTF's job-scoped tokens
  ([27 section 3](../reference/27-reference-materials.md#3-otf)): a leaked token stops working
  when the run moves on.
- **Shared locks for planning.** Proposed runs plan concurrently with each other; an
  exclusive plan lock would make two previews block each other for no safety gain. A plan
  still conflicts with an apply, which is the risk Spacelift's lock-by-default protects
  against.
- **One object per version, sealed by infraweave.** History works on any store, including
  local disk, and a stolen bucket yields only ciphertext.
- **Upload and download through infraweave.** A pre-signed object-store URL would hand out
  sealed bytes on download and leave plaintext at rest on upload; routing both through the
  server keeps state sealed at rest. States are small enough that proxying costs little.
- **Rejected: the Terraform Cloud `remote` API for external access.** It is a second API
  surface with its own JSON:API shapes, discovery document and `terraform login` flow, which
  report 27 advises against ([27 section 3.4](../reference/27-reference-materials.md#34-do-not-copy)).
  The `http` backend with a token already serves `terraform_remote_state` and offline
  `terraform state pull`.
- **Rejected: locks in the object store.** Conditional writes differ across S3-compatible
  stores and do not exist on local disk; PostgreSQL row locks are already the
  serialization point for runs.
- **Problem details on the backend paths.** Terraform's `http` client reads the body of a
  `423` alone, as the lock it conflicts with, and reports only the status of any other
  error, so a problem costs it nothing, and every other client of `/state/v1` handles
  errors as on the other surfaces ([0003](0003-api-conventions.md)). The `423` keeps the
  `LockInfo` body because Terraform names the lock's holder from it.
- **Force-unlock on an empty body.** Refusing it would leave `terraform force-unlock`
  always failing, so the usual Terraform answer to a stuck lock would not work. Limiting
  it to callers that may take the exclusive lock keeps it where an apply could already
  overwrite state, and the audit entry records who broke whose lock.
- **90 days of non-current history.** Long enough to roll back past a quarter's changes,
  short enough that the `states` store stops growing with every run; `0` keeps every
  version for those who need it.
- **Ignoring `Content-MD5` under `fips140=only`.** MD5 here is a transfer check, not a
  security function, but `fips140=only` exists to prove no unapproved algorithm runs.
  - **Rejected: `fips140.WithoutEnforcement` around the MD5.** It would put an exemption in
    the code path the FIPS-only CI job is there to check, and an operator who chose
    `only` would still run MD5.
  - **Rejected: answering `400` to every write under `only`.** Terraform always sends the
    header, so no run could write state.
- **Departures from Spacelift**, for the deviations register:
  - External access uses the `http` backend, not the `remote` backend.
  - Upload and download URLs point at infraweave, not at the object store.
  - Planning takes a shared lock; Spacelift's lock kind is undocumented.
  - A run's locks are released when it ends, so a stopped run leaves no stuck lock.
  - Non-current versions are kept 90 days by default, where SaaS keeps 30 days.
  - `terraform force-unlock` works, as an audited force-unlock.
  - Under `GODEBUG=fips140=only`, `Content-MD5` is accepted and not checked, and reads
    never send it.
  - History does not rely on bucket versioning.

## Constraints

- **Existing `remote` backend configurations** that read Spacelift state from outside must
  change to `backend = "http"` when moving to infraweave.
- **Client-side encrypted OpenTofu state** is stored opaquely: outputs, the resources view
  and anything else that parses state see nothing for those versions.
- **Unverified in Spacelift and decided here:** which run states may read and write, and
  what counts as a pending run for import and rollback.
- **The KEK of decision [0013](0013-secrets-at-rest.md) is required to read any state.** A
  database and bucket backup without it restores no state.
- **A force-unlock breaks a running run's lock.** That run's own `UNLOCK` then answers
  `423`, and a write it attempts answers `423`, as Terraform's own force-unlock warns.

## Verification

- `internal/state` unit tests: the authorization table per run state; every lock
  row of the conflict table; lock release on a run's terminal `run_updated`; every shared
  control of decision 0003 on `/state/v1`, but the deviations it records; empty-body `UNLOCK`
  allowed for an exclusive-lock caller with an audit entry and refused with `403` for any
  other; `Content-MD5` mismatch answering `400`, and the same request accepted under
  `GODEBUG=fips140=only` with the body's SHA-256 recorded; no `Content-MD5` on reads; the
  size limit; identical writes adding no version; client-encrypted bodies; token expiry for
  the upload and download URLs.
- Store contract tests against PostgreSQL and every object store driver of decision 0005 (decision 0016): versions,
  current pointer, orphan collection through `pending` `blob_objects` rows, retention.
- Gherkin acceptance with real Terraform and OpenTofu in testcontainers:
  `state/backend.feature` (init, plan, apply through the backend; a second apply blocked by
  `423` until the first ends), `state/external_access.feature` (`terraform_remote_state`
  with and without the permissions and the stack switch), `state/import.feature`,
  `state/rollback.feature`, `state/terragrunt_units.feature` with workspace unlock, and
  `state/force_unlock.feature` (`terraform force-unlock` from a task releases a lock left
  by a killed run).
- A retention test on a fake clock: a non-current version is deleted after 90 days, the
  current version and a version a pending run references are kept.
- A sealing test writes a state with a known secret output and asserts the bytes in the
  object store do not contain it.

## References

- [16: State management](../reference/16-state-management.md).
- [24 section 3.2: object storage buckets](../reference/24-self-hosted-reference.md#32-object-storage-buckets).
- [27 section 5: Digger (OpenTaco)](../reference/27-reference-materials.md#5-digger-opentaco).
- [14 section 3.3: the action catalogue](../reference/14-spaces-rbac-and-identity.md#33-the-action-catalogue).
- [Go FIPS 140-3 mode](https://go.dev/doc/security/fips140): `GODEBUG=fips140=only`.
- Terraform `http` backend: <https://developer.hashicorp.com/terraform/language/backend/http>;
  the client at `hashicorp/terraform` `b32cc59736f3` and `opentofu/opentofu` `f0af18832521`,
  `internal/backend/remote-state/http/client.go` (lock, unlock and `ID` query behaviour).
