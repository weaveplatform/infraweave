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
// Credential hands a run the token it fetches its source with: one the
// provider's Minter mints to read the repository alone for TokenLife,
// kept in Tokens by its ID so Release can revoke it, or, where the
// integration may not mint one or the provider mints none, the
// integration's own token from its Lender, which the log notes without
// the token. Release enqueues a Revoke job keyed by the run, and Revoked,
// its handler, revokes each kept token through its integration and
// forgets it; a token whose integration was deleted is forgotten and
// expires alone.
//
// ref: decision 0012 § "Source access for runs"
//
// # Input and output
//
// New takes Deps: the stacks domain's api, each provider's Source by its
// served VCSProvider value, the domain's Integrations, the Tokens, the
// queue Release enqueues on in its caller's transaction, the clock and a
// log.
//
// # Dependencies
//
// It imports the standard library, internal/kernel, platform/queue, the
// stacks api package and the domain's api and port packages. The domain's assembly constructs it.
package source
