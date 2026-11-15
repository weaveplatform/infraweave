// Package plan reads a Terraform or OpenTofu plan into a run's changes and
// delta, and writes and reads the changes document a run's changes.json
// holds. Parse reads the JSON that `show -json` writes of a saved plan,
// format version 1.x: each resource_changes entry becomes a Change, its
// type from its actions, IMPORT for one that imports, and moved for one
// with a previous address; the Delta counts the managed resources' changes
// as the tool's own summary does, the managed resources of prior_state,
// and whether an output changes. Both vendors register Parse, since
// OpenTofu writes Terraform's format. EntityOf splits a change's address
// into its module, resource type and name. ReadOutputs reads the JSON
// that `output -json` writes into the root module's outputs, a sensitive
// one with no value among them, WithoutSensitiveValues writes that JSON
// again with each sensitive output's value dropped, and Descriptions
// reads a plan's JSON for its configuration's output descriptions.
//
// ref: decision 0008 § "Phases, reports and the plan decision"
//
// # Input and output
//
// Parse takes a reader of a plan's JSON and answers a Plan, or
// ErrTooLarge, ErrMalformed or ErrFormat. Write writes changes as the
// changes document, version DocumentVersion, and Read reads one back,
// refusing the same way. ReadOutputs answers Outputs by name,
// WithoutSensitiveValues writes the outputs' JSON to a writer, and
// Descriptions descriptions by name, refusing a document past MaxSize as
// ErrTooLarge and one that is not theirs as ErrMalformed. Nothing it reads panics, which FuzzPlan holds it
// to.
//
// # Dependencies
//
// It imports the standard library alone, so the agent and the server both
// link it.
package plan
