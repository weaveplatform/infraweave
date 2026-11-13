// Package vendors holds the command sets the runner calls in each phase,
// one per tool a run's specification names. Lookup answers the Vendor of
// a tool; its Commands are the commands of a worker state's phase, each
// an argument list and the file its standard output goes to. Terraform
// and OpenTofu share one command set under their own binaries: init,
// plan to PlanFile and show of it as JSON, and apply of PlanFile, with
// the launcher's lock timeout on each command that can lock.
//
// ref: decision 0008 § "Phases, reports and the plan decision"
// ref: decision 0010 § "How a run reaches it"
//
// # Input and output
//
// None. It answers argument lists in memory; it runs nothing.
//
// # Dependencies
//
// It imports internal/agent/wire's models and the standard library.
// internal/agent/exec runs the commands it answers.
package vendors
