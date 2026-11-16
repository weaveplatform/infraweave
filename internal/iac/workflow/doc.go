// Package workflow declares the command templates of a run's workflow:
// the nine keys of .infraweave/workflow.yml, one per command the runner
// may need, the run parameters a template reads, and the built-in
// templates of Terraform and OpenTofu, and of a Terragrunt run, which
// runs each of their commands across its units through terragrunt run
// --all, which a run without a workflow file renders. Each value is a text template whose literal text is
// shell; the expression engine quotes every value it embeds. The vendors
// domain declares the templates' editor surface from Variables, and the
// runner renders them.
//
// ref: decision 0024 "Workflow command templates"
//
// # Input and output
//
// None. Parse reads a workflow file's bytes into its templates and the
// line of each, or ErrFile naming every key at fault; it opens no file.
//
// # Dependencies
//
// It imports the standard library and gopkg.in/yaml.v3, so the agent and
// the server both link it.
package workflow
