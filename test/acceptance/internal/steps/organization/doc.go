// Package organization holds the acceptance steps of features/organization:
// the organization's members and keys, its accounts and their limits,
// managed users, and the account reads and settings.
//
// # Input and output
//
// It sends requests to the scenario's servers through pkg/sdk and the
// browser, and seeds and reads their database through SQL.
//
// # Dependencies
//
// It builds on harness.World, and kernel for the IDs it seeds.
package organization
