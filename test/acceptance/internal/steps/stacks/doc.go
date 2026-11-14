// Package stacks holds the acceptance steps of features/stacks: stacks,
// their search, and the wait for a stack's deletion that a destroy run
// commits.
//
// # Input and output
//
// It sends requests to the scenario's servers through pkg/sdk and seeds
// their database through SQL.
//
// # Dependencies
//
// It builds on harness.World.
package stacks
