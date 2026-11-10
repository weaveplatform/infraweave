// Package port declares the vcs domain's ports: Source, which each VCS
// provider implements to resolve refs, read files and list the files two
// commits differ in, over a Repository; ErrNotFound when the repository,
// ref or path is missing.
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
