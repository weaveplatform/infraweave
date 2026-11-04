// Package app is infraweave's composition root: it builds each process mode
// from the configuration and the platform packages, and is the one place
// that will see every domain. cmd/infraweave holds one thin cobra file per
// process mode over it.
//
// Today it composes the serve process mode. API mounts the generated strict
// server (apiServer, which embeds api.UnimplementedStrict, so every operation
// answers 501) under BaseURL behind decision 0003's controls; Guarded and
// Operations put the operator switch, the rate limits and idempotency keys in
// front of each route, with each operation's cost class read from the
// embedded spec's x-infraweave-cost. Start and Serve run the API and the
// probes on their two listeners. Version is the line infraweave version
// prints.
//
// # Input and output
//
// Start and Serve bind the API and admin listeners through httpkit/serve and
// answer HTTP on them until the context ends; Serve then drains in-flight
// requests. The operation table is decoded once from the spec embedded in
// gen/api. Panics and listener events are logged through the given
// slog.Logger. It reads no file and no environment variable itself: config
// does that before it is called.
//
// # Dependencies
//
// It imports app/config for the settings; platform/gen/api for the strict
// server; platform/httpkit/{serve,middleware,middleware/ratelimit,
// middleware/idempotency,problem} for the listeners and controls;
// platform/observe for the probes; platform/buildinfo for the version;
// chi for routing and kin-openapi for the spec. Only cmd/ imports it; no
// package under platform, kernel or agent may (repo layout, depguard rules).
package app
