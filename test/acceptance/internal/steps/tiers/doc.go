// Package tiers holds the acceptance steps of features/tiers: the platform
// tiers, the billing switch and feature flags.
//
// # Input and output
//
// It sends requests to the scenario's servers through pkg/sdk and seeds
// and reads their database through SQL.
//
// # Dependencies
//
// It builds on harness.World, test/deps's Eventually for what settles
// after an answer, and kernel for the IDs it seeds.
package tiers
