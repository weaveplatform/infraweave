// Package ratelimit enforces decision 0003's rate limits: one token bucket
// per caller and cost class, the bucket's state in X-RateLimit-* headers on
// every response, 429 when it is empty, and the operator's switch that turns
// the API off for one caller.
//
// Middleware takes a token for each request from the bucket of its caller
// (CallerFunc; ClientAddr until authentication arrives) and class (ClassFunc:
// Standard or Expensive). Disabled answers 503 api-disabled for a caller the
// Switch names, before any bucket is touched. Store holds the buckets:
// Postgres is the store serve uses, shared by every replica, and Memory an
// in-process one for tests. The switches are Callers, the configured list,
// and Table, api_disabled_callers, which Switches consults together. Config
// is server.rate_limit.
//
// # Input and output
//
// It reads the request's remote address and writes the X-RateLimit-Limit,
// X-RateLimit-Remaining and X-RateLimit-Reset headers, and Retry-After on a
// 429 or 503 problem. Postgres refills the platform stream's
// rate_limit_buckets in one statement per request on the database clock.
// Table keeps a copy of api_disabled_callers per replica, read on first use
// and again after an infraweave_invalidate notification carrying
// DisabledCallersKey, a listener reconnect, or the earliest until. Nothing
// is logged.
//
// # Dependencies
//
// It imports platform/httpkit/problem for its problem bodies, platform/tx
// for the transaction a store call joins, and platform/sql/notify for the
// invalidations Table watches. internal/app composes it and app/config
// reads its Config.
package ratelimit
