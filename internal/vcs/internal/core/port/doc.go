// Package port declares the vcs domain's ports: Source, which each VCS
// provider implements to resolve refs, read files and list the files two
// commits differ in, over a Repository; ErrNotFound when the repository,
// ref or path is missing, ErrRepositoryURL for a repository URL a Source
// refuses, and ErrTooLarge for a file over MaxFileSize; and Tester, which
// a provider implements to check an Integration, its Settings and opened
// secrets, with Checks of each kind. The sourcetest package is the
// contract every Source passes.
//
// # Input and output
//
// None; it declares types and an interface.
//
// # Dependencies
//
// It imports the standard library and the domain's api package. The
// domain's core packages call it; the provider adapters implement it.
package port
