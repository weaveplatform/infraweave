// Package app is infraweave's composition root: it builds each process mode
// from the configuration and the platform packages, and is the one place
// that sees every domain. cmd/infraweave holds one thin cobra file per
// process mode over it. Domains holds the business domains, which
// newDomains builds in the declared graph's order on the primary pool and
// the keyring, with their settings, beside the access domain's
// Authenticator and OperatorAuthenticator, and the expression registry it
// builds from the domains' surfaces under the expressions settings and
// binds back into the configuration domain, and the vcs domain's
// resolution of a stack's refs and check of the VCS integration a stack
// names, which it binds back into the stacks domain;
// apiServer embeds each domain's partial strict server ahead of
// api.UnimplementedStrict.
//
// It composes the serve, scheduler and all process modes (Mode). API mounts
// the generated strict server (apiServer, which embeds
// api.UnimplementedStrict, so every operation no domain serves answers 501) under BaseURL, and the
// operator API's (operatorServer, which embeds the access and features
// domains' operator partials) under OperatorBaseURL on the same
// router, behind the rate limits and idempotency keys, each prefix
// authenticated by its own authenticator (byPrefix); Guarded and
// Operations put authentication, then the authorization route table, which
// answers 403 forbidden to a route the router serves and no operation
// names and runs the access domain's Requirer on each API operation's
// x-infraweave-action and x-infraweave-subject, then the operator switch, the rate limits
// and idempotency keys in front of each route, and last strictBodies, which
// answers 400 invalid-request to a body its operation's schema refuses,
// naming a member no strict schema lists by its pointer with reason
// unknown-field, and checks a PATCH's merge patch against its PUT's input
// with every member optional and nullable, with each operation's cost
// class read from the embedded spec's x-infraweave-cost, whether it is
// public from its empty security, and whether it admits a credential of
// no account from its x-infraweave-access: organization, with the
// access domain's break-glass login (accessapi.LoginPattern) public and
// expensive beside them, with the controls' state in Guards and
// the principal's <kind>:<actor ID> as their caller; the access log runs
// before them. Run runs a mode: in serve and all it requires
// server.public_url; it opens PostgreSQL from
// database.url, which it requires, applies or checks the migration streams
// Streams registers, in the declared graph's order, with each domain's
// stream after the platform's, loads the keyring
// through the KEK provider secrets.kek.provider names, which it requires
// (openKEK), and reloads it on each change to encryption_keys, opens each
// KMS signing key of auth.signing.kms_keys under the kms backend
// (openSigners), so a key that cannot sign ES256 stops the start, opens the
// object store
// objectstore.driver names, which it requires (openObjects), registers the
// probe checks every
// mode runs (databaseChecks and keyringCheck), and serves the probes on the
// admin listener and, in serve and all, the API on the API listener, with
// the controls' state in PostgreSQL (postgresGuards). serve holds object
// storage as Blobs (serving): blob records over objectstore.WriterOnly of
// the store, so nothing it holds can delete an object, and the fs driver's
// /blobs/{token} routes on the API listener, public in the operations
// table because the token is their credential. In scheduler and all
// it starts the scheduler role (startScheduler): the queue's workers with
// the platform's handlers, the outbox's events.fanout and events.consume
// and blob.delete over the full Store, and the cron ticker over
// periodic_tasks, after registering each domain's tasks (Domains.cron),
// integrity.orphans, which counts every domain's held IDs whose row is
// gone against access' keys and logs each column with any at warn
// (orphansTask), queue.prune, idempotency.purge,
// events.prune, blob.expire and secrets.rotate, with secrets.reseal, which
// each rotation enqueues (secretsTasks) and which walks SealedColumns, the
// platform's sealed columns and each domain's; every
// domain appends to and subscribes on Run's one outbox, where
// subscribePurges subscribes the features, stacks, configuration and vcs purges to
// accessapi.Deleted, since features cannot read access' event itself, and
// the configuration domain subscribes its automatic attachments, and the
// vcs domain its tracking of a stack's commit, to stacksapi.Changed. It stops the ticker, then
// the workers, after the HTTP drain. MessageQueueStats is the
// installation's own operation, which no domain owns, as are the operator
// API's dead-job operations and its data key rotation, the same rotation
// as infraweave secrets rotate; Platform holds what they reach. MigrateUp
// and MigrateStatus are
// infraweave migrate up and status; GenerateKEK, SecretsRotate and
// SecretsRewrap are infraweave secrets generate-kek, rotate and rewrap.
// Version and FIPS are the lines infraweave version prints, the second the
// FIPS 140-3 mode and the Go Cryptographic Module's version.
// HashPassword is infraweave auth hash-password: it reads a password's
// first line and prints its break-glass hash.
//
// ref: decisions 0003, 0007, 0017 and 0022
//
// # Input and output
//
// Run binds its mode's listeners through httpkit/serve and answers HTTP on
// them until the context ends, then shuts down: /readyz fails first, and
// the listeners drain after the ready delay. The
// operation table is decoded once from the specs embedded in gen/api and
// gen/operator. HashPassword reads its io.Reader and writes the
// io.Writer. Run
// connects to PostgreSQL through platform/sql and migrates it through
// platform/sql/migrate before any listener binds, logging each file it
// applies, then loads the keyring from encryption_keys through the KEK
// provider; the probes query it; every mode holds one listening connection, shared
// by the API's switch (infraweave_invalidate) and the workers
// (infraweave_jobs), whose liveness the scheduler's readiness checks. It closes the connections and the
// pools after the drain. MigrateUp, MigrateStatus, SecretsRotate and
// SecretsRewrap write their report to the given io.Writer; GenerateKEK
// creates the file it is given. Requests, panics and listener events are logged
// through the given slog.Logger. It reads no file and no environment
// variable itself: config does that before it is called.
//
// # Dependencies
//
// It imports app/config for the settings; each domain's assembly (access)
// to build it, mount its partial and list its stream, sealed columns, keys
// and held columns; accessapi for the event the purges consume;
// platform/gen/api and platform/gen/operator for the strict
// servers; platform/httpkit/{serve,middleware,middleware/ratelimit,
// middleware/idempotency,problem} for the listeners and controls;
// platform/observe for the probes and their checks; platform/sql, sql/migrate, sql/schema
// and sql/notify for the database, its streams and its notifications;
// sql/integrity for the orphan count;
// platform/queue, platform/cron and platform/outbox for the scheduler role;
// platform/expr for the expression registry;
// platform/crypt, crypt/kek and its providers for the keyring;
// crypt/signer and its KMS backends for the kms signing keys; platform/objectstore, its
// drivers and objectstore/blobs for object storage;
// platform/buildinfo for the version;
// chi for routing and kin-openapi for the spec. Only cmd/ imports it; no
// package under platform, kernel or agent may (repo layout, depguard rules).
package app
