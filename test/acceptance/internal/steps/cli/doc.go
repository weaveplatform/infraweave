// Package cli holds the acceptance steps of features/cli, which drive the
// built infraweavectl binary: one feature per area of what the CLI must
// cover, starting with the operator's commands, its profiles, whoami,
// version and the problems it prints.
//
// # Input and output
//
// It runs infraweavectl as a subprocess against the scenario's servers,
// with a profiles file and an environment of the scenario's own, and
// reads what it prints.
//
// # Dependencies
//
// It builds on harness.World, harness.CtlBinary and the operator key in
// harness; pkg/sdk names the problems infraweavectl prints.
package cli
