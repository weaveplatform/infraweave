// Package ratelimit enforces decision 0003's rate limits: one token bucket
// per caller and cost class, the bucket's state in X-RateLimit-* headers on
// every response, 429 when it is empty, and the operator's switch that turns
// the API off for one caller.
//
// Middleware takes a token for each request from the bucket of its caller
// (CallerFunc; ClientAddr until authentication arrives) and class (ClassFunc:
// Standard or Expensive). Disabled answers 503 api-disabled for a caller the
// Switch names, before any bucket is touched. Store holds the buckets, and
// Memory is the in-process store used until P2. Config is
// server.rate_limit.
//
// # Input and output
//
// It reads the request's remote address and writes the X-RateLimit-Limit,
// X-RateLimit-Remaining and X-RateLimit-Reset headers, and Retry-After on a
// 429 or 503 problem. Buckets live in process memory and refill by the clock
// it is given; nothing is logged.
//
// # Dependencies
//
// It imports platform/httpkit/problem for its problem bodies, and the
// standard library otherwise. internal/app composes it and app/config reads
// its Config.
package ratelimit
