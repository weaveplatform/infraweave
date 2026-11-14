// Package surface is the vendors domain's expression surface: the
// workflow command templates of .infraweave/workflow.yml, whose variables
// are the run parameters internal/iac/workflow declares. It is a shell
// surface, so the editor checks, completes and evaluates a template as
// the runner renders it, every embedded value one shell word, with no now
// and no random.*. A context is a vendor named by its kind, which every
// principal reads, with sample run parameters.
//
// ref: decision 0024 "Workflow command templates"
//
// # Input and output
//
// Workflow takes nothing and returns the surface; its Env describes a
// vendor, or answers expr.ErrNoContext for a kind no vendor has. Nothing
// is stored or logged.
//
// # Dependencies
//
// internal/platform/expr, internal/iac and iac/workflow, and
// internal/kernel. The vendors domain's assembly declares it, and
// internal/app registers it.
package surface
