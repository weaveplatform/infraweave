// Package runner is infraweave runner, the run container's root process,
// which the launcher starts from its own binary. This build's runner is a
// stub: Run reads the launcher's handoff from standard input, reports that
// the run's first phase started, which ends the handover, and then that
// it failed with Detail as its note, since it executes no phase.
//
// ref: decision 0008 § "The launcher and the runner"
//
// # Input and output
//
// It reads the handoff from an io.Reader, the container's standard input,
// and sends two reports to the worker API with the run token. It logs
// "phase started" and "run ended" with the run and the state.
//
// # Dependencies
//
// It imports internal/agent/handoff and internal/agent/client.
// internal/agent/cli runs it as infraweave runner.
package runner
