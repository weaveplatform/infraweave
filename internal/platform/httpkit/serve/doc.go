// Package serve runs a process's two HTTP listeners: the API listener and the
// admin listener, which carries the probes (decision 0017).
//
// Config is server.listen, server.admin_listen and server.shutdown.timeout,
// with their Defaults and Validate; ErrConfig marks an invalid setting. Start
// binds both listeners and serves the two handlers it is given, Running
// reports their addresses and shuts them down, and Run serves until its
// context ends and then drains in-flight requests for at most the shutdown
// timeout. What each listener routes is the caller's: internal/app passes the
// API and the probes, and the agent process modes will pass their own.
//
// # Input and output
//
// It opens two TCP listeners and serves HTTP on them, bounding the time a
// client may take to send its headers. It logs "listening" with both
// addresses and "shutting down" with the timeout, and routes net/http's own
// errors to the given slog.Logger at error level.
//
// # Dependencies
//
// It imports only the standard library, so the agent process modes may use
// it (repo layout, agent-isolation). internal/app and app/config import it.
package serve
