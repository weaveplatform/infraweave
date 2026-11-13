// Package runs holds the acceptance steps of features/runs: the run
// queue, the scheduling of runs, a run's logs, which it writes as a
// worker does and reads back, and local previews, whose workspace
// archives it builds, uploads and reads back from a run's specification.
//
// # Input and output
//
// It sends requests to the scenario's servers through pkg/sdk, uploads
// and downloads objects through the store's signed URLs, and seeds and
// reads their database through SQL.
//
// # Dependencies
//
// It builds on harness.World, and test/deps's Eventually for what settles
// after an answer.
package runs
