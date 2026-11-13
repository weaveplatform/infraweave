// Package runner is infraweave runner, the run container's root process,
// which the launcher starts from its own binary. Run reads the launcher's
// handoff from standard input and executes the run it hands over with
// internal/agent/exec, in the working directory, which is the
// container's workspace.
//
// ref: decision 0008 § "The launcher and the runner"
//
// # Input and output
//
// It reads the handoff from an io.Reader, the container's standard input,
// and the process's working directory and environment. The run's phases
// write to standard output, and its reports, log and artifacts reach the
// worker API with the run token.
//
// # Dependencies
//
// It imports internal/agent/handoff, internal/agent/client and
// internal/agent/exec. internal/agent/cli runs it as infraweave runner.
package runner
