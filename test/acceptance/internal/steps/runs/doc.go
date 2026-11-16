// Package runs holds the acceptance steps of features/runs: the run
// queue, the scheduling of runs, a run's logs and CPU profiles, which it
// uploads as a worker does and reads back, a profile as pprof, local
// previews, whose workspace archives
// it builds, uploads and reads back from a run's specification, and a
// run's changes, whose document it writes from internal/iac's recorded
// plans and seals and commits as a worker does; and it checks that a
// value, such as an override's, is named in no answer and no server's
// log, and whether an output a run kept holds a sealed value. It moves
// the clock on for one run or one worker by ageing its timestamps, so
// run.timers and worker.reap reach their boundaries without a wait, and
// reads the timer a run_updated event names.
//
// # Input and output
//
// It sends requests to the scenario's servers through pkg/sdk, uploads
// and downloads objects through the store's signed URLs, and seeds and
// reads their database through SQL.
//
// # Dependencies
//
// It builds on harness.World, test/deps's Eventually for what settles
// after an answer, internal/iac/plan, platform/seal and
// github.com/google/pprof/profile.
package runs
