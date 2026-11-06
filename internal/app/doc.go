// Package app is infraweave's composition root: it builds each process mode
// from the configuration and the platform packages, and is the one place
// that will see every domain. cmd/infraweave holds one thin cobra file per
// process mode over it.
//
// It composes the serve, scheduler and all process modes (Mode). API mounts
// the generated strict server (apiServer, which embeds
// api.UnimplementedStrict, so every operation answers 501) under BaseURL behind decision 0003's controls; Guarded and
// Operations put the operator switch, the rate limits and idempotency keys in
// front of each route, with each operation's cost class read from the
// embedded spec's x-infraweave-cost, with their state in Guards; the access
// log runs before them. Run runs a mode: it opens PostgreSQL from
// database.url, which it requires, applies or checks the migration streams
// Streams registers, in the declared graph's order, loads the keyring
// under secrets.kek, which it requires, opens the object store
// objectstore.driver names, which it requires (openObjects), registers the
// probe checks every
// mode runs (databaseChecks and keyringCheck), and serves the probes on the
// admin listener and, in serve and all, the API on the API listener, with
// the controls' state in PostgreSQL (postgresGuards). serve holds object
// storage as Blobs (serving): blob records over objectstore.WriterOnly of
// the store, so nothing it holds can delete an object, and the fs driver's
// /blobs/{token} routes on the API listener. In scheduler and all
// it starts the scheduler role (startScheduler): the queue's workers with
// the platform's handlers, the outbox's events.fanout and events.consume
// and blob.delete over the full Store, and the cron ticker over
// periodic_tasks, after registering queue.prune, idempotency.purge,
// events.prune and blob.expire; every
// domain appends to and subscribes on Run's one outbox. It stops the ticker, then
// the workers, after the HTTP drain. MessageQueueStats is the
// installation's own operation, which no domain owns. MigrateUp and MigrateStatus are
// infraweave migrate up and status. Version is the line infraweave version
// prints.
//
// # Input and output
//
// Run binds its mode's listeners through httpkit/serve and answers HTTP on
// them until the context ends, then shuts down in decision 0017's order. The
// operation table is decoded once from the spec embedded in gen/api. Run
// connects to PostgreSQL through platform/sql and migrates it through
// platform/sql/migrate before any listener binds, logging each file it
// applies, then loads the keyring from encryption_keys through the KEK
// provider; the probes query it; every mode holds one listening connection, shared
// by the API's switch (infraweave_invalidate) and the workers
// (infraweave_jobs), whose liveness the scheduler's readiness checks. It closes the connections and the
// pools after the drain. MigrateUp and MigrateStatus write their report to
// the given io.Writer. Requests, panics and listener events are logged
// through the given slog.Logger. It reads no file and no environment
// variable itself: config does that before it is called.
//
// # Dependencies
//
// It imports app/config for the settings; platform/gen/api for the strict
// server; platform/httpkit/{serve,middleware,middleware/ratelimit,
// middleware/idempotency,problem} for the listeners and controls;
// platform/observe for the probes and their checks; platform/sql, sql/migrate, sql/schema
// and sql/notify for the database, its streams and its notifications;
// platform/queue, platform/cron and platform/outbox for the scheduler role;
// platform/crypt and crypt/kek for the keyring; platform/objectstore, its
// drivers and objectstore/blobs for object storage;
// platform/buildinfo for the version;
// chi for routing and kin-openapi for the spec. Only cmd/ imports it; no
// package under platform, kernel or agent may (repo layout, depguard rules).
package app
