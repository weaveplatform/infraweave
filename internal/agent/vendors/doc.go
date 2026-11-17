// Package vendors holds the runner's workflow: the command templates a
// run executes in each phase, and the registry that renders them on the
// workflow surface. Defaults answers a tool's built-in workflow, the
// templates internal/iac/workflow declares under its binary, and
// Terragrunt a Terragrunt run's, which runs each command across the
// stack's units through terragrunt run --all or in the project root's
// unit alone through terragrunt run --; Read answers the workflow a
// .infraweave/workflow.yml holds. Either checks every key
// before any command runs, a fault naming the key, its line in the file
// and its range. Commands renders a worker state's phase with the run's
// parameters when the phase runs: init; plan, then showPlan with its
// output to the plan's JSON file; apply, then getOutputs with its output
// to the outputs' JSON file; and destroy. A Terragrunt run's planning
// runs plan alone, with TG_JSON_OUT_DIR naming the directory Terragrunt
// writes each unit's plan's JSON under, and its applying apply alone.
// Command also renders showPlan for a single-unit run that writes no JSON
// there; a run-all phase never falls back to showPlan.
// Every value a template embeds is one shell word, so no workspace name
// or target becomes shell syntax.
//
// ref: decision 0008 § "Phases, reports and the plan decision"
// ref: decision 0024 "Workflow command templates"
//
// # Input and output
//
// None. It takes a workflow file's bytes and answers shell lines in
// memory; it reads no file and runs nothing.
//
// # Dependencies
//
// It imports internal/agent/wire's models, internal/iac/workflow,
// internal/platform/expr and the standard library. internal/agent/exec
// reads the workflow file and runs the lines it answers.
package vendors
