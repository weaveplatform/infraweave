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
- `{workspace}` is a Terragrunt unit's workspace name: the unit's path from the project
  root with `/` separators, escaped as one path segment (`/` as `%2F`), at most 512 bytes
  unescaped. A unit at the project root uses the default workspace, the stack's own URL.
- `DELETE` and any other method answer `405`. Terraform's `http` backend has no workspace
  support of its own, so nothing calls them.

### How a run reaches it

- **Backend file.** For a stack with managed state, the runner (decision
  [0008](0008-run-execution-and-worker-protocol.md)) writes `infraweave_backend.tf` at the
  project root with a `backend "http"` block naming `address`, `lock_address` and
  `unlock_address` (all the stack URL), `lock_method = "LOCK"` and
  `unlock_method = "UNLOCK"`. Code that declares its own backend fails initialization, as
  in Spacelift ([16 section 1.1](../reference/16-state-management.md#11-how-a-run-reaches-it)).
- **Terragrunt units.** For a Terragrunt stack, the runner writes the same file into each
  unit's directory, naming the unit's workspace URL
  ([16 section 8](../reference/16-state-management.md#8-terragrunt-units-and-workspace-locks)).
  Terragrunt copies a unit's directory into its cache before it runs the tool, so the file
  goes with it. A unit that declares its own backend or `remote_state`, or whose name is
  over 512 bytes, fails initialization naming the unit.
- **Which stacks manage state.** A stack manages state when created with `manageState`.
  For a Terragrunt stack, `useStateManagement` decides when it is set and `manageState`
  when it is null, as the Terraform provider documents
  ([16 section 2.1](../reference/16-state-management.md#21-where-the-flag-lives)); the
  stack answers the effective value. Either is fixed at creation:
  `terragrunt.use_state_management` in a run's runtime configuration changes nothing.
- **Credentials.** The username is `run` and the password is the run token (decision
  [0006](0006-authentication-and-tokens.md)), passed through `TF_HTTP_USERNAME` and
  `TF_HTTP_PASSWORD` in the phase environment, never written into the workspace, so the
  token is not in a saved workspace or a log.
- **Lock timeout.** The runner passes `-lock-timeout=5m` to commands that lock (the
  launcher's setting `launcher.state_lock_timeout`, which the handoff carries to the
  runner, since the runner reads no configuration of its own). The stack environment variable
  `INFRAWEAVE_DISABLE_STATE_LOCK`, the counterpart of Spacelift's `SPACELIFT_DISABLE_STATE_LOCK`
  (report [13](../reference/13-configuration.md#22-user-tunable-spacelift_-variables)), makes the
  runner pass `-lock=false` to `plan`.

### Who may call what

A request authenticates with HTTP Basic, and the username names what the password
carries:

- **`run`**: a run token (decision [0006](0006-authentication-and-tokens.md)).
- **`api`**: an API key as `<id>:<secret>`, or a bearer token decision 0006 accepts on the
  API: an API, CLI, Terraform CLI or in-run token. A password holding a colon is an API
  key, since no token holds one. An API key is exchanged for a token as the registry
  exchanges one (decision 0014), and the replica keeps the token in memory, by the key's
  ID and the SHA-256 of its secret, never the secret, until 30 seconds before it expires,
  so a client's requests share one session. Disabling or deleting the key revokes that
  session, so a revoked key stops reading as any revoked token does: at once on every
  replica, and within 60 seconds if a notification is lost (decision 0006).

Any other username, or a password that is neither, answers `401`.

| Caller | `GET` | `LOCK`, `UNLOCK` | `POST` |
|---|---|---|---|
| Run token, run in `INITIALIZING`, `PLANNING` | yes | shared lock | no |
| Run token, run in `APPLYING`, `DESTROYING`, `PERFORMING` | yes | exclusive lock | yes, holding the exclusive lock |
| Run token, any other state, or a terminal run | no | no | no |
| In-run token, the run's own stack | no | no | no |
| Run token or in-run token of a run on another stack, run in one of the states above, stack with external access on, the run's stack holding `STACK_STATE_READ` and `STACK_STATE_DOWNLOAD` on the stack's space through its role bindings | yes | no | no |
| Run token or in-run token of a run on another stack, any other case | no | no | no |
| API key or bearer token, stack with external access on, caller holds `STACK_STATE_READ` and `STACK_STATE_DOWNLOAD` on the stack's space (decision [0007](0007-authorization-spaces-and-roles.md)) | yes | no | no |
| API key or bearer token, any other case | no | no | no |

- The run's state is read on every request, so a run that ends or is stopped loses access
  at once, whatever the token's expiry ([16 section 1.1](../reference/16-state-management.md#11-how-a-run-reaches-it)).
- External access is read-only and requires managed state
  ([16 section 3](../reference/16-state-management.md#3-external-state-access)). A user's
  `terraform_remote_state` data source uses `backend = "http"` with the stack URL, the
  username `api` and an API key's `<id>:<secret>`, or a token, as password.
- **A run reads another stack's state as its stack.** The stack's role bindings in the
  target's space decide, as they decide its in-run token's API calls (decision 0007), and
  the target's owner opts in with external access. Both of the run's tokens get the same
  answer, so a `terraform_remote_state` with `backend = "http"` and no credentials in the
  code reads in a run: the `http` backend takes `TF_HTTP_USERNAME` and
  `TF_HTTP_PASSWORD`, which the runner sets to the run token, as its defaults. No run locks
  or writes another stack's state.
- **Answers to every caller but a run on its own stack**, in order: `401` for a missing or
  invalid credential or another username; `404` for a stack the caller cannot see
  (decision 0007) or one without managed state; then `403`, for an in-run token on its
  own stack, any method but `GET`, external access off, either action missing, and a run
  whose state allows it no read, in that order. A `403` for a missing action names it, as
  the API's do.
- The same rules hold on a Terragrunt unit's workspace path.

### Locks

- **Table `state_locks`**: `(account_id, stack_id, workspace, lock_id)` key, `mode` (`shared` or
  `exclusive`), `run_id`, `triggered_by` (who triggered the run), `info` (the `LockInfo`
  JSON Terraform sent, at most 16 KiB), `created_at`. The lock ID is the `ID` field of the
  `LockInfo` body.
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
  none is held, `200`, so a repeated release is idempotent, unless the lock was broken
  from under the run that sends it (below), which answers `423` with `{}`. A client sends back the
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
- **Broken locks.** A lock released from under the run that holds it, by a force-unlock
  or by the operator release below, is recorded in table `state_broken_locks`:
  `(account_id, stack_id, workspace, lock_id, run_id)` key, `created_at`, cascading with
  its workspace. That run's own `UNLOCK` of it answers `423` with `{}`, so the run learns
  that it no longer held the lock it wrote under, while an `UNLOCK` of a lock no one ever
  held still answers `200`. The rows go with the run's locks when its terminal
  `run_updated` arrives, and with the stack and the account in their purges.
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
  need `STACK_WORKSPACE_UNLOCK` on the stack's space and delete, in one transaction,
  every lock row of one workspace or of every locked workspace of a stack. Unlocking a
  free workspace answers it unchanged, and `tofuWorkspaceUnlockAll` answers in
  `unlockedWorkspaces` only those it unlocked. Each release records the audit action
  `stack.state_lock_break` (decision 0015) with the caller as actor, the caller's address
  as the entry's remote IP, the workspace's ID and each released `LockInfo`. Each released
  lock is recorded as broken, so the holding run's next write, and its own `UNLOCK`,
  answer `423`.

### Storage

- **Table `state_workspaces`**: `id` (ULID, set when the stack is created with managed
  state, in the creation's transaction, or when a Terragrunt unit's first `LOCK` creates
  its row);
  `(account_id, stack_id, name)` unique, `name` empty for the stack's default workspace;
  `current_version_id`; `created_at`. It is the `TofuWorkspace` entity:
  - the default workspace answers the name `default`;
  - `isLocked` and `lockedBy` are derived from `state_locks`, `lockedBy` naming the login
    of the user, or the API key, that triggered the holding run, or the oldest holder's
    when shared locks overlap;
  - a `tofu_workspace` subject resolves to its stack's space for authorization
    (decision 0007).
- **Table `managed_state_versions`** (report [28 section 12.3](../reference/28-data-model.md#123-stacks-and-runs)):
  `id` (ULID), `account_id`, `stack_id`, `workspace`, `object_key`, `size`, `sha256`,
  `serial`, `lineage`, `client_encrypted`, `run_id` (null for imports; a rollback copies
  its target's), `rollback`,
  `rollback_username`, `rolled_back_from`, `imported`, `imported_username`,
  `imported_at`, `created_at` (answered as `lastModified`). Index
  `(account_id, stack_id, workspace, created_at)` serves `stackSearchManagedStateVersions`
  and the version's key to its workspace.
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

- **The tokens.** A URL's token is `kind | data key ID | account | subject | target |
  expiry`, signed with HMAC-SHA-256 under a key derived for its kind
  ([0013](0013-secrets-at-rest.md)): `crypt.DeriveKey("state-upload-url")` for uploads, whose
  target is the object ID and whose subject is the principal that asked; and
  `crypt.DeriveKey("state-download-url")` for downloads, whose target is the stack and the
  version current when the URL was signed. A token verifies through `crypt.DerivedKey`,
  so one signed before a data key's rotation works until it expires, which is 15 minutes
  after signing (`objectstore.signed_url_ttl`). A token is unpadded base64url, decoded
  strictly so that it has one spelling: a lenient decoder ignores the unused bits of the
  last character. A bad, expired or other-kind token answers `403`, problem `forbidden`,
  as `/blobs/{token}` does. The two routes take no credential, since the token is one.
- **`stateUploadUrl`** answers `objectId` (a ULID), `url` (`/state/v1/uploads/{token}`,
  absolute) and an empty `uploadHeaders`.
  - The `PUT` declares its `Content-Length` (`400`, `invalid-request`, without one) and is
    at most 100 MiB (`413`, `payload-too-large`); any `Content-Type` is accepted, since
    the Terraform provider sends `application/json` and the body is checked at import.
  - `state` seals the body as it streams, in the sealed-object format the `states` store
    uses, and hashes it, then writes it to the `uploads` store under
    `<account>/state/<object ID>/<sha256 of the subject>`, so a state's plaintext never
    rests in `uploads`, which stays unsealed for local-preview archives sealed to their
    pool ([0013](0013-secrets-at-rest.md)), and an import finds the upload only under
    the principal that asked for it.
  - A token takes one upload: a second `PUT` answers `409`, `invalid-state`, whether the
    first completed or was cut short, since the object's key is taken when its upload
    begins. A client whose upload failed asks for a new URL, which costs nothing.
  - An upload not imported within a day expires through `blob.expire`.
- **Import** (`stackCreate` with `stackObjectID`, `stackManagedStateImport`) checks, in
  order:

  | Check | At creation | Later |
  |---|---|---|
  | The caller holds `STACK_MANAGED_STATE_IMPORT` on the stack's space | `403`, `forbidden` | `403`, `forbidden` |
  | The stack manages state | `422`, `validation-failed`, `/stackObjectID`, reason `rule` | `409`, `invalid-state` |
  | The caller holds the stack's lock (`stackLock`) | not checked | `409`, `invalid-state` |
  | No tracked run, task or destroy run on the stack has ended yet; proposed runs write no state and do not count | not checked | `409`, `invalid-state`, the run in the `detail` |
  | The upload exists and the caller asked for it | `422`, `/stackObjectID`, reason `not-found` | `422`, `/state`, reason `not-found` |
  | It is at most 100 MiB | `422`, reason `maximum` | `422`, reason `maximum` |
  | It is a JSON state with `version` 4, or a client-encrypted envelope | `422`, reason `rule` | `422`, reason `rule` |

  - A client-encrypted envelope is a JSON object with `encrypted_data` and
    `encryption_version` and neither `serial` nor `lineage`, as OpenTofu's state
    encryption writes it. A plain state is checked for `version` 4 at import alone; a
    run's write takes any plain state.
  - **At creation**, `stackCreate` decides the first two checks itself, in its
    transaction before it writes the stack: `STACK_MANAGED_STATE_IMPORT` on the new
    stack's space beside the route's `STACK_CREATE`, as a move decides its second action,
    and that the stack keeps its state, as `manageState` or a Terragrunt stack's
    `useStateManagement` decides. The upload's checks and the import run through
    `stacksapi.ManagedState`, the one port through which `stackCreate` reaches `state`,
    and which opens the default workspace (§ "Storage"), with the caller and the object
    ID. `state` implements it and the assembly wires it in, as `stacksapi.StackGuard` is
    ([0004](0004-repository-architecture.md)). `stackCreate` calls it inside the
    creation's transaction, after the stack's row, so a failed check fails the create
    and nothing is created.
  - **Later**, the checks and the version's insert run in one transaction that holds the
    stack's row `FOR UPDATE`, which waits for `stacksapi.Hold` and keeps a run from being
    created until the import commits. The idle check reads `runsapi.StackRuns`, the
    runs domain's list of a stack's runs that have not ended, with their types.
  - Either way the import locks the upload's `blob_objects` row `FOR UPDATE` before it
    reads it, so of two imports of one upload the second waits and then finds it gone.
    A refused file leaves its resealed object `pending`, for `blob.expire`: the refusal
    rolls back the import's transaction, which a deletion would join, while the pending
    row is written outside it.
  - An import acts on the default workspace. It reseals the file into `states` as a new
    version with `imported`, `imported_username` (the caller's login, or its actor's ID
    without one) and `imported_at` set, even when it matches the current version, since an
    import is a user's act and is recorded as one.
    It then deletes the upload
    ([16 section 5](../reference/16-state-management.md#5-importing-state)).
  - It records the audit action `stack.state_import` (decision 0015), event type `UPDATE`
    on the stack, with the new version's ID.
  - The problem slugs are those of decision [0022](0022-api-errors-and-update-semantics.md);
    the `detail` names the failed check.
- **`stateDownloadUrl`** needs `STACK_STATE_READ` and `STACK_STATE_DOWNLOAD` on the stack's
  space, managed state and external access on, and answers a signed
  `/state/v1/download/{token}` URL for the default workspace's current version.
  - It answers, in order: `422`, `validation-failed`, for a `version` argument, as
    Spacelift's schema documents; `404`, `not-found`, for a stack the caller cannot read
    or whose state infraweave does not keep; `403`, `forbidden`, naming
    `STACK_STATE_READ`, which the handler decides since a route names one action;
    `403`, `forbidden`, with external access off; and `404`, `not-found`, for a stack
    with no state yet, since there is no version to sign.
  - The `GET` checks again that the stack exists, manages state and has external access
    on, but not the caller's actions, which the signature carries. It streams the signed
    version unsealed, or answers `404` when retention has deleted it.
- **No idempotency key.** The two URL operations ignore `Idempotency-Key`: each call
  signs a new URL, and a replay would answer one that may have expired
  ([0003](0003-api-conventions.md)). Each carries `x-infraweave-idempotency: ignored`,
  which the route table reads, so the idempotency middleware passes it by and the
  specification declares no `Idempotency-Key` and none of its conflicts.

### Rollback, searches and retention

- **`stackManagedStateRollback`** checks, in order: `STACK_MANAGED_STATE_ROLLBACK` (`403`),
  managed state, the caller's stack lock and an idle stack as import does (`409`), that the
  target version exists (`404`, `not-found`, also once retention has deleted it), and that
  it is not the current version (`409`, `invalid-state`). In the same transaction as
  import's, it unseals the target, reseals it as a new object and inserts a version that
  copies the target's `run_id`, `serial`, `lineage`, `imported`, `imported_username` and
  `imported_at`, with `rollback`, `rollback_username` and `rolled_back_from` set
  ([16 section 6](../reference/16-state-management.md#6-state-history-and-rollback)).
  A rollback to an imported version therefore answers its `importedTimestamp`. It records
  the audit action `stack.state_rollback`.
- **`Stack.isStateRollback`** is true while the default workspace's current version has
  `rollback` set; the next write clears it. `stacksapi` declares a read port that `state`
  implements, so the `Stack` view answers it with no edge from `stacks` to `state`.
- **Searches.** Each takes `SearchInput` with cursor paging, the `expensive` class and
  `SPACE_READ` reach (decision 0007):
  - `stackSearchManagedStateVersions` searches the default workspace's versions by
    `createdAt` (the default order, newest first), `run`, `rollback` and `imported`.
  - `searchTofuWorkspaces` searches by `name` (the default order, ascending), `stack` and
    `isLocked`; `searchTofuWorkspacesSuggestions` suggests `name` and `stack`. The Tofu
    Workspaces tag is gated by the `tofu-workspaces` feature (decision 0021).
- **Retention.** Non-current versions are deleted once `state.noncurrent_retention` (a
  duration, default `2160h`, 90 days; `0` keeps them forever) has passed since they
  stopped being current, which is the next version's `created_at`, so no column records
  it.
  - The `state.retention` periodic task (decision [0011](0011-persistent-work-queue.md))
    runs daily for the installation on the maintenance queue, deleting rows and then
    objects in batches of 500, as `state.purge` does.
  - It never deletes a workspace's current version, or a version whose `run_id` names a
    run that has not ended.
  - Rolling back to, or downloading, a deleted version answers `404`, problem
    `not-found`.
  - The `states` store takes no `objectstore.retention.states`: a store-wide expiry would
    delete current versions ([0005](0005-persistence-postgresql.md)).
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
| `401` | missing or invalid credentials, a username other than `run` or `api`, or another surface's credential, with `WWW-Authenticate: Basic realm="infraweave-state"` | problem `unauthenticated` |
| `403` | valid credentials without the right in the table above, external access off, or an empty-body `UNLOCK` or an `UNLOCK` of another run's lock from a caller not granted the exclusive lock | problem `forbidden` |
| `404` | unknown stack or workspace, or the stack does not manage state | problem `not-found` |
| `405` | any other method | problem `method-not-allowed` |
| `413` | body over `state.max_size`, or a `LockInfo` over 16 KiB | problem `payload-too-large` |
| `423` | lock conflict, or a `POST` without the run's exclusive lock | the holder's `LockInfo` JSON, or `{}` for a `POST` when no lock is held |

The shared controls' own refusals, such as `429` and a panic's `500`, are problems here as
on every surface.

The signed routes answer problems too: `403` for a bad, expired or other-kind token, `409`
`invalid-state` for a second complete upload, `413` for an upload over 100 MiB or without
`Content-Length` (`400`), and `404` for a download whose version retention deleted.

The public API operations (import, rollback, the URL operations, the searches, workspace
unlock) are ordinary REST operations and answer the problem details of decision
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
- **An API key as the Basic password.** A `terraform_remote_state` block keeps its
  credentials for as long as the code exists, and an API key's exchanged token lives
  10 hours (decision 0006), so the key itself is accepted and exchanged on the server, as
  the registry does for `terraform login`'s stored credentials.
- **A run reads another stack's state as its stack.** Spacelift lets a stack holding both
  state actions read another's state
  ([16 section 3](../reference/16-state-management.md#3-external-state-access)), and a
  run's tool sends the run token it already has. Evaluating that token as the run's stack
  keeps one rule for the stack's reach, its role bindings, rather than a second credential
  stored in a context, and the target's external access keeps the owner's opt-in. Only
  `GET` is granted, so the in-run restriction of decision 0007 still keeps every run from
  changing another stack's state.
- **Import at creation inside `stackCreate`.** A bad upload then fails the create with
  `422` and leaves no stack without the state it was meant to start from; importing from
  an event afterwards would let the create succeed and a first run plan against no state.
  A port that `state` implements keeps `stacks` from depending on `state`
  ([0004](0004-repository-architecture.md)).
- **Import and rollback need the caller's lock.** The lock is how a user takes a stack out
  of everyone else's hands (report [07](../reference/07-stack-model.md)); a lock another user
  holds means that user is working on the stack, so the state is not swapped under them.
- **An upload URL serves the principal that asked for it.** An object ID seen by another
  member of the account, in a log or a shared script, cannot import a state that member
  never saw.
- **Uploads bounded on the server, not declared.** Every URL infraweave signs for the
  object store binds a declared size and SHA-256, because a pre-signed `PUT` cannot bound
  what it is not told. A state upload reaches infraweave itself, which bounds it at
  100 MiB and hashes it as it streams, so `stateUploadUrl` keeps Spacelift's empty input
  and the Terraform provider's upload works unchanged.
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
  - A Terragrunt unit reads another stack's state as any run of its stack does, where
    Spacelift's units cannot.

## Constraints

- **Existing `remote` backend configurations** that read Spacelift state from outside must
  change to `backend = "http"` when moving to infraweave.
- **Client-side encrypted OpenTofu state** is stored opaquely: outputs, the resources view
  and anything else that parses state see nothing for those versions.
- **Unverified in Spacelift and decided here:** which run states may read and write; what
  counts as a pending run for import and rollback (C109); that `stackObjectID` and external
  access need managed state (C107); the URLs' 15 minutes and the upload's day (C108);
  rollback past retention and when `isStateRollback` clears (C110); that the backend's
  locks are the Tofu workspaces', that every stack with managed state lists its default
  workspace from its creation, and that a stack without it lists none (C111); a
  Terragrunt stack's `useStateManagement` deciding over `manageState` (C113); and state
  management fixed at creation whatever a run's runtime configuration says (C114). The
  scenarios assert these until the captures are recorded.
- **The KEK of decision [0013](0013-secrets-at-rest.md) is required to read any state.** A
  database and bucket backup without it restores no state.
- **A force-unlock breaks a running run's lock.** That run's own `UNLOCK` then answers
  `423`, and a write it attempts answers `423`, as Terraform's own force-unlock warns.

## Verification

- `internal/state` unit tests: the authorization table per run state; every lock
  row of the conflict table; lock release on a run's terminal `run_updated`; `423` for a
  run's `UNLOCK` of a lock broken from under it and `200` for a lock never held; every shared
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
  `423` until the first ends), `state/urls.feature` (the signed upload and download),
  `state/external_access.feature` (`terraform_remote_state` with an API key and a bearer
  token, a run of another stack reading with no credentials in its code, and each refusal),
  `state/import.feature` (at creation and later, client-encrypted included),
  `state/rollback.feature` (rollback, `isStateRollback`, the version search and retention),
  `state/terragrunt_units.feature` (a workspace per unit, listed and unlocked), and
  `state/force_unlock.feature` (`terraform force-unlock` from a task releases a lock left
  by a killed run).
- A retention test in virtual time (decision 0016): a non-current version is deleted once
  its retention passes, and the current version and a version a run that has not ended
  wrote are kept.
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
