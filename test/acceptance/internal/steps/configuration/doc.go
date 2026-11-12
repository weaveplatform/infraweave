// Package configuration holds the acceptance steps of
// features/configuration: contexts and the config elements of stacks.
//
// # Input and output
//
// It sends requests to the scenario's servers through pkg/sdk and seeds
// and reads their database through SQL.
//
// # Dependencies
//
// It builds on harness.World, and test/deps's Eventually for what settles
// after an answer.
package configuration
