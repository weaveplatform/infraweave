// Package iac is infraweave's knowledge of the infrastructure-as-code
// tools a stack runs, which the server and the agent share. Vendor is one
// tool's knowledge, named by its Kind: the member of a stack input's
// vendorConfig that holds its settings. Lookup and Vendors read the
// registry of the vendors infraweave runs, Terraform and OpenTofu.
// ValidateSettings checks a stack input's vendor settings when the stack
// is written: vendorConfig names at most one vendor, a registered one,
// whose settings that vendor accepts, and the top-level terraformVersion
// is a version constraint. It answers a *kernel.Invalid with a JSON Pointer
// into the stack input for each field refused, which the HTTP layer
// answers with 422 validation-failed. A Vendor's ParsePlan reads the
// tool's JSON of a saved plan into the run's changes and delta.
//
// ref: decisions 0004 and 0022
//
// # Input and output
//
// None. It checks values and reads plans in memory, from the reader it is
// given; it runs no tool and opens no file.
//
// # Dependencies
//
// It imports the standard library, internal/kernel, iac/version, iac/plan
// and the vendor packages it registers, terraform and opentofu. depguard's
// iac-pure rule keeps every package under internal/iac to those and its
// own tree, so the agent can link it without a domain or a database
// driver.
package iac
