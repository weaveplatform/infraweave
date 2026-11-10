// Package source resolves the commits of a stack's repository: Service
// implements vcsapi.Sources by reading the stack's settings and asking the
// Source of the provider they name. A provider without a Source is
// refused as a *kernel.Invalid naming /provider, a repository URL the
// Source refuses as one naming /repositoryURL, and a ref the repository
// lacks as vcsapi.ErrRefNotFound. A provider the Source cannot reach keeps
// kernel.ErrUnavailable. Resolver is the same resolution as the stacks
// domain's stacksapi.StackSources, a ref the repository lacks being
// stacksapi.ErrRefNotFound. Track sets a stack's branch head and tracked
// commit to its branch's head, set by infraweave; a stack gone or without
// a source it can read is skipped, and a repository or branch its host
// lacks fails the job for good, as a retry finds it no more. The Source
// reaches the repository through the integration the stack uses, its own
// or its provider's default, which Integrations finds; a stack whose
// integration was deleted is refused as a *kernel.Invalid.
//
// # Input and output
//
// New takes the stacks domain's api, each provider's Source by its served
// VCSProvider value, and the domain's Integrations.
//
// # Dependencies
//
// It imports internal/kernel, platform/queue, the stacks api package and
// the domain's api and port packages. The domain's assembly constructs it.
package source
