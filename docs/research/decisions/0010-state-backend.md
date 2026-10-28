# 0010: Terraform and OpenTofu state backend

Status: Proposed

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
infraweave runs on S3-compatible stores and on local disk (decision
[0005](0005-persistence-postgresql.md)), where neither can be assumed.

## Decision

### Endpoints

Package `internal/statebackend` serves the backend on chi under `/state/v1`, with `LOCK`
and `UNLOCK` registered as methods through `chi.RegisterMethod`.

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
  `SPACELIFT_DISABLE_STATE_LOCK` (report [13](../reference/13-configuration.md#22-user-tunable-spacelift_-variables))
  makes the runner pass `-lock=false` to `plan`, under its Spacelift name so stacks moved
  from Spacelift keep their behaviour.

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

- **Table `state_locks`**: `(stack_id, workspace, lock_id)` key, `mode` (`shared` or
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
  | the same `lock_id` | the same mode | `200` (Terraform retries are idempotent) |

  Terraform's `http` backend reads a `423` body as the current lock and reports its holder
  to the user.
- **Releasing.** `UNLOCK` deletes the row whose `lock_id` matches the body's `ID`; a
  mismatch answers `423` with the holder's `LockInfo`. A client sends back the `LockInfo`
  of the lock it took. `tofu force-unlock <id>` sends a `LockInfo` carrying the given ID and
  so succeeds for a caller who may lock. `terraform force-unlock` sends an empty body,
  because Terraform's `http` client ignores the ID argument and sends only the lock it
  took itself, so under this rule it always answers `423`; a stuck lock is then released
  through `tofuWorkspaceUnlock` or by ending its run.
- **Writes under a lock.** `POST` carries the lock ID in the `ID` query parameter, as
  Terraform sends it. A `POST` whose ID does not name an exclusive lock held by the same run
  answers `423`.
- **Locks die with their run.** The transition of a run into a terminal state deletes its
  locks in the same transaction (decision [0008](0008-run-execution-and-worker-protocol.md)).
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
  `md5`, `serial`, `lineage`, `client_encrypted`, `run_id` (null for imports), `rollback`,
  `rollback_username`, `rolled_back_from`, `imported`, `imported_username`,
  `imported_at`, `created_at` (answered as `lastModified`). Index
  `(stack_id, workspace, created_at)` serves `searchManagedStateVersions`.
- **Objects.** Each version is its own object in the `states` store (decision
  [0005](0005-persistence-postgresql.md)) under
  `<account>/<stack ulid>/<sha256 of workspace name>/<version ulid>`, written once and
  never overwritten. History does not depend on bucket versioning.
- **Sealing.** Every object is sealed in the sealed-object format of decision
  [0013](0013-secrets-at-rest.md), under its own content key held in
  `blob_objects.content_key_sealed`, so rotating data keys never rewrites a state object.
  Plaintext state exists only in server memory while streaming and on the worker.

### Writing a version

1. A `blob_objects` row for the new object is inserted as `pending` (decision
   [0005](0005-persistence-postgresql.md)). `POST` then streams the body through an MD5 and SHA-256 hasher, a size limit
   (`state.max_size`, default 512 MiB, `413` beyond) and the sealer into the object store.
2. A `Content-MD5` header that disagrees with the body answers `400`.
3. The body must be JSON. A body with `version`, `serial` and `lineage` is a plain state and
   those are recorded. A body without `serial` and `lineage`, as OpenTofu's client-side
   encryption produces, is stored as `client_encrypted` and never parsed again
   ([16 section 9](../reference/16-state-management.md#9-opentofu-state-encryption)).
4. When the SHA-256 equals the current version's, no version is added and the object is
   deleted: a run with no changes creates none
   ([16 section 6.2](../reference/16-state-management.md#62-behaviour)).
5. Otherwise one transaction marks the object `live`, inserts the version row, moves
   `current_version_id` and enqueues `state.indexed` (decision [0011](0011-persistent-work-queue.md)) for consumers
   that read state, such as the resources view.
6. An object whose transaction never commits keeps its `pending` row, and decision
   [0005](0005-persistence-postgresql.md)'s cleanup deletes both after 24 hours, so no
   bucket listing is needed.

The server does not compare lineage or serial: Terraform enforces both on the client, and
`terraform state push -force` from a task must keep working.

### Reading

`GET` opens the current version's object, unseals while streaming, and answers `200` with
`Content-Type: application/json` and `Content-MD5`. A workspace with no version answers
`204`, which Terraform reads as no state.

### Upload, import and download URLs

- **`stateUploadUrl`** answers `objectId` (a ULID), `url` (`/state/v1/uploads/{token}`,
  absolute) and an empty `uploadHeaders`. The token is `(objectId, account,
  expiry)`, signed with HMAC-SHA256 under `secrets.DeriveKey("state-upload-url")`
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
- **Retention.** Non-current versions are kept until `state.noncurrent_retention` passes
  (default `0`, meaning forever). The current version and versions referenced by a
  non-terminal run are never deleted. Rolling back to a deleted version answers `404`,
  problem `not-found`.
- **Stack deletion** with resources kept deletes the stack's version rows and objects only
  after the stack row is gone, through a `state.purge` job.

### Errors on the backend paths

The backend paths answer what Terraform's `http` client expects, not problem details:

| Status | When | Body |
|---|---|---|
| `200` | success | state, or empty |
| `204` | `GET` with no state | empty |
| `400` | bad `Content-MD5`, body not JSON, `LockInfo` not JSON | plain text |
| `401` | missing or invalid credentials, with `WWW-Authenticate: Basic realm="infraweave-state"` | plain text |
| `403` | valid credentials without the right in the table above | plain text |
| `404` | unknown stack or workspace, or the stack does not manage state | plain text |
| `405` | any other method | plain text |
| `413` | body over `state.max_size` | plain text |
| `423` | lock conflict | the holder's `LockInfo` JSON |

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
  serialisation point for runs.
- **Departures from Spacelift**, for the deviations register:
  - External access uses the `http` backend, not the `remote` backend.
  - Upload and download URLs point at infraweave, not at the object store.
  - Planning takes a shared lock; Spacelift's lock kind is undocumented.
  - A run's locks are released when it ends, so a stopped run leaves no stuck lock.
  - Non-current versions are kept forever by default, where SaaS keeps 30 days.
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

## Verification

- `internal/statebackend` unit tests: the authorization table per run state; every lock
  row of the conflict table; lock release on terminal transitions; `Content-MD5`
  mismatch; the size limit; identical writes adding no version; client-encrypted bodies;
  token expiry for the upload and download URLs.
- Store contract tests against PostgreSQL and both blob drivers (decision 0016): versions,
  current pointer, orphan collection through `pending` `blob_objects` rows, retention.
- Gherkin acceptance with real Terraform and OpenTofu in testcontainers:
  `state_backend.feature` (init, plan, apply through the backend; a second apply blocked by
  `423` until the first ends), `state_external_access.feature` (`terraform_remote_state`
  with and without the permissions and the stack switch), `state_import.feature`,
  `state_rollback.feature`, and `state_terragrunt_units.feature` with workspace unlock.
- A sealing test writes a state with a known secret output and asserts the bytes in the
  object store do not contain it.

## References

- [16: State management](../reference/16-state-management.md).
- [24 section 3.2: object storage buckets](../reference/24-self-hosted-reference.md#32-object-storage-buckets).
- [27 section 5: Digger (OpenTaco)](../reference/27-reference-materials.md#5-digger-opentaco).
- [14 section 3.3: the action catalogue](../reference/14-spaces-rbac-and-identity.md#33-the-action-catalogue).
- Terraform `http` backend: <https://developer.hashicorp.com/terraform/language/backend/http>;
  the client at `hashicorp/terraform` `b32cc59736f3` and `opentofu/opentofu` `f0af18832521`,
  `internal/backend/remote-state/http/client.go` (lock, unlock and `ID` query behaviour).
