// Package exec executes a run inside its container. Run reports that the
// state the run's preparation leads to started, which ends the handover,
// then follows the server's answers: execute runs a state's phase, pause
// ends the runner while the run waits for a decision, and exit runs the
// after_run hooks with TF_VAR_infraweave_final_run_state and ends it. A
// report whose answer is lost is sent again with the same body, and one
// refused with 409 ends the runner with no further report.
//
// Before its first phase the runner writes the specification's mounted
// files under the workspace, the managed state backend's file at the
// project root, and builds the phase environment: the container's, the
// specification's variables with their write-only values opened, the
// in-run API token, the backend's credentials, TF_IN_AUTOMATION and
// TF_INPUT. One sh session runs every phase, so a variable a hook exports
// is set in every later hook and command. A phase sets
// TF_VAR_infraweave_run_state, then runs its before-hooks, its vendor's
// commands and its after-hooks, each in a group of its own with no
// standard input, stopping at the first that fails, whose exit status the
// failed report's details name. Planning's succeeded report carries the
// delta internal/iac/plan reads from the plan's JSON.
//
// ref: decision 0008 § "Phases, reports and the plan decision"
// ref: decision 0010 § "How a run reaches it"
//
// # Input and output
//
// It writes files under the workspace and a temporary directory, runs sh
// and the tool, which write to the output it is given, and sends reports
// through a Reporter. It logs each phase, the run's end and each report
// it sends again.
//
// # Dependencies
//
// It imports internal/agent/backend, client, handoff, vendors and wire,
// and internal/iac/plan. internal/agent/runner runs it.
package exec
