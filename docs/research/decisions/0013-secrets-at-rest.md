# 0013: Secrets at rest, delivery to runs and log masking

Status: Proposed

## Context

infraweave stores credentials that belong to other systems: secret environment
variables and mounted files, sensitive stack outputs, VCS tokens and private
keys, webhook secrets, SSO client secrets, AI provider keys, Slack tokens and
Flows secrets. It also stores its own private keys, for signing tokens and for
the worker pool CA. Report 28 marks each of these "encrypted" or "hashed"
([28 section 12.1](../reference/28-data-model.md#121-shapes-that-recur)).

What Spacelift does:

- **Write-only values.** A secret configuration element is never returned by
  the API. Only its SHA-256 checksum is readable, and a secret mounted file
  cannot be downloaded
  ([13 section 4.1](../reference/13-configuration.md#41-plain-and-secret)).
  Plain values are also encrypted at rest.
- **Masking.** Runs mask secret values, `::add-mask` words and, optionally,
  well-known token patterns in their logs, one line at a time
  ([13 section 4.4](../reference/13-configuration.md#44-log-masking)).
- **Encryption of the database.** Self-hosted Spacelift encrypts sensitive
  database fields with either AWS KMS or an RSA key that wraps an AES-256 data
  key. With KMS a separate key signs JWTs; with RSA the same key does. The
  choice between the two is permanent, and losing the key loses every
  credential
  ([24 section 3.5](../reference/24-self-hosted-reference.md#35-encryption-and-kms)).
- **Private worker pools.** Temporary run state is encrypted end to end with
  the pool's key pair, so only the pool's workers can read it
  ([10 section 1.1](../reference/10-workers-and-execution.md#11-public-and-private-pools-compared)).

A database backup, a replica or a support dump must not expose any of this.
Large sensitive objects (state files, sampled policy inputs, plan output) live
in object storage ([0005](0005-persistence-postgresql.md)), where bucket
encryption alone protects nothing from someone holding the bucket's
credentials.

## Decision

### Keys

- **Envelope encryption with a keyring.** `internal/secrets` holds a keyring
  of data keys. Each is a random 256-bit AES key, stored in
  `encryption_keys(id int primary key, wrapped_key bytea, kek_ref text, state text, created_at, retired_at)`
  and wrapped by a key encryption key (KEK) that never enters the database.
- **Key states.**

  | State | Seals new values | Opens existing values |
  |---|---|---|
  | `active` (exactly one, by a partial unique index) | yes | yes |
  | `retiring` | no | yes |
  | `retired` | no | no; the row is deleted once nothing references it |

- **Start-up.** At start, a replica unwraps every `active` and `retiring` key
  and holds them in memory. A key that fails to unwrap stops the start with an
  error naming the key ID, because it means the wrong KEK is configured.
- **The first start.** The first replica to start on an empty keyring creates
  the first data key. A replica that loses that insert race re-reads the
  keyring.

### KEK providers

The provider is set by `secrets.kek.provider`.

| Provider | Settings | Wrap operation | Library |
|---|---|---|---|
| `local` | `secrets.kek.local.key_file`, or the environment variable `INFRAWEAVE_SECRETS_KEK` (base64 of 32 bytes) | AES-256-GCM, with associated data `infraweave/data-key/<id>` | standard library |
| `awskms` | `key_id` (ARN or alias), `region` | KMS `Encrypt` and `Decrypt`, with encryption context `{"infraweave":"data-key","id":"<id>"}` | `github.com/aws/aws-sdk-go-v2/service/kms` |
| `gcpkms` | `key_name` (CryptoKey resource name) | Cloud KMS `Encrypt` and `Decrypt`, with `additional_authenticated_data` | `cloud.google.com/go/kms` |
| `azurekv` | `vault_url`, `key_name`, `key_version` | Key Vault `wrapKey` and `unwrapKey` with `RSA-OAEP-256` | `github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys` |
| `vault` | `address`, `mount`, `key_name`, `auth` (`token`, `kubernetes`, `approle`) | Transit `encrypt` and `decrypt` | `net/http`, calling the Transit API directly |

- **Calls to the provider.** Each replica calls the KEK provider once per data
  key at start, and again only when the keyring changes. Sealing and opening
  are local AES operations, so KMS latency and quota never sit on a request
  path.
- **Generating a local KEK.** `infraweave secrets generate-kek` writes a new
  local KEK to a file with mode `0600`.

### Sealed columns

- **Format.** A sealed value is a `bytea` column named `<name>_sealed`:

  ```text
  0x01 | key id (uint32, big-endian) | nonce (12 random bytes) | AES-256-GCM ciphertext and tag
  ```

- **Associated data.** The associated data is
  `<table>.<column>:<primary key>`, where a composite key joins its parts with
  `/`. A sealed value copied into another row or column fails to open, so
  swapping ciphertexts between rows can't move a secret to a place where it
  would be shown.
- **The API.**
  - `secrets.Seal(ctx, aad, plaintext)` and `secrets.Open(ctx, aad, sealed)`
    seal and open one value.
  - Store code calls them through typed helpers, so a handler never sees a
    sealed value.
  - The result is a `secrets.Value`, whose `String`, `Format`, `MarshalJSON`,
    `MarshalText` and `LogValue` all render `[redacted]`. Code that needs the
    bytes calls `Reveal()`, and review looks at every call.

**Derived keys.** Some features need a symmetric key that is not a sealing
key, such as an HMAC key.
- **The call.** `secrets.DeriveKey(purpose)` returns HKDF-SHA256 of the active
  data key, with info `infraweave/<purpose>`, together with that data key's ID.
- **Verifying.** A value made with a derived key carries the key ID.
  Verification accepts keys derived from `active` and `retiring` data keys, so
  a rotation never breaks a value that is still in use.
- **Purposes.**

  | Purpose | Used for | Owner |
  |---|---|---|
  | `objectstore-url` | Signed URLs of the `fs` object store driver | [0005](0005-persistence-postgresql.md) |
  | `state-upload-url` | Tokens of the state upload URLs | [0010](0010-state-backend.md) |
  | `audit-secret-sha` | The `SecretSHA` that stands in for write-only fields in audit entries and webhook payloads | decision 0015 |

  A rotation changes every derived key. So a `SecretSHA` computed before a
  rotation differs from one computed after it for the same value.

**What is sealed.** Every table here is from report 28's storage sketch, with
the owning record beside it.

| Table | Sealed columns | Readable beside it | Owner |
|---|---|---|---|
| `config_elements` | `value_sealed`, for plain and secret elements alike | `checksum` (SHA-256 hex of the value), `write_only` | [13 section 1](../reference/13-configuration.md#1-the-configuration-element) |
| `stack_outputs` | `value_sealed`, for every output | `sensitive` | [0008](0008-run-execution-and-worker-protocol.md) |
| `vcs_integrations` | `credentials_sealed` (tokens, app private keys, passwords), `webhook_secret_sealed`, `previous_webhook_secret_sealed` | provider settings | [0012](0012-vcs-integration-model.md) |
| `webhooks` | `secret_sealed` | endpoint | decision 0015 |
| `webhook_secret_headers` | `value_sealed` | header name | decision 0015 |
| `slack_integrations` | `tokens_sealed` | team | decision 0015 |
| `sso_settings` | `client_secret_sealed`, `sp_private_key_sealed` | the rest of the settings | [0006](0006-authentication-and-tokens.md) |
| `signing_keys` | `private_key_sealed` | public key, `kid`, state | [0006](0006-authentication-and-tokens.md) |
| `ai_integrations` | `credentials_sealed` | provider, base URL, models | decision 0019 |
| `flows_secrets` | `value_sealed` | name | decision 0018 |
| `runs` | `artifact_key_sealed` | | [0008](0008-run-execution-and-worker-protocol.md) |
| `worker_pool_ca` | `private_key_sealed`, the installation's pool CA | certificate, next serial | [0008](0008-run-execution-and-worker-protocol.md) |
| `idempotency_keys` | `body_sealed` | status, headers | [0005](0005-persistence-postgresql.md) |
| `blob_objects` | `content_key_sealed`, for objects in sealed stores | size, checksum | [0005](0005-persistence-postgresql.md) |

- **Hashed, never sealed.** The following are never stored in a form that
  can be reversed: API key secrets, session identifiers, SCIM client secrets,
  VCS agent pool keys ([0012](0012-vcs-integration-model.md)) and Flows agent
  pool tokens. Their hashing is [0006](0006-authentication-and-tokens.md)'s. A secret that infraweave only
  ever compares is hashed. A secret that infraweave must present to someone
  else is sealed.
- **A registry of sealed columns.** Each sealed column is listed in
  `secrets.Registry`, with its table, column and primary key columns. Resealing
  walks the registry, and a test keeps it in step with the schema.

### Sealed objects

- **Which stores are sealed.** Objects in the `states`, `policy-inputs` and
  `run-artifacts` stores ([0005](0005-persistence-postgresql.md)), and state
  files uploaded through the state backend into `uploads`
  ([0010](0010-state-backend.md)), are sealed whatever the bucket's own
  encryption.
- **The content key.** Each object has its own 256-bit content key. That key
  is sealed into `blob_objects.content_key_sealed`, with associated data
  `blob_objects:<store>/<key>`.
- **Object format.** The object is a stream of segments:
  - a header: the magic bytes `IWSB`, version `0x01`, and a 7-byte random
    nonce prefix;
  - segments of 64 KiB of plaintext, each encrypted with AES-256-GCM;
  - each segment's nonce: the prefix, a 4-byte big-endian segment counter, and
    one byte that is `1` on the last segment and `0` otherwise.

  The counter and the last-segment flag make reordering and truncation fail
  to authenticate. Objects stream in both directions, so a 500 MB state file
  never sits in memory.
- **Rotation touches only the database.** Because the content key sits in
  the database, rotating data keys reseals one column and never rewrites
  objects.
- **Artifacts sealed by the runner.**
  - **The artifact key.** The run spec carries a fresh per-run 256-bit
    artifact key ([0008](0008-run-execution-and-worker-protocol.md)).
  - **Per-object keys.** The runner derives each object's content key as
    HKDF-SHA256 of the artifact key with info `infraweave/artifact/<object key>`.
    It seals the object in the format above, the same code in the same binary,
    and uploads it with a pre-signed `PUT`.
  - **Committing the upload.** The server derives the same content key from
    `runs.artifact_key_sealed` and seals it into `blob_objects`.
  - The bucket therefore never holds a readable plan, and the bytes never pass
    through the API.

### Delivery to runs

- **Opening at spec time.** The server opens sealed values only when it
  builds a run's spec. That covers environment variables, mounted files,
  integration credentials and output references for a dependent stack.
- **Sealing to the pool key.** The spec's secret part is sealed to the worker
  pool's public key as a JWE, using `github.com/go-jose/go-jose/v4`:

  | Pool key | Key management | Content encryption |
  |---|---|---|
  | RSA, 2048 bits or more (Spacelift's documented setup uses 4096) | `RSA-OAEP-256` | `A256GCM` |
  | EC P-256 | `ECDH-ES+A256KW` | `A256GCM` |

  The protected header carries the run ID and the state version it was issued
  for. The runner rejects a spec issued for another run.
- **Who can read a spec.** Only a holder of the pool's private key, which the
  customer keeps ([0008](0008-run-execution-and-worker-protocol.md)), can read
  the spec. A replica, a proxy or a log that sees the spec sees ciphertext.
- **Workspaces.** Workspaces that wait for confirmation or a replan are
  encrypted by the worker to the pool key, so infraweave itself cannot open
  them. This is Spacelift's end-to-end property.
- **What no API call returns.** No API operation returns a write-only value,
  whatever the caller's role. That includes the in-run API token of
  [0006](0006-authentication-and-tokens.md), as in
  [13 section 4.1](../reference/13-configuration.md#41-plain-and-secret).
  Secret mounted files refuse download with `403` and the `forbidden` problem
  of [0022](0022-api-errors-and-update-semantics.md).
- **Environment overrides.** The `environmentVarsOverrides` of a proposed run
  are stored sealed like any run input, but they are not masked. This keeps
  the schema's warning that they are not secrets.

### Masking

Masking happens in the runner, before a log line leaves the container. The
server never has unmasked output to leak.

| Source of masked values | Rule |
|---|---|
| Secret configuration elements | The value of every element with `write_only` set, as delivered in the spec. A mounted file's content is masked line by line |
| `SPACELIFT_MASK_ENVS` on the launcher | The values of the named variables |
| `::add-mask v1 v2 ...` printed by a hook or phase | Each whitespace-separated word, from that line on |
| Well-known patterns, when the stack enables `enableWellKnownSecretMasking` | The pattern list of [13 section 4.4](../reference/13-configuration.md#44-log-masking), as Go regular expressions in `internal/secrets/mask` |

- **The mechanism.**
  - **Replacement.** Each match becomes five asterisks.
  - **Scanning.** Matching uses an Aho-Corasick automaton over the literal
    values, then the regular expressions, one line at a time.
  - **Lines.** A line is cut at 64 KiB before masking.
- **Short values.** A value shorter than 4 bytes is not masked, because masking
  `1` or `on` would destroy the log and protect nothing. Spacelift documents
  no minimum, so this is infraweave's rule.
- **What masking can't catch.** A value that spans lines is not masked as a
  whole. Each of its lines is, when it is 4 bytes or longer. That is no better
  than Spacelift's line-at-a-time rule.
- **Policy print output.** The `print` output of policies is masked by the same
  code on the server ([0009](0009-policy-engine-opa.md)).

### Rotation and migration

| Operation | Command | Effect |
|---|---|---|
| Rotate the KEK, or change provider | `infraweave secrets rewrap --to <provider settings>` | Unwraps each data key with the current KEK, wraps it with the new one and updates its row, one row per transaction. Values and objects are untouched. Replicas pick the change up through `infraweave_invalidate` |
| Rotate the data key | `infraweave secrets rotate`, or the admin operation `POST /admin/secrets/rotate` | Inserts a new `active` key and marks the old one `retiring`. Then it enqueues the `secrets.reseal` job on the `maintenance` queue ([0011](0011-persistent-work-queue.md)) |
| Reseal | `secrets.reseal` job | Walks `secrets.Registry` with keyset pagination, 500 rows per transaction. It reseals each value whose key ID is not the active one, then marks the old key `retired` and deletes it once a final pass finds no reference |
| Rotate a pool key | `workerPoolReset` ([0008](0008-run-execution-and-worker-protocol.md)) | New specs are sealed to the new key. Workspaces sealed to the old key can no longer be opened, so runs awaiting confirmation must be replanned |

- **An annual default.** Data keys are rotated every 365 days by default
  (`secrets.rotation_interval`; 0 disables). The `secrets.rotate` periodic
  task checks this daily.

### What is not a secret

The following are stored in plain text, because the API returns them and
nothing about them is secret:
- an AWS integration's role ARN and external ID;
- an Azure integration's tenant and application IDs;
- worker pool certificates;
- public signing keys.

## Rationale

- **Envelope encryption with a local keyring.** It keeps the KMS out of the
  request path. The KMS is called once per key per process, not once per
  value. It also allows rotation without rewriting every row through the KMS.
  Self-hosted Spacelift's RSA mode follows the same pattern
  ([24 section 3.5](../reference/24-self-hosted-reference.md#35-encryption-and-kms)).
- **Associated data bound to the row.** AES-GCM proves a value was sealed by
  infraweave. The associated data also proves where it was sealed, which
  stops an attacker with database write access from moving ciphertext between
  rows.
- **Plain configuration values are sealed too.** Spacelift encrypts both kinds
  at rest. One code path also means that flipping `write_only` never changes
  how a value is stored.
- **Content keys in the database, not in object headers.** An object header
  would force a rewrite of every state version on each rotation.
- **Departures from Spacelift.**
  - **The encryption choice is reversible.** `rewrap` moves the keyring from a
    local key to a KMS, between KMS providers, or to a new key, in a few
    statements. Spacelift's choice between KMS and RSA is permanent; only the
    KMS keys rotate, yearly, by AWS's own key rotation.
  - **GCP, Azure and Vault are offered beside AWS KMS.** The Kubernetes
    deployments of Spacelift can only use RSA there.
  - **The JWT signing key is separate from the encryption root**, also in
    the local-key mode where Spacelift's RSA key does both. It is a sealed row
    of [0006](0006-authentication-and-tokens.md), so rotating one never
    touches the other.
  - **Values shorter than 4 bytes are not masked.** Spacelift documents no
    minimum length for masked values; masking `1` or `on` would destroy the
    log and protect nothing.
- **Rejected: column encryption in PostgreSQL** (`pgcrypto`). The key would
  travel to the database server in queries and logs, and it needs an
  extension that [0005](0005-persistence-postgresql.md) does not require.
- **Rejected: a JWT library's own encryption for columns.** JWE is used for the
  one case it fits, sealing to a public key that infraweave doesn't hold the
  private half of. For local sealing, a fixed binary format is smaller and
  needs no parser.
- **Rejected: delivering secrets to runs in plain text over TLS.** It would
  leave every replica, ingress log and debugging proxy able to read them, and
  would lose Spacelift's guarantee that only the pool can read run state.
- **Masking in the runner, not the server.** The runner is the only component
  that sees the raw output. Masking later would mean shipping unmasked lines
  across the network and through object storage.

## Constraints

- **Losing the KEK.** Every sealed value and object becomes unreadable. The
  operator guide (decision 0020) requires the KEK, or KMS key deletion
  protection, to be backed up with the database.
- **Key material in memory.** Go cannot guarantee that memory is wiped, so
  data keys and revealed values live in process memory until collected. A
  process memory dump exposes them, and the threat model accepts that.
- **Masking is best effort.** Encoded, split or transformed secrets are not
  recognised. Masking is a safety net, not a control.
- **Pool resets.** A pool reset invalidates every workspace waiting for
  confirmation in that pool.
- **Primitives.** Every primitive comes from Go's standard `crypto` packages:
  AES-GCM, HKDF-SHA256, RSA-OAEP and ECDH P-256. Go's FIPS 140-3 module runs
  this design with `GODEBUG=fips140=on`, but records AES-GCM with
  caller-chosen nonces as not approved, and `fips140=only` refuses it
  (`crypto/cipher.NewGCM`). The column format's random nonce matches
  `cipher.NewGCMWithRandomNonce`, which is approved; the segment counter nonces
  and go-jose's JWE content encryption are not, so a FIPS-only build needs a
  different object format and JWE path.

## Verification

- **Sealing.** `internal/secrets` unit tests cover:
  - round trips of `Seal` and `Open`;
  - failure on a wrong key, a changed associated data value, a truncated value
    or an unknown version byte;
  - streaming objects: round trips at 0 bytes, exactly 64 KiB, 64 KiB plus one,
    and 1 GiB, plus a dropped last segment, swapped segments and a flipped
    flag;
  - `secrets.Value`, which renders `[redacted]` through `fmt`, `slog` and
    `encoding/json`.
- **KEK providers.**
  - The `local` provider is tested in unit tests.
  - `vault` runs against Vault in testcontainers.
  - `awskms` runs against LocalStack's KMS in testcontainers.
  - `gcpkms` and `azurekv` use recorded HTTP fixtures, with a live test gated
    on credentials.
- **The registry.** `TestAccSealedColumnRegistry` migrates a fresh database and
  asserts that the set of `*_sealed` columns in `pg_catalog` equals
  `secrets.Registry`.
- **Rotation.** An acceptance feature, `secrets_key_rotation.feature`:
  1. populates every sealed column;
  2. rotates the data key while two server instances run;
  3. checks every value through the API and every sealed object through its
     reader;
  4. confirms the old key is deleted;
  5. runs `rewrap` from `local` to the Vault provider.
- **Delivery and masking.**
  - `secrets_run_delivery.feature` runs a real OpenTofu task that prints a secret
    variable, an `::add-mask` word and a well-known token. It asserts the log
    holds five asterisks for each, and that the bucket holds no plaintext copy
    of the plan.
  - `internal/secrets/mask` has table tests for overlapping values, the
    4-byte minimum and the 64 KiB line cap.

## References

- [13 section 4](../reference/13-configuration.md#4-secrets-write-only-values-and-masking):
  write-only values and masking.
- [24 section 3.5](../reference/24-self-hosted-reference.md#35-encryption-and-kms):
  Spacelift's KMS and RSA modes.
- [10 section 2.3](../reference/10-workers-and-execution.md#23-credentials): pool
  key pairs.
- [RFC 7516](https://www.rfc-editor.org/rfc/rfc7516) (JWE) and
  [RFC 7518](https://www.rfc-editor.org/rfc/rfc7518) (JWA): the run spec envelope.
- [RFC 5869](https://www.rfc-editor.org/rfc/rfc5869): HKDF.
