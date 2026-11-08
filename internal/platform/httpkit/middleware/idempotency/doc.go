// Package idempotency makes a retried POST or PATCH safe. A request that
// sends an Idempotency-Key runs once; a repeat with the same key and request
// gets the first response again, marked Idempotency-Replayed: true. A repeat
// with another request answers 422 idempotency-key-reuse, and one that
// arrives while the first is still in flight answers 409
// idempotency-key-in-flight.
//
// Middleware applies the rule to the requests its applies function selects;
// Store holds each key's claim and stored response per caller. Postgres is
// the store serve uses, shared by every replica, which seals each stored
// body in idempotency_keys.body_sealed (Body) under the keyring, with the
// associated data idempotency_keys.body_sealed:<caller>/<key>; Memory is an
// in-process one for tests. Config is server.idempotency: how long a stored response
// is kept, and how long a request holds its key before another may take it
// over.
//
// ref: decision 0003
//
// # Input and output
//
// For a request that carries a key it reads the body, up to MaxBody (a larger
// one answers 413 payload-too-large), to fingerprint it with SHA-256, then
// restores it for the handler; it records the response and replays it from
// the store. Postgres reads and writes the platform stream's
// idempotency_keys table, its locks and expiry on the database clock; Memory
// keeps everything in process memory by the clock it is given. A failing
// store is logged through the given slog.Logger and answered by
// problem.Failure: 503 with Retry-After when PostgreSQL cannot be reached,
// otherwise 500. Postgres marks the first with sql.Unavailable.
//
// # Dependencies
//
// It imports platform/httpkit/problem for its 400, 409, 413, 422, 500 and
// 503 bodies, platform/tx for the transaction a store call joins,
// platform/sql to mark an unreachable database, platform/crypt for the
// sealed column, and pgx for its errors. internal/app composes it with the
// keyring and app/config reads its Config.
package idempotency
