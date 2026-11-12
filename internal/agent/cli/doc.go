// Package cli is the agent's cobra commands: infraweave launcher, and
// infraweave runner, which is hidden.
//
// Launcher loads the launcher's configuration, strictly, from its flags,
// the INFRAWEAVE_LAUNCHER_* environment and the file --config names; a key
// the configuration does not have fails the start. It builds the Docker
// executor with launcher.docker's network and runtime, binds the admin
// listener, and then runs the launcher until its context ends or a cycle
// ends it. The admin listener answers /livez, /startupz once the worker
// registered and Docker answered, and /readyz while the worker has a
// session the server answered within three poll timeouts and Docker
// answers. SIGHUP re-reads launcher.log.level. Runner reads the
// launcher's handoff from standard input and runs the runner.
//
// ref: decision 0008 § "The launcher and the runner"
//
// # Input and output
//
// It reads the configuration file and the environment, logs JSON or text
// to the command's standard output, serves the admin listener, and calls
// the worker API and the Docker daemon through the packages it runs.
//
// # Dependencies
//
// It imports cobra, internal/agent/config, executor, executor/docker,
// launcher, runner and wire, and platform/buildinfo, httpkit/serve and
// observe; no server package. cmd/infraweave adds its commands.
package cli
