// Package trigger creates a run on a stack at a caller's request: Service
// resolves the commit the caller names through the vcs domain, or takes
// the stack's tracked commit or else its branch head, refusing a stack
// with neither with ErrNoCommit, and creates the run through the
// lifecycle, which answers a run of the stack, type and commit that has
// not ended instead of a second and refuses a disabled or locked stack.
// It answers the run as a read.View.
//
// A local preview takes two calls on a stack that enables local
// previews. UploadWorkspace records a pending tar.gz of a declared size,
// at most MaxWorkspace, and SHA-256 in the uploads store and signs its
// PUT. ProposeWorkspace commits that upload and creates a PROPOSED run on
// it, with the stack's tracked commit as its commit's metadata; the
// run's specification then names the archive as its source.
//
// # Input and output
//
// None of its own: it reaches the stacks domain through stacksapi.Stacks,
// the vcs domain through stacksapi.StackSources, runs through the
// lifecycle, their views through the read service, and the uploads store
// through Objects.
//
// # Dependencies
//
// It imports stacksapi, the domain's lifecycle and read, internal/kernel,
// platform/objectstore and its blobs, and platform/tx. The runs domain's
// assembly constructs it.
package trigger
