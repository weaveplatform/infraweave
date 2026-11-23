// Package initcommand runs the exec calls of a worker initialization
// policy in isolated helpers, never on the launcher's host.
//
// Each call is a fresh container or Pod of the run's resolved image that
// an executor.Commander starts with the infraweave executable as its
// fixed entrypoint, infraweave runner command, which is Serve. Its
// argument vector travels only in a private request frame on the
// helper's standard input: a big-endian uint32 length, then strict JSON
// of version, call, argv and timeoutMillis, at most 64 KiB, with at most
// 128 arguments of at most 4 KiB each and 32 KiB together. It never
// reaches a container's or Pod's specification. Serve executes the
// vector without a shell, with no standard input, and writes the
// command's standard output and error as frames of at most 64 KiB, each
// a kind byte and a big-endian uint32 length, then a result frame of
// call, status, exitCode and reason.
//
// Session is the launcher's side, one per evaluation: at most eight
// calls, one at a time, each within 60 seconds and the evaluation's
// remaining deadline, with at most 1 MiB of each stream per call and 4
// MiB across the evaluation, all checked before any chunk is kept. A
// command's exit status, 127 when it cannot start, reaches the policy
// with its output only when the command ended by itself. A call beyond
// its bounds, a timeout, a cancellation, output beyond its bounds, an
// image that cannot be pulled and a helper that fails, also one stopped
// before its command ends, end the helper and stop the evaluation with an
// error whose text names no argument, output or image.
//
// ref: decision 0009 § "The worker-side initialization policy"
// ref: docs/implementation/worker-pool-contracts.md § "Worker initialization and signed runs"
//
// # Input and output
//
// Serve reads its request from an io.Reader, starts one process and
// writes frames to an io.Writer. Session writes requests to, and reads
// frames from, the helpers its executor.Commander runs. Nothing is
// logged.
//
// # Dependencies
//
// It imports internal/agent/executor for the helper port and
// platform/rego for the policy's view of a call. internal/agent/launcher
// opens a Session per evaluation, and internal/agent/cli's runner
// command serves helpers.
package initcommand
