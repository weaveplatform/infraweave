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
// Streams registers, in the declared graph's order, registers the probe
// checks every mode runs (databaseChecks), and serves the probes on the
// admin listener and, in serve and all, the API on the API listener, with
// the controls' state in PostgreSQL (postgresGuards). The scheduler's loops
// join its mode with their phases. MigrateUp and MigrateStatus are
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
// applies; the probes query it; in serve and all it holds one listening
// connection for infraweave_invalidate. It closes the connections and the
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
// platform/buildinfo for the version;
// chi for routing and kin-openapi for the spec. Only cmd/ imports it; no
// package under platform, kernel or agent may (repo layout, depguard rules).
package app
