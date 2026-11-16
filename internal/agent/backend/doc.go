// Package backend points a run at its stack's managed state backend.
// Configure, for a handoff whose specification names stateBackend, writes
// File at the run's project root with Terraform's and OpenTofu's backend
// "http" block, its state, lock and unlock addresses all the stack's
// backend URL, and answers the phase environment's credentials: Username
// and the run token, which no file in the workspace holds. Address joins
// the backend's path to the server URL the launcher reaches, keeping a
// relay's or a private address's host and port. Code that declares its
// own backend fails initialization, since the tool refuses two.
//
// A Terragrunt stack's run writes File into each of its Units instead:
// each directory under the project root with a terragrunt.hcl, found as
// terragrunt run --all finds them, outside .git, .terraform and
// .terragrunt-cache and never through a link. A unit's workspace is its
// path from the project root, the default workspace for a unit at the
// root, and its address names it as one path segment, each / escaped as
// %2F, which the state backend decodes back. Before any file is written
// every unit is checked, and the first that fails is refused naming it: a
// name over MaxWorkspaceName bytes or not UTF-8, remote_state in its
// terragrunt.hcl or in the file an include names, a generate block whose
// contents declare a backend, or a backend or cloud block in a Terraform
// file of its directory. An include's path is worked out from literals
// and templates of find_in_parent_folders, get_terragrunt_dir,
// get_repo_root and get_path_to_repo_root, each answering within the
// workspace; one that cannot be is not read. A terragrunt.stack.hcl is
// refused, since Terragrunt generates its units when the run runs, where
// no backend file reaches them.
//
// ref: decision 0010 § "How a run reaches it"
// ref: decision 0010 § "Endpoints"
// ref: decision 0008 § "The run specification and its secrets"
//
// # Input and output
//
// Configure takes the run's workspace, the project root's directory under
// it and a handoff, writes one file there or in each unit, and answers
// environment entries as NAME=value strings; its errors name the unit and
// what it declares, and are ErrAddress, ErrOwnBackend, ErrUnitName or
// ErrStackFile where they say so. Units answers the units' names. Every
// file a unit's check reads is read through the workspace opened as a
// root, at most 1 MiB of it. Nothing is logged.
//
// # Dependencies
//
// It imports internal/agent/client's BasePath, internal/agent/handoff,
// hashicorp/hcl/v2's hclsyntax and json parsers and go-cty, which
// evaluate a Terragrunt file's include paths, and the standard library.
// The runner calls it before a run's first phase.
package backend
