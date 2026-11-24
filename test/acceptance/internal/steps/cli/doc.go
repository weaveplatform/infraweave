// Package cli holds the acceptance steps of features/cli, which drive the
// built infraweavectl binary, one feature per area of the CLI: the
// operator's commands, its profiles, whoami, version, the feature preview
// commands and the problems it prints.
//
// # Input and output
//
// It runs infraweavectl as a subprocess against the scenario's servers,
// with a profiles file and an environment of the scenario's own, and
// reads what it prints. A profile holds the operator key, a tenant's key,
// which it seeds first, or one of the scenario's named tokens.
//
// # Dependencies
//
// It builds on harness.World, harness.CtlBinary and the operator key and
// tenants in harness; pkg/sdk names the problems infraweavectl prints.
package cli
