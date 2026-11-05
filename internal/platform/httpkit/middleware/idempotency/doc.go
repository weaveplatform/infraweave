// Package idempotency makes a retried POST or PATCH safe (decision 0003). A
// request that sends an Idempotency-Key runs once; a repeat with the same
// key and request gets the first response again, marked
// Idempotency-Replayed: true. A repeat with another request answers 422
// idempotency-key-reuse, and one that arrives while the first is still in
// flight answers 409 idempotency-key-in-flight.
//
// Middleware applies the rule to the requests its applies function selects;
// Store holds each key's claim and stored response per caller. Postgres is
// the store serve uses, shared by every replica, and Memory an in-process
// one for tests. Config is server.idempotency: how long a stored response
// is kept, and how long a request holds its key before another may take it
// over.
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
// platform/sql to mark an unreachable database, and pgx for its errors. internal/app composes it and app/config reads its Config.
package idempotency
