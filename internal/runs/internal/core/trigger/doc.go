// Package trigger creates a run on a stack at a caller's request: Service
// resolves the commit the caller names through the vcs domain, or takes
// the stack's tracked commit or else its branch head, refusing a stack
// with neither with ErrNoCommit, and creates the run through the
// lifecycle, which answers a run of the stack, type and commit that has
// not ended instead of a second and refuses a disabled or locked stack.
// It answers the run as a View, with its stack's branch and its
// transitions. Store is its storage port.
//
// # Input and output
//
// None of its own: it reaches storage through Store, the stacks domain
// through stacksapi.Stacks, the vcs domain through stacksapi.StackSources
// and runs through the lifecycle.
//
// # Dependencies
//
// It imports stacksapi, the domain's lifecycle, internal/kernel and
// platform/tx. The runs domain's assembly constructs it.
package trigger
