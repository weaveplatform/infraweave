// Package trigger creates a run on a stack at a caller's request: Service
// resolves the commit the caller names through the vcs domain, or takes
// the stack's tracked commit or else its branch head, refusing a stack
// with neither with ErrNoCommit, and creates the run through the
// lifecycle, which answers a run of the stack, type and commit that has
// not ended instead of a second and refuses a disabled or locked stack.
// It answers the run as a read.View. Before creating a Terragrunt run it
// checks the runtime YAML's terragrunt block and refuses malformed YAML or
// a non-boolean use_run_all at /runtimeConfig/yaml; other vendors leave it
// unread. A proposed run may carry
// environment variable overrides, which CheckOverrides bounds and the
// lifecycle keeps sealed; a run with overrides is never answered in place
// of a second. Task creates a TASK run of a command, kernel.Task bounds,
// on the tracked commit, and every task is a run of its own.
//
// Batch creates a TRACKED run on each of up to MaxBatch stacks in one
// transaction, locking their rows in ID order, and refuses the whole
// batch with every entry's refusal at its index. Destroy is the
// stacksapi.StackDestroyer a stack's delete with destroyResources creates
// its DESTROY run through, in the delete's transaction.
//
// A local preview takes two calls on a stack that enables local
// previews. UploadWorkspace records a pending tar.gz of a declared size,
// at most MaxWorkspace, and SHA-256 in the uploads store and signs its
// PUT. ProposeWorkspace commits that upload and creates a PROPOSED run on
// it, with the stack's tracked commit as its commit's metadata and any
// overrides; the run's specification then names the archive as its
// source.
//
// # Input and output
//
// None of its own: it reaches the stacks domain through stacksapi.Stacks,
// the vcs domain through stacksapi.StackSources, the access domain's
// decisions through accessapi.Authorizer, runs through the lifecycle,
// their views through the read service, and the uploads store through
// Objects.
//
// # Dependencies
//
// It imports iac/terragrunt, accessapi, stacksapi, the domain's lifecycle and read,
// internal/kernel, platform/objectstore and its blobs, and platform/tx.
// The runs domain's assembly constructs it.
package trigger
