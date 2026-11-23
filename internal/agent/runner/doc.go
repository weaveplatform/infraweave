// Package runner is infraweave runner, the run container's root process,
// which the launcher starts from its own binary. Run reads the launcher's
// handoff from standard input and executes the run it hands over with
// internal/agent/exec, in the working directory, which is the
// container's workspace, taking each SIGINT on the channel it is given as
// a stop of the run. HandBack, infraweave runner hand-back, opens the
// workspace of a run container that ended before its runner did to the
// launcher's user. Interrupt, infraweave runner signal interrupt, sends
// a process SIGINT, as a Kubernetes run Pod's stop does to its runner.
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
// internal/agent/exec. internal/agent/cli runs it as infraweave runner,
// infraweave runner hand-back and infraweave runner signal interrupt.
package runner
