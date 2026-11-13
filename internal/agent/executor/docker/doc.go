// Package docker is the Docker executor.Executor, over
// github.com/moby/moby/client.
//
// Start pulls a job's image when the host lacks it and creates its
// container with the launcher's own binary bind-mounted read-only at
// BinaryPath and run as the root process with the argument runner,
// replacing the image's ENTRYPOINT and CMD, as UID and GID 1983, in
// Workspace, with the job's mounts bound, labelled with the worker and
// the run, on the configured network and runtime. It attaches before it starts the container, writes
// the job's handoff to the runner's standard input and closes it, so the
// opened specification never reaches the container's configuration on
// the host's disk, and copies the runner's output to the job's writer.
// Find lists a worker's containers by label after a restart and follows
// the newest one's output. A container's Wait answers its exit code with
// killed after Kill, out-of-memory when the kernel ended it, and missing
// when it is gone; Stop sends SIGINT and Kill SIGKILL; Remove removes it.
//
// ref: decision 0008 § "The launcher and the runner"
//
// # Input and output
//
// It calls the Docker daemon the DOCKER_* environment names, or the local
// socket, and copies containers' output to the writers it is given.
// Nothing is logged.
//
// # Dependencies
//
// It imports the moby client and API types, containerd's errdefs, and
// internal/agent/executor. internal/agent/cli builds it for the launcher.
package docker
