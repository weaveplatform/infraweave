// Package terraform is infraweave's knowledge of Terraform as a stack's
// vendor: Kind, the vendorConfig member that holds its settings, and
// ValidateSettings, which checks them when a stack is written so that a
// setting Terraform would refuse fails the write rather than the stack's
// first run.
//
// # Input and output
//
// None. It checks JSON in memory.
//
// # Dependencies
//
// It imports the standard library, internal/kernel for the field errors
// and the settings checks it shares with opentofu. internal/iac registers
// it.
package terraform
