// Package executor is the launcher's port to the runtime that runs its
// runs: an Executor starts a Job's container with the host paths it
// mounts, handing its runner the launcher's handoff on standard input, and finds a worker's container
// again after the launcher restarts; a Container waits for its end, stops
// or kills its runner, and is removed. A Commander also runs an
// initialization command's helper, a fresh container of the run's image
// that mounts nothing but the infraweave executable, receives its
// request on standard input, and is removed once the call ends.
// executor/docker and executor/kubernetes are the implementations, and
// internal/agent/agenttest has a fake for the launcher's tests.
//
// ref: decision 0008 § "The launcher and the runner"
//
// # Input and output
//
// It declares types and does no I/O.
//
// # Dependencies
//
// It imports internal/agent/wire for an exit's reason. internal/agent/launcher
// drives it.
package executor
