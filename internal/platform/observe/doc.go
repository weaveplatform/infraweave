// Package observe is what a process tells the outside about itself: its logs
// and its health.
//
// NewLogger is the process's slog.Logger: JSON, one object per line, or text
// for local work (LogConfig, log.format and log.level), with the standard
// fields service, mode and version on every line, time in UTC, and the
// request ID of the record's context. Every attribute passes through Redact
// first, which replaces the value of a secret key, keeps only the
// allowlisted headers of an http.Header, and leaves a Secret as Redacted
// under any key. FollowLevel re-reads log.level each time a signal arrives,
// and Warnings logs a repeated warning once a minute per source.
//
// Health is the check registry each process mode adds to: startup checks,
// which latch once they have all passed, and readiness checks, which fail
// for good once Stop marks the shutdown. Each check runs with a 2 s timeout
// and its result is cached for 1 s. Probes is the admin listener's handler:
// GET /livez answers 200 while the process serves HTTP, /startupz and
// /readyz answer 200 or 503 from the checks, as ok or fail or, with
// ?verbose, as JSON; every other path or method is a problem detail.
// OpenTelemetry and /metrics join it with P18b.
//
// ref: decision 0017
//
// # Input and output
//
// The logger writes to the io.Writer it is given, stdout in every process
// mode. FollowLevel reads a signal channel and calls the given read
// function. Probes answers HTTP on whichever listener it is mounted on and
// runs the registered checks, which reach whatever the mode's checks reach.
// A panic in a probe is logged through the given slog.Logger.
//
// # Dependencies
//
// It imports platform/httpkit/middleware for the router and the request ID,
// and platform/buildinfo for the version. internal/app and app/config import
// it, and the agent process modes may (repo layout, agent-isolation).
package observe
