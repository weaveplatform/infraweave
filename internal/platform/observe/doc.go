// Package observe is what a process tells the outside about its own health.
//
// Probes is the admin listener's handler: GET /livez answers 200 "ok" while
// the process serves HTTP, and every other path or method is a problem
// detail. Decision 0017's readiness and startup probes join it when there is
// something to be ready for, and slog, OpenTelemetry and /metrics with P2 and
// P18b.
//
// # Input and output
//
// It answers HTTP on whichever listener it is mounted on; it reads no
// dependency's state yet. A panic in a probe is logged through the given
// slog.Logger.
//
// # Dependencies
//
// It imports platform/httpkit/middleware for the router. internal/app mounts
// it, and the agent process modes may (repo layout, agent-isolation).
package observe
