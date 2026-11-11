// Package trigger creates a run on a stack at a caller's request: Service
// resolves the commit the caller names through the vcs domain, or takes
// the stack's tracked commit or else its branch head, refusing a stack
// with neither with ErrNoCommit, and creates the run through the
// lifecycle, which answers a run of the stack, type and commit that has
// not ended instead of a second and refuses a disabled or locked stack.
// It answers the run as a read.View.
//
// # Input and output
//
// None of its own: it reaches the stacks domain through stacksapi.Stacks,
// the vcs domain through stacksapi.StackSources, runs through the
// lifecycle and their views through the read service.
//
// # Dependencies
//
// It imports stacksapi, the domain's lifecycle and read, internal/kernel
// and platform/tx. The runs domain's assembly constructs it.
package trigger
