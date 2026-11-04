# 0005: Persistence: PostgreSQL, embedded migrations and object storage

Status: Accepted

## Context

Every stateful feature needs a store, and several records wait on this one:

- [0003](0003-api-conventions.md#constraints) keeps idempotency keys and rate-limit
  buckets in memory until a persistence decision exists, so it works for one
  replica only.
- Report 28 derives about a hundred tables from the wire entities and their
  access paths ([28 section 12](../reference/28-data-model.md#12-storage-tables-i)).
  It leaves the engine, key types, naming and migrations to this record. Runs alone
  need three orderings at once: by stack in time order, by pool in scheduling order,
  and by workflow
  ([28](../reference/28-data-model.md#what-this-means-for-infraweave)).
- The queue ([0011](0011-persistent-work-queue.md)) needs its jobs in the same
  database as the changes that create them, so that both commit together.
- Large payloads (run logs, state files, plans, workspaces, module archives,
  webhook bodies) don't belong in table rows. Self-hosted Spacelift keeps them in
  eleven buckets with their own retention rules, and browsers and workers reach
  them only through pre-signed URLs
  ([24 section 3.2](../reference/24-self-hosted-reference.md#32-object-storage-buckets)).
- Self-hosted Spacelift's only mandatory data stores are PostgreSQL 14 to 18
  and object storage. Its queue, encryption and worker broker each have a form built on
  those two
  ([24 section 3](../reference/24-self-hosted-reference.md#3-external-dependencies)).
- infraweave runs as several stateless replicas behind a load balancer, so
  anything that affects correctness has to be shared through the stores.

## Decision

### Engine and versions

- **PostgreSQL is the only database.** No SQLite, MySQL or in-memory store ships
  as a production backend.
- **Version floor: the newest major that all three managed services offer
  generally available.** The services are Amazon RDS for PostgreSQL, Azure
  Database for PostgreSQL flexible server and Cloud SQL for PostgreSQL. The
  floor is PostgreSQL 18:

  | Service | PostgreSQL 18 | PostgreSQL 19 |
  |---|---|---|
  | Amazon RDS | generally available since 2025-11-14 ([announcement](https://aws.amazon.com/about-aws/whats-new/2025/11/amazon-rds-postgresql-major-version-18)) | preview environment only ([release notes](https://docs.aws.amazon.com/AmazonRDS/latest/PostgreSQLReleaseNotes/postgresql-versions.html)) |
  | Azure flexible server | generally available since November 2025 ([announcement](https://techcommunity.microsoft.com/blog/adforpostgresql/-/4469802), [supported versions](https://learn.microsoft.com/en-us/azure/postgresql/configure-maintain/concepts-supported-versions)) | not offered |
  | Cloud SQL | generally available since 2025-11-20 ([release notes](https://docs.cloud.google.com/sql/docs/postgres/release-notes)) | not offered |

  The PostgreSQL project plans 19's general availability for 2026-11-19
  ([19 RC 1 announcement](https://www.postgresql.org/about/news/postgresql-19-rc-1-released-3409/)).
- **When the floor rises.** The first infraweave release after all three
  services offer the next major generally available raises the floor to it,
  and its release notes say so. A database below the floor makes every process
  mode that opens it refuse to start, with a message naming the floor; the
  check reads `server_version_num` when the connection pool opens.
- **Tested versions.** CI gates on the floor. The next major, as a beta or
  release candidate until it is generally available, runs the same suites in a
  job that reports but does not block
  ([0016](0016-testing-and-acceptance.md#ci)).
- **No extensions.** The schema uses only core features, so it runs on any
  managed PostgreSQL service without asking for an extension allowlist. Among
  the core features below the floor, the schema relies on `UNIQUE NULLS NOT
  DISTINCT` and `MERGE`.

### Driver and access

- **Driver.** `github.com/jackc/pgx/v5` with `pgxpool`, used natively rather
  than through `database/sql`, so `LISTEN`, `COPY` and batch pipelining are
  available.
- **SQL.** SQL is hand-written beside the store code in `internal/store` and
  its per-family subpackages. Rows are mapped with `pgx.CollectRows` and
  `pgx.RowToStructByName`. There is no ORM and no query builder.
- **Connections.**

  | Setting | Default | Meaning |
  |---|---|---|
  | `database.url` | required | Primary connection string |
  | `database.read_url` | unset | Optional replica, used only for reads that tolerate lag (lists, searches, audit queries); never inside a write, an idempotent request or a worker poll |
  | `database.max_conns` | 20 | Pool size per replica |
  | `database.statement_timeout` | 30s | Set on each connection |
  | `database.idle_in_transaction_timeout` | 60s | `idle_in_transaction_session_timeout` on each connection |
  | `database.iam_auth` | `none` | `aws`, `azure` or `gcp`: pgx's `BeforeConnect` hook fetches a fresh token for each new connection and uses it as the password. `aws` signs an RDS IAM token, as self-hosted Spacelift does ([24 section 3.1](../reference/24-self-hosted-reference.md#31-postgresql)); `azure` takes an Entra token for Azure Database for PostgreSQL from `azidentity`'s default chain; `gcp` takes a Cloud SQL IAM database authentication token from Application Default Credentials. Every value but `none` requires `sslmode` `require`, `verify-ca` or `verify-full` in `database.url`, and fails the start otherwise, because the token is a bearer credential |
  | `database.migrate` | `auto` | `auto` applies pending migrations at start; `check` refuses to start while any are pending |

- **No Cloud SQL connector.** `gcp` connects to the instance's address over
  TLS like any PostgreSQL server; the Cloud SQL Go connector would add a
  second connection path and a dependency that the token alone makes
  unnecessary.
- **One listening connection.** Each replica holds one dedicated connection
  outside the pool for `LISTEN`. It reconnects with backoff. After a reconnect,
  every waiter re-checks its condition, because notifications sent while the
  connection was down are lost.

  | Channel | Payload | Used by |
  |---|---|---|
  | `infraweave_jobs` | queue name | [0011](0011-persistent-work-queue.md): wakes idle queue workers |
  | `infraweave_runs` | worker pool ID | [0008](0008-run-execution-and-worker-protocol.md): wakes held worker polls |
  | `infraweave_vcs_agent` | VCS agent pool ID for a new request, or the request ID for its response | [0012](0012-vcs-integration-model.md): hands proxied agent requests and responses between replicas |
  | `infraweave_invalidate` | cache key | in-process caches (rules below) |
  | `infraweave_events` | event ID | [0015](0015-events-notifications-webhooks-audit.md): wakes server-sent-event streams |
  | `infraweave_logs` | run ID, state and state version | [0015](0015-events-notifications-webhooks-audit.md): wakes run log streams |

### Schema conventions

| Concern | Rule |
|---|---|
| Identifiers | ULIDs generated in Go (`github.com/oklog/ulid/v2`, monotonic within a millisecond), stored in `uuid` columns and rendered as 26-character ULID text on the wire. Runs, worker pools and stacks carry the ULIDs that report 28 records ([28 section 1.2](../reference/28-data-model.md#12-identity-and-tenancy)) |
| Slugs | Slug-addressed entities (stacks, modules, spaces, contexts, policies, templates, blueprints) keep a ULID key and a `slug text` with `unique (account_id, slug)`. A reslug updates one column; foreign keys reference the ULID |
| Tenancy | Every account-scoped table carries `account_id uuid not null`, and its primary or unique keys lead with it. One installation hosts many accounts ([0007](0007-authorization-spaces-and-roles.md)) |
| Spaces | Space-scoped tables carry `space_id uuid not null` with an index. The space tree and reachability are [0007](0007-authorization-spaces-and-roles.md)'s |
| Time | `timestamptz` only. Conversion to the Unix seconds that Spacelift's fields use happens in handlers. The database clock (`now()`) is the clock for leases, expiries and timers |
| Enumerations | `text` with a `CHECK` constraint, not PostgreSQL enum types, so adding a value is an ordinary migration |
| Documents | `jsonb` for vendor configuration, runtime configuration and other nested inputs that are read whole |
| Labels | `text[]` with a GIN index, matched with `@>` and `&&` for auto-attachment and filters ([28 section 12.1](../reference/28-data-model.md#121-shapes-that-recur)) |
| Polymorphic owners | One nullable foreign-key column per owner kind (`stack_id`, `context_id`, ...) with a `CHECK` that exactly one is set. Report 28's `owner_kind` and `owner_id` sketch would lose real foreign keys and cascades. An owner whose domain sits below the attachment's in [0004](0004-repository-architecture.md)'s graph keeps the rows itself: a registry module's configuration elements and attachments are `registry`'s, referencing the context, policy or integration |
| Attachments | Their own ULID, a unique `(left, right)` constraint and `is_autoattached`, as report 28 derives |
| Soft delete | `deleted_at timestamptz` only where the schema exposes `deleted` (stacks, worker pools, template versions). Everything else is deleted, with `ON DELETE` rules chosen per foreign key inside a domain; a key that crosses domains follows [0004](0004-repository-architecture.md)'s dependency direction and never cascades |
| Secrets | `bytea` columns named `<name>_sealed`, written and read only through [0013](0013-secrets-at-rest.md) |
| Foreign keys | Every foreign key column is indexed |
| Optimistic concurrency | Mutable aggregates with concurrent writers carry `version bigint`. A conditional `UPDATE ... WHERE version = $n` detects a lost update. Runs use `state_version` ([0008](0008-run-execution-and-worker-protocol.md)) |

A Flows organization, the top of Spacelift Flows' hierarchy, is an infraweave
account, and Flows keeps its own identifiers
([28 section 12.11](../reference/28-data-model.md#1211-flows),
[0018](0018-flows-runtime.md)). Its tables live
in the same database, in the `flows` PostgreSQL schema that
[0018](0018-flows-runtime.md) sets.

### Migrations

- **Embedded streams.** Migrations are SQL files embedded with `embed.FS`, in
  one or more named streams. Decision 0004 places the streams: each domain
  has one. Within a stream, files are named
  `NNNNN_snake_case.sql` (five digits, gap-free). The composition root
  registers the streams in the domains' dependency order, and the runner
  applies them in that order, so a stream may refer to tables of the streams
  registered before it, along the foreign-key rule of
  [0004](0004-repository-architecture.md).
  The runner is infraweave's own, in `internal/store/migrations`, about two
  hundred lines.
- **Forward only.** There are no down migrations. A bad migration is corrected
  by a later one, or by restoring a backup.
- **Bookkeeping.**
  - `schema_migrations(stream text, version int, name text, checksum bytea, applied_at timestamptz, primary key (stream, version))`
    records each applied file with the SHA-256 of its contents.
  - `schema_state(stream text primary key, compatible_from int, release text)`
    records, per stream, the oldest version of that stream a running binary
    may know, and the infraweave release that last migrated it.
- **Applying at start.**
  1. Take `pg_advisory_lock` on a fixed 64-bit key, the FNV-1a hash of
     `infraweave.migrate`, so concurrent replicas apply each migration once.
     This session-level lock is the only advisory lock in infraweave.
  2. Compare the applied migrations with the embedded ones. A checksum mismatch
     on an applied version fails the start.
  3. Apply each pending file in its own transaction and record it in that
     transaction. A file whose first line is `-- infraweave:no-transaction` runs
     outside a transaction, for `CREATE INDEX CONCURRENTLY`. It must be
     re-runnable, using `IF NOT EXISTS`.
  4. Release the lock.
- **Mixed versions.** Release N's migrations keep release N-1's binary working:
  columns are added before they are used, and dropped one release after their
  last use (expand, then contract). A file may raise `compatible_from` with the
  header `-- infraweave:compatible-from NNNNN`, which names a version of its
  own stream.
- **Refusing to start.** A binary refuses to start, and says why, when:
  - a stream's `compatible_from` is greater than the binary's newest
    embedded version of that stream (the binary is too old);
  - an applied version that the binary should know is missing from its
    embedded set (the schema has diverged);
  - the upgrade would skip a major release. Each binary embeds an
    `upgrade_floor`: the newest migration of the last minor release of the
    previous major, per stream. A database where any stream's newest applied
    version is below its floor is refused, by `migrate up` and by `auto` alike, with a message naming the
    release to install first. Within a major, minors may be skipped. The
    upgrade rules are decision 0020's.
- **Operator commands.** `infraweave migrate up` applies migrations and
  `infraweave migrate status` lists them, for operators who migrate in a
  separate job, such as a Helm pre-upgrade hook, and run replicas with
  `database.migrate: check`.
- **Backfills.** A data rewrite that touches many rows is not done inside a
  migration. The migration adds the schema, and a `maintenance` job of
  [0011](0011-persistent-work-queue.md) does the rewrite in batches.

### Transactions

- **`store.InTx`.** `store.InTx(ctx, opts, fn)` runs `fn` in a transaction at
  `READ COMMITTED` unless `opts` asks for `SERIALIZABLE`.
- **Retries.** A transaction that fails with SQLSTATE `40001`
  (serialization failure) or `40P01` (deadlock) is retried up to three times,
  with 10 to 100 ms of jittered delay. So `fn` must have no effect outside the
  database. External effects are enqueued as jobs inside the transaction
  ([0011](0011-persistent-work-queue.md)) and happen after commit.
- **Row locks** use `SELECT ... FOR UPDATE`, taken in a fixed order:
  organization entitlement ([0021](0021-capability-scope-and-supersession.md#limits)),
  account, space, stack, run, worker. Code that needs two rows of one table locks them in
  ID order.
- **Long waits.** No request holds a transaction or a pooled connection while it
  waits for something outside the database. A held worker poll is the
  important case: it waits on a channel woken by `infraweave_runs`, then
  re-reads in a fresh transaction.

### The stores of 0003

0003's behaviour is unchanged. Its state moves into PostgreSQL, so every replica
sees the same keys, buckets and switches.

| 0003 concern | Table | Mechanics |
|---|---|---|
| Idempotency keys | `idempotency_keys(caller, key, fingerprint, state, locked_until, status, content_type, location, body_sealed, created_at, expires_at)`, primary key `(caller, key)`; the stored body is sealed ([0013](0013-secrets-at-rest.md)) because a replayed create can carry a secret shown once, such as a new API key | `INSERT ... ON CONFLICT DO NOTHING` claims the key as `in_flight` with `locked_until` set to now plus the request timeout. If the insert loses, the row it lost to answers the request: replay, `idempotency-key-reuse` or `idempotency-key-in-flight`. Finishing stores the response and sets `state = done`. A `5xx` deletes the row. An `in_flight` row whose `locked_until` has passed belongs to a crashed replica, and the next request with that key takes it over |
| Rate-limit buckets | `rate_limit_buckets(caller, class, tokens, refilled_at)`, primary key `(caller, class)`, `UNLOGGED` | One `INSERT ... ON CONFLICT DO UPDATE` per request refills the bucket by the elapsed time, takes one token when one is available, and returns what remains, all in one round trip. When no token is taken, the same statement returns the wait that `Retry-After` reports |
| Operator switch | `api_disabled_callers(caller, until, reason)` | Read through a per-replica cache that `infraweave_invalidate` clears, so a change takes effect at once on every replica |

- **Expiry.** A `maintenance` periodic task (`idempotency.purge`, hourly)
  deletes rows whose `expires_at` has passed.
- **Callers.** The caller is the authenticated principal from
  [0006](0006-authentication-and-tokens.md), and the client address only for
  unauthenticated routes.
- **`UNLOGGED` buckets.** The bucket table writes no WAL, so a crash empties it.
  The worst result is that every caller briefly has a full bucket, which costs
  far less than a WAL write on every request.

### Object storage

- **Interface.** `internal/objectstore` defines `objectstore.Store` with `Put`, `Get`, `Head`,
  `Delete` and `SignURL(method, key, ttl, constraints)`. The driver is
  `objectstore.driver`, and each driver's settings sit under
  `objectstore.<driver>`. Each driver uses its provider's own Go SDK, and
  none goes through another provider's compatibility API.

  | Driver | Backs | Notes |
  |---|---|---|
  | `s3` | AWS S3 and S3-compatible stores (RustFS, Ceph RGW, Cloudflare R2) | `github.com/aws/aws-sdk-go-v2/service/s3` with its upload manager for multipart. Settings: `bucket`, `endpoint`, `region` (default `us-east-1`), `path_style`, credentials from the SDK's default chain (including workload identity) or static keys, and server-side encryption (`none`, `s3` or `kms` with a key ID) |
  | `gcs` | Google Cloud Storage | `cloud.google.com/go/storage`. Settings: `bucket`. Credentials from Application Default Credentials; V4 signed URLs are signed through the IAM `signBlob` call when the identity holds no private key, so workload identity needs no static key |
  | `azureblob` | Azure Blob Storage | `github.com/Azure/azure-sdk-for-go/sdk/storage/azblob`. Settings: `account_url`, `container`. Credentials from `azidentity`'s default chain; signed URLs are user delegation SAS, so Entra workload identity needs no account key |
  | `fs` | A local directory or a shared POSIX volume | Writes go to a temporary file, are synced and are renamed into place. Signed URLs point at infraweave's own `GET` and `PUT /blobs/{token}`. The token carries method, key, expiry and size limit, and is signed with HMAC-SHA256 under `secrets.DeriveKey("objectstore-url")` of [0013](0013-secrets-at-rest.md). More than one replica needs a volume shared by all of them |

  The `gcs` and `azureblob` drivers exist because the reference deployments
  (decision 0020) authenticate with workload identity, and Google Cloud
  Storage's S3 interoperability API needs static HMAC keys.

- **One contract.** Every driver passes the same contract suite
  ([Verification](#verification)), which defines what a driver must do:

  | Area | What every driver does |
  |---|---|
  | Put, get, head, delete | Round-trips bytes and size; `Get` and `Head` of a missing key return `objectstore.ErrNotFound`; deleting a missing key succeeds |
  | Multipart | An object above `objectstore.part_size` (default 16 MiB) is written in parts: an S3 multipart upload, a GCS resumable upload in chunks, Azure staged blocks committed by a block list, or a temporary file on `fs`. A failed upload leaves no readable object, and the driver aborts what it started |
  | Checksums | Every write carries the SHA-256 that `blob_objects` records. `s3` sends it as `x-amz-checksum-sha256`, which the store verifies. `gcs` and `azureblob` have no SHA-256 the service verifies, so the driver hashes the stream as it uploads and, on a mismatch, fails the write and leaves the object under its `pending` row for `blob.expire` to delete; the service's own CRC32C (GCS) or CRC64 (Azure) still guards each transfer. No driver computes or sends MD5, because `GODEBUG=fips140=only` refuses it ([0013](0013-secrets-at-rest.md#fips-140-3)) |
  | Signed URLs | `GET` and `PUT` URLs that expire after their TTL and bind the method, key and declared size; a `PUT` URL on `s3` also binds the checksum |
  | Lifecycle | Expiry is infraweave's own (below): a `live` object past `expires_at` and a `pending` object older than 24 hours are deleted by `blob.expire`. Nothing relies on a bucket lifecycle rule or on bucket versioning |

- **`serve` never deletes.** `serve` holds the store as
  `objectstore.Writer`, the interface without `Delete`, and only
  `scheduler` holds the full `Store`, for `blob.delete` and `blob.expire`.
  So the identity `serve` runs as needs no delete permission on any bucket
  ([0020](0020-reference-deployments-and-packaging.md)), and a compromised
  API process cannot erase state versions or audit bodies. A write that
  fails after its object exists leaves the object to `blob.expire`.
- **Logical stores.** Each store is a key prefix in one bucket by default. Any
  store can be given its own bucket (`objectstore.buckets.<store>`), so its
  replication or encryption can differ.

  | Store | Holds | Written by | Default retention | Owner |
  |---|---|---|---|---|
  | `run-logs` | Run log chunks | Runner, through pre-signed `PUT` | 60 days | [0008](0008-run-execution-and-worker-protocol.md) |
  | `run-artifacts` | Plan files, plan JSON, change and output uploads | Runner | 30 days | [0008](0008-run-execution-and-worker-protocol.md) |
  | `workspaces` | Workspaces of runs awaiting confirmation or replan, encrypted to the pool key | Runner | 7 days after the run settles | [0008](0008-run-execution-and-worker-protocol.md) |
  | `states` | State versions, sealed | State backend | none | [0010](0010-state-backend.md) |
  | `modules` | Module and provider archives | Registry | none | decision 0014 |
  | `policy-inputs` | Sampled policy inputs, sealed | Policy engine | 7 days | [0009](0009-policy-engine-opa.md) |
  | `uploads` | Client uploads, such as a state file at stack creation or a local-preview workspace | API clients, through pre-signed `PUT` | 1 day | the owning family |
  | `deliveries` | Outbound webhook request and response bodies | Event delivery | `events.deliveries.retention` | [0015](0015-events-notifications-webhooks-audit.md) |
  | `inbox` | Inbound webhook bodies above the inline limit, and queue payloads above 64 KiB | VCS intake, queue | 2 days | [0012](0012-vcs-integration-model.md), [0011](0011-persistent-work-queue.md) |
  | `run-observability` | Worker traces and metrics artifacts | Runner | 60 days | [0008](0008-run-execution-and-worker-protocol.md) |
  | `flows-events` | Flows event bodies above the inline limit | Flows engine | `flows.retention` | [0018](0018-flows-runtime.md) |

  The defaults follow the retention that self-hosted Spacelift's docs suggest
  for the matching buckets; `run-artifacts` has no matching bucket, and its 30
  days is infraweave's own
  ([24 section 3.2](../reference/24-self-hosted-reference.md#32-object-storage-buckets)).
  Each one is set by `objectstore.retention.<store>`, except `deliveries`
  and `flows-events`, whose bodies are read through their rows and so live
  exactly as long as those rows, under the owner's setting. For deliveries
  that is 7 days, where Spacelift suggests 1 day for its bucket.

- **Keys.** A key is `<account>/<owner path>/<leaf>` within its store, and
  the bucket holds the object under `<store>/<key>`, also when the store
  has a bucket of its own, so moving a store copies objects without
  renaming them. `blob_objects` keeps the store and the key apart, and
  records name the store and write the key without the store prefix. The
  leaf is a ULID, or a name the owner never reuses, such as a log chunk's
  sequence number. An object is written once and never overwritten, so the store needs neither versioning nor
  read-after-overwrite consistency. A new version of anything, such as a state
  file, is a new object.
- **No bucket versioning.** infraweave neither needs nor uses it. An operator
  who turns it on for their own reasons also owns a lifecycle rule that expires
  non-current versions, because infraweave's deletes then leave them behind.
- **Rows and objects stay consistent through `blob_objects`.**
  - **The table.** `blob_objects(store, key, size, sha256, state, content_key_sealed, expires_at, created_at)`
    has primary key `(store, key)`. `state` is `pending`, `live` or
    `deleting`. Tables that reference an object have a foreign key to it.
  - **Writing.** A row is inserted as `pending` in its own short transaction
    before the upload, or before a pre-signed `PUT` URL is issued. The
    transaction that records the business change marks it `live`. A `pending`
    row older than 24 hours is deleted by `blob.expire`, together with any
    object under its key,
    so an upload whose transaction rolled back never leaves an orphan, and no
    bucket listing is ever needed.
  - **Deleting.** Deleting a business row sets its objects to `deleting` and
    enqueues a `blob.delete` job in the same transaction. The object goes after
    commit.
  - **Expiry.** A `maintenance` periodic task, `blob.expire` (every 10
    minutes), deletes objects whose `expires_at` has passed, 1,000 per batch.
    infraweave expires objects itself rather than relying on bucket lifecycle
    rules, so the `fs` driver, and buckets where the operator sets no rules,
    behave the same. A bucket lifecycle rule an operator adds is a harmless
    backstop when it is set longer than the store's retention.
- **Pre-signed URLs.**
  - **Lifetime.** 15 minutes by default (`objectstore.signed_url_ttl`).
  - **Uploads.** A `PUT` URL signs the expected `Content-Length` and, on
    `s3`, `x-amz-checksum-sha256`. Committing an upload checks the size with
    `Head`. On `s3` the store has already checked the SHA-256; on `gcs` and
    `azureblob` the commit reads the object back once and compares it, and a
    mismatch fails the commit and leaves the row `pending`; on `fs`
    infraweave's own `PUT` handler hashes while it writes.
  - **Who uses them.** Browsers, runners and launchers reach object storage
    only through these URLs. So the object store's endpoint must be reachable
    from them, as in self-hosted Spacelift.
- **Encryption.** Server-side encryption is the bucket's business. Stores whose
  content is sensitive whatever the bucket does (`states`, `policy-inputs`,
  `run-artifacts`, and state files in `uploads`) are also sealed by
  [0013](0013-secrets-at-rest.md), each object under its own
  content key in `blob_objects.content_key_sealed`. Workspaces are encrypted to the
  worker pool's key by the worker
  ([0008](0008-run-execution-and-worker-protocol.md)).

### Multiple replicas

1. **Shared state only.** Replicas share nothing but PostgreSQL and object
   storage. Any `serve` replica serves any request, including the worker API and
   webhook intake, and any `scheduler` replica takes any job (process modes
   are decision 0004's).
2. **Caches.** An in-process cache either lives at most 5 seconds or is
   cleared through `infraweave_invalidate`. Nothing that affects an
   authorization or idempotency decision is cached without invalidation.
3. **No leader election.** Work that must happen once is a unique job or a
   row claimed with `FOR UPDATE SKIP LOCKED`. That covers queue jobs, periodic
   tasks, cron firing and run assignment
   ([0011](0011-persistent-work-queue.md),
   [0008](0008-run-execution-and-worker-protocol.md)).
4. **One clock.** Leases, expiries, timers and `next_fire_at` compare against
   the database clock. Replica clocks never decide an outcome.
5. **Rolling upgrades.** Releases N and N-1 run side by side against one
   schema, under the migration rules above.
6. **Connection budget.** Each replica uses `database.max_conns` plus one
   listening connection. The deployment guides (decision 0020) size PostgreSQL's
   `max_connections` from the replica count.

## Rationale

- **PostgreSQL alone.** It is the one database self-hosted Spacelift requires,
  so operators who run Spacelift today already run it. It gives `SKIP LOCKED`,
  `LISTEN/NOTIFY`, transactional DDL and `jsonb`, and those let the queue, worker
  wake-ups and migrations live in one dependency.
  - **Rejected: SQLite as a second backend**, as artifactweave has. Every
    store would need a second contract implementation, and the queue and worker
    poll would need a second locking design. That doubles the coverage burden,
    for an install shape that self-hosted Spacelift doesn't offer either.
  - **Rejected: MySQL and CockroachDB.** Neither offers the same mix of
    `SKIP LOCKED`, `LISTEN` and transactional DDL. CockroachDB also changes
    retry and isolation behaviour throughout the code.
- **The floor is the newest major the managed services all offer.** The
  maintainer's rule is that the minimum is the latest version available.
  Tying "available" to the three managed services keeps every reference
  deployment of decision 0020 able to create a database at the floor, and a
  floor that rises one release after the slowest service keeps the gate on
  one major while the next one is already exercised.
  - **Rejected: the oldest community-supported major** (15 today). It
    widens the tested range to four majors and keeps the oldest one gating
    every change.
  - **Rejected: the newest community release.** RDS offers PostgreSQL 19
    only in preview, so the AWS reference deployment could not be created
    at that floor.
  - **Departure from Spacelift**, whose self-hosted install accepts 14 to 18
    ([24 section 3.1](../reference/24-self-hosted-reference.md#31-postgresql)).
- **No extensions.** `ltree`, `pgcrypto` or `pg_partman` would simplify a few
  tables. But each is a request to the operator's database team, and some
  managed services restrict them. The space tree uses a closure table
  ([0007](0007-authorization-spaces-and-roles.md)), and encryption happens in
  the application ([0013](0013-secrets-at-rest.md)).
- **pgx natively, SQL by hand.**
  - The queue's claim query, keyset pagination (0003's
    `(sort_key, id) > cursor`), and conditional updates on `state_version` are
    SQL that an ORM hides or fights.
  - **Rejected: an ORM** (gorm, ent).
  - **Rejected: sqlc.** It checks queries against the schema at build time,
    which is valuable. But it adds a code generator whose output must either
    be covered by tests or excluded from the coverage gate. The store contract
    suites, which run every query against real PostgreSQL, catch the same
    errors.
- **ULIDs in `uuid` columns.** Spacelift's run and worker pool IDs are ULIDs,
  and its slug-addressed stacks carry a ULID behind the slug, so generating
  them in Go keeps wire and storage identical. The `uuid` type stores them in
  16 bytes and sorts them by creation time, which keyset pagination uses.
- **An own migration runner.**
  - **Rejected: golang-migrate.** A failed migration leaves it in a "dirty"
    state that an operator has to force by hand.
  - **Rejected: goose.** It works through `database/sql`, while infraweave
    uses pgx natively.
  - **Rejected: down migrations.** They are rarely correct for data and are
    never exercised in production. Forward fixes and backups are the recovery
    path.
  - The two header directives cover the two needs a library would bring:
    non-transactional index builds and mixed-version safety.
- **Migrations at start by default.** Self-hosted Spacelift migrates on
  startup ([24 section 2.1](../reference/24-self-hosted-reference.md#21-process-inventory)),
  and a single command is the simplest install. `check` mode is for operators
  who must separate schema changes from rollouts.
- **0003's stores in PostgreSQL rather than Redis.**
  - **Rejected: Redis.** It would add a third dependency for two small
    tables.
  - **Idempotency.** The key insert is a cheap primary-key write.
  - **Rate limits.** The bucket upsert is one statement on an `UNLOGGED`
    table.
  - **Rejected: per-replica buckets that divide the budget by the replica
    count.** They would make `X-RateLimit-Remaining` wrong whenever the load
    balancer is uneven.
- **One bucket with prefixes by default.** Eleven buckets are eleven pieces of
  infrastructure to provision before a first run. This departs from
  self-hosted Spacelift's one bucket per purpose. The per-store override keeps
  separate buckets available where policy requires them.
- **infraweave expires and versions objects itself.** This departs from
  Spacelift, which relies on bucket lifecycle rules and asks for bucket
  versioning on the states bucket
  ([24 section 3.2](../reference/24-self-hosted-reference.md#32-object-storage-buckets)).
  - Immutable keys plus `blob_objects` make retention and state history
    database facts, which the API can list and test.
  - The `fs` driver and minimal S3-compatible stores need no lifecycle or
    versioning support.
- **Four drivers on native SDKs, one contract.** The maintainer's rule is
  that infraweave is not locked to an Amazon S3 backend. A single S3 driver
  would push Google Cloud and Azure installations onto static HMAC keys or an
  S3 gateway; native drivers keep workload identity on every cloud. The
  shared contract suite keeps four drivers from drifting apart.
- **SHA-256, not MD5, for object integrity.** SHA-256 is the digest
  `blob_objects` already records, and Go's `fips140=only` mode refuses MD5
  ([0013](0013-secrets-at-rest.md#fips-140-3)).
- **Rejected: SQS as an alternative queue.** Self-hosted Spacelift offers it
  beside its Postgres queue
  ([24 section 3.3](../reference/24-self-hosted-reference.md#33-message-queues)).
  Offering only one queue is a departure, and it keeps transactional enqueue,
  which SQS can't join.

## Constraints

- **Connections.** PostgreSQL's connection limit bounds the replica count.
  A pooler in transaction mode (PgBouncer) breaks `LISTEN` and session advisory
  locks, so the listening connection and migrations must bypass it.
- **Encryption keys.** A database backup holds sealed values that are useless
  without the key encryption key of [0013](0013-secrets-at-rest.md). Backups of
  the two must be kept together.
- **What disaster recovery needs.** The `states`, `modules`, `policy-inputs`
  and `run-logs` stores, together with the database, are the data whose loss
  matters. Losing the other stores costs only transient failures, as in
  Spacelift's disaster recovery guide
  ([24 section 11](../reference/24-self-hosted-reference.md#11-disaster-recovery)).
- **`fs` driver.** It is for development, tests and single-node installs.
  Several replicas need a shared POSIX volume.
- **The floor moves.** An installation on a managed service upgrades its
  PostgreSQL major before it installs the release that raises the floor. A
  self-managed PostgreSQL below the floor cannot run infraweave.
- **Read-back on `gcs` and `azureblob`.** Committing a pre-signed upload
  reads the object once more, which costs a second transfer of each upload on
  those two drivers.
- **Importing from Spacelift.** Data imported from Spacelift keeps its ULIDs.
  Slugs keep their uniqueness rule.

## Verification

- **Store contract suites.**
  - `internal/store/storetest` holds one suite per store family.
  - They run against PostgreSQL in testcontainers-go
    (`github.com/testcontainers/testcontainers-go/modules/postgres`), on the
    floor as a blocking job and on the next major as a non-blocking one
    ([0016](0016-testing-and-acceptance.md#ci)).
  - They cover transactions, retries on injected `40001`, lock ordering under
    concurrent writers, and keyset pagination.
- **Migration runner.** `internal/store/migrations` tests:
  - an empty database migrates every stream to its newest version, in
    registration order;
  - a gap in a stream's numbering fails, and two streams may reuse a number;
  - a second run is a no-op;
  - two concurrent runners apply each file once;
  - a changed checksum fails;
  - `no-transaction` files run outside a transaction;
  - `compatible-from` makes an older binary refuse to start;
  - `check` mode refuses to start while migrations are pending.
- **Schema conventions.** `TestAccSchemaConventions` migrates a fresh database
  and queries `pg_catalog`. It asserts:
  - every foreign key is indexed;
  - no column is `timestamp without time zone`;
  - no enum types exist;
  - every `_sealed` column is `bytea`;
  - every account-scoped table has `account_id`.
- **Object storage.**
  - `internal/objectstore/objectstoretest` runs the contract above against
    every driver: `s3` on RustFS, `gcs` on fake-gcs-server and `azureblob` on
    Azurite, each in testcontainers (`TestAcc`), and `fs` (`TestUnit`). The
    emulators' gaps, and the nightly run against real AWS, Google Cloud and
    Azure buckets that covers them, are [0016](0016-testing-and-acceptance.md#contract-suites)'s.
  - A floor test starts infraweave against PostgreSQL one major below the
    floor and asserts that it refuses to start.
  - Tests of `blob_objects` cover orphan cleanup of `pending` rows and
    deletion after commit; a `gcs` or `azureblob` checksum mismatch leaves
    the object under a `pending` row and deletes nothing.
  - A unit test asserts that `serve`'s composition receives an
    `objectstore.Writer` and that no `serve` dependency holds a `Store`.
- **IAM database authentication.** Unit tests with a fake token source
  assert that each `database.iam_auth` value sets a new token as the
  password of every new connection and that a weak `sslmode` fails the
  start; the reference deployments' smoke tests connect with each
  ([0016](0016-testing-and-acceptance.md)).
- **0003 across replicas.** Gherkin acceptance features run two server
  instances on one database:
  - `api_idempotency_replicas.feature`: a replay through the other instance, and
    in-flight takeover after a lapsed lock;
  - `api_rate_limit_replicas.feature`: one budget shared by both instances;
  - `api_disabled_callers.feature`: the switch takes effect on both.

## References

- [24 section 3](../reference/24-self-hosted-reference.md#3-external-dependencies):
  self-hosted Spacelift's PostgreSQL, buckets, queues and encryption.
- [28 section 12](../reference/28-data-model.md#12-storage-tables-i): the tables
  this schema implements.
- [27 section 10.2](../reference/27-reference-materials.md#102-river): River's
  Postgres patterns.
- [0003](0003-api-conventions.md): the idempotency and rate-limit contracts these
  tables serve.
- <https://www.postgresql.org/support/versioning/>: PostgreSQL support windows.
- Managed PostgreSQL 18 availability: [Amazon RDS](https://aws.amazon.com/about-aws/whats-new/2025/11/amazon-rds-postgresql-major-version-18),
  [Azure flexible server](https://learn.microsoft.com/en-us/azure/postgresql/configure-maintain/concepts-supported-versions),
  [Cloud SQL](https://docs.cloud.google.com/sql/docs/postgres/release-notes).
- <https://github.com/jackc/pgx>: the driver.
