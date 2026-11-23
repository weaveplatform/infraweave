// Package cli is the agent's cobra commands: infraweave launcher,
// infraweave worker-controller, and infraweave runner, which is hidden.
//
// Launcher loads the launcher's configuration, strictly, from its flags,
// the INFRAWEAVE_LAUNCHER_* environment and the file --config names; a key
// the configuration does not have fails the start. It builds the Docker
// executor with launcher.docker's network, runtime and runner binary,
// gives the launcher its own environment's whitelisted variables and
// launcher.docker's extra mounts as each run's phase-scoped inputs,
// binds the admin listener, and then runs the launcher until its context
// ends or a cycle ends it. The admin listener answers /livez, /startupz once the worker
// registered and Docker answered, and /readyz while the worker has a
// session the server answered within three poll timeouts and Docker
// answers. SIGHUP re-reads launcher.log.level. Runner reads the
// launcher's handoff from standard input and runs the runner. Its
// bootstrap subcommand, a run Pod's init container, receives the Pod's
// preparation stream on standard input into the Pod's volumes, for the
// run, state version and worker its flags name, and logs only the
// stream's shape; its signal interrupt subcommand sends the run
// container's PID 1, the runner, SIGINT; and its command subcommand, the
// fixed entrypoint of an initialization command's helper container,
// serves the private request on standard input through
// internal/agent/initcommand.
//
// WorkerController loads the worker_controller configuration the same
// strict way, from its flags, the INFRAWEAVE_WORKER_CONTROLLER_*
// environment and --config. It reaches Kubernetes through the file
// KUBECONFIG names or else the Pod's service account, logs its commit and
// FIPS 140-3 mode as it starts, serves /livez and /readyz, ready while
// the API server answers its Lease and, on the leader, once a pass read
// its pools and reached their installations, and runs the controller
// until its context ends. Its uninstall subcommand, the chart's
// pre-delete hook, deletes the WorkerPools its --selector matches and
// waits, within --timeout, while the running controller finalizes them.
//
// ref: decision 0008 § "The launcher and the runner"
//
// # Input and output
//
// It reads the configuration file and the environment, logs JSON or text
// to the command's standard output, serves the admin listener, and calls
// the worker API, the account API, the Docker daemon and the Kubernetes
// API through the packages it runs.
//
// # Dependencies
//
// It imports cobra, client-go's rest and clientcmd, internal/agent/bootstrap,
// config, controller, executor, executor/docker, initcommand, launcher, runner and wire, and
// platform/buildinfo, httpkit/serve and observe; no server package.
// cmd/infraweave adds its commands.
package cli
