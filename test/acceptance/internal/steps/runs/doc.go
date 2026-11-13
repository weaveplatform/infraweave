// Package runs holds the acceptance steps of features/runs: the run
// queue, the scheduling of runs, and a run's logs, which it writes as a
// worker does and reads back.
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
