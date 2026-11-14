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
// Before that report, planning saves its results: the plan's JSON and its
// changes, each sealed in platform/seal's object stream under the content
// key the run's artifact key derives for its object, which the server
// opens, and, for a tracked run whose plan changes something, the
// workspace with the binary plan, sealed to the pool's public key through
// internal/agent/workspace, which the server cannot open. Each is written
// to the run's temporary directory, declared, PUT to its pre-signed URL
// and recorded, every step taken again for up to ten minutes while the
// worker API or the store is unavailable; a save that fails fails the
// phase. A run that resumes in a new container to apply or replan its
// saved workspace runs the init hooks and command first, since the
// workspace was saved without its providers.
//
// The session's output, and the runner's own lines, go to the run's log,
// internal/agent/runlog, which masks the run's secrets: its write-only
// values and files, its tokens, the values of the variables the
// launcher's mask list names, and the well-known patterns when the stack
// enables them. Each phase's lines are in its state's chunks, flushed
// before the phase's report; the after_run hooks' go to standard output
// alone.
//
// Before it returns, a runner whose workspace another user owns, as a
// launcher that is not root hands it, makes every directory the run made
// writable by every user, so that launcher removes the workspace.
//
// ref: decision 0008 § "Phases, reports and the plan decision"
// ref: decision 0008 § "Logs and artifacts"
// ref: decision 0013 § "Masking"
// ref: decision 0010 § "How a run reaches it"
//
// # Input and output
//
// It writes files under the workspace and a temporary directory, changes
// the modes of the workspace's directories it made, runs sh
// and the tool, whose output it masks onto the output it is given and
// uploads, sends reports through a Reporter and log chunks through a
// runlog.API, and uploads and records its artifacts through Uploads. It
// logs each phase, the run's end, each report or upload it sends again
// and a log it could not flush.
//
// # Dependencies
//
// It imports internal/agent/backend, client, handoff, runlog, vendors,
// wire and workspace, internal/iac/plan, internal/kernel for the objects'
// keys, and platform/mask and platform/seal. internal/agent/runner runs
// it.
package exec
