// Package authz holds the acceptance steps of features/authz: spaces,
// roles, role bindings, the grant views, live sessions under changed
// authorization, and tenancy between accounts.
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
package authz
