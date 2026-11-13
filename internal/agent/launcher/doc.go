// Package launcher is infraweave launcher's loop: it registers a worker
// of a private pool, polls the worker API for runs, and executes each in
// a container through an executor.Executor, one at a time.
//
// New reads the pool token and the pool's private key and checks that the
// key is the P-256 key the token's certificate certifies. Run registers
// with an ES256 JWS carrying the certificate in x5c and a fresh jti,
// resuming the worker the cache directory names, then polls, holding each
// poll for launcher.poll_timeout. A poll reports whether a run is
// executing, the drain flag it last learned, and the exit of a container
// that ended since the last poll; a container that ends cuts a held poll
// short. An assignment is prepared: the launcher fetches the run's
// specification with the run token, opens its sealed part with the pool's
// private key, fetches the release of the tool the specification names
// through internal/agent/tools, unless its download is false, and starts
// the container with the tool's binary mounted read-only at /bin/<tool>
// and the handoff, so the opened secrets reach the runner without
// touching the host's disk; a preparation that fails reports the run
// failed in its preparing state, with the error as its note.
// stop and kill reach the run's container, cycle exits once the launcher
// is idle, and a renewed session token replaces the old one.
//
// The cache directory's worker.json keeps the pool's worker and the run it
// executes, so a restarted launcher resumes the worker, finds the run's
// container by its labels, and reports its exit, or reports it missing
// when it is gone. A refused session registers again: the same worker
// keeps its run, and a new one means the server lost the old worker and
// failed its run, whose container is removed. A refused registration,
// such as after a pool reset, ends Run with an error; other failures are
// retried with backoff up to 30 seconds. When Run's context ends, an idle
// launcher deregisters; a busy one keeps its worker for a restart.
// Registered reports a session, for /startupz, and Ready a session the
// server answered within three poll timeouts, for /readyz.
//
// ref: decision 0008 § "The launcher and the runner"
// ref: decision 0008 § "Runner images"
//
// # Input and output
//
// It calls the worker API and downloads tool releases through the
// caller's HTTP client, starts and waits on containers through the
// executor, and reads and writes worker.json and the tool binaries in
// launcher.cache_dir. It logs registration, each run's
// start, signals and end, each line of the runner's output as "runner
// output", and each failed call with its retry.
//
// # Dependencies
//
// It imports internal/agent/client, config, executor, handoff, tools and
// wire,
// platform/seal to open specifications, and go-jose to sign
// registrations; no server package. internal/agent/cli runs it as
// infraweave launcher.
package launcher
