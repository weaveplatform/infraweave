// Package idempotency makes a retried POST or PATCH safe (decision 0003). A
// request that sends an Idempotency-Key runs once; a repeat with the same
// key and request gets the first response again, marked
// Idempotency-Replayed: true. A repeat with another request answers 422
// idempotency-key-reuse, and one that arrives while the first is still in
// flight answers 409 idempotency-key-in-flight.
//
// Middleware applies the rule to the requests its applies function selects;
// Store holds each key's reservation and stored response per caller, and
// Memory is the in-process store used until P2's PostgreSQL store. Config is
// server.idempotency: how long a stored response is kept.
//
// # Input and output
//
// For a request that carries a key it reads the body, up to MaxBody (a larger
// one answers 413 payload-too-large), to fingerprint it with SHA-256, then
// restores it for the handler; it records
// the response and replays it from the store. Memory keeps everything in
// process memory, and records expire after the configured TTL by the clock it
// is given. A failing store is logged through the given slog.Logger and
// answered with a 500 problem.
//
// # Dependencies
//
// It imports platform/httpkit/problem for its 400, 409, 413, 422 and 500
// bodies, and the
// standard library otherwise. internal/app composes it and app/config reads
// its Config.
package idempotency
