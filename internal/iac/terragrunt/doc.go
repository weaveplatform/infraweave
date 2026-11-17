// Package terragrunt is infraweave's knowledge of Terragrunt as a stack's
// vendor: Kind, the vendorConfig member that holds its settings, and
// ValidateSettings, which checks them when a stack is written. A
// Terragrunt stack drives OpenTofu or Terraform, which its tool names;
// the manually provisioned tool, smart sanitization, reusing the saved
// plan in apply and prefixing resource names are refused, as infraweave
// does not run them yet. Read decodes the settings the server reads, and
// Settings' Driven names the vendor kind of the tool whose release a run
// mounts beside Terragrunt's. ManagesState decides whether a Terragrunt
// stack keeps its state in infraweave's backend: useStateManagement when
// it is set, and the stack's manageState when it is null. RuntimeRunAll
// reads a run's YAML terragrunt block: use_run_all decides the mode, false
// when omitted, and an absent block leaves the stack's mode in effect.
//
// ref: decision 0008 § "The run specification and its secrets"
// ref: decision 0010 § "How a run reaches it"
//
// # Input and output
//
// None. It checks and decodes JSON and runtime YAML in memory.
//
// # Dependencies
//
// It imports the standard library, gopkg.in/yaml.v3, internal/kernel for field errors,
// the settings checks it shares with the other vendors, and terraform and
// opentofu for the kinds of the tools it drives. internal/iac registers
// it.
package terragrunt
