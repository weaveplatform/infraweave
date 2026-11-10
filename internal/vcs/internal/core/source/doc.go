// Package source resolves the commits of a stack's repository: Service
// implements vcsapi.Sources by reading the stack's settings and asking the
// Source of the provider they name. A provider without a Source is
// refused as a *kernel.Invalid naming /provider, and a ref the repository
// lacks as vcsapi.ErrRefNotFound.
//
// # Input and output
//
// New takes the stacks domain's api and each provider's Source by its
// served VCSProvider value.
//
// # Dependencies
//
// It imports internal/kernel, the stacks api package and the domain's api
// and port packages. The domain's assembly constructs it.
package source
