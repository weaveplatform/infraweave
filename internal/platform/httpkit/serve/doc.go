// Package serve runs a process's HTTP listeners: the admin listener, which
// carries the probes, and the API listener of a mode that serves the API.
//
// Config is server.listen, server.admin_listen and server.shutdown, with
// their Defaults and Validate; ErrConfig marks an invalid setting. Start
// binds the admin listener and, when it is given an API handler, the API
// listener; Running reports their addresses and shuts them down. Run serves
// until its context ends, then shuts down: it calls the stopping function
// the caller gives, which makes /readyz fail, keeps accepting for
// server.shutdown.ready_delay, and drains in-flight requests for at most
// server.shutdown.timeout. What each listener routes is the caller's:
// internal/app passes the API and the probes, and the agent process modes
// will pass their own.
//
// ref: decision 0017
//
// # Input and output
//
// It opens TCP listeners and serves HTTP on them, bounding the time a client
// may take to send its headers. It logs "listening" with the bound addresses
// and "shutting down" with the ready delay and the timeout, and routes
// net/http's own errors to the given slog.Logger at error level.
//
// # Dependencies
//
// It imports only the standard library, so the agent process modes may use
// it (repo layout, agent-isolation). internal/app and app/config import it.
package serve
