// Package port declares the vcs domain's ports: Source, which each VCS
// provider implements to resolve refs, read files and list the files two
// commits differ in, over a Repository; ErrNotFound when the repository,
// ref or path is missing, ErrRepositoryURL for a repository URL a Source
// refuses, and ErrTooLarge for a file over MaxFileSize; and Tester, which
// a provider implements to check an Integration, its Settings and opened
// secrets, with Checks of each kind; Browse, which lists the repositories
// an integration reaches and a repository's branches; Apps, the GitHub
// provider's App work, reporting an App, writing a manifest form and
// converting the code its host hands back; and RateLimited, a call its
// host refused until a reset time; Minter, which mints a run a Credential
// that reads one repository alone and revokes it, ErrCannotMint when the
// integration may not mint one; and Lender, which lends a run the
// integration's own token instead. The sourcetest package is the contract
// every Source passes.
//
// # Input and output
//
// None; it declares types and interfaces.
//
// # Dependencies
//
// It imports the standard library, internal/kernel and the domain's api
// package. The domain's core packages call it; the provider adapters
// implement it.
package port
