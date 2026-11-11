// Package sourcetest is the contract suite every port.Source passes.
//
// ref: decision 0016
//
// Check resolves a branch, an annotated tag and a SHA that is no ref's
// tip, reads a file at a commit and at a branch, lists the paths two
// commits differ in, and checks that an unknown ref, SHA, repository,
// path and base are port.ErrNotFound and that the source URL is set. Run
// fails a test with every clause Check reports broken. Repository
// describes what the repository under test holds, and Lab what a
// repository holds once vcslab's history is pushed to it.
//
// For the scheduled vcs-lab job, LabEnv reads the lab a provider's test
// runs against, and Reads and Scope check which repositories a run's
// credential may fetch, the way git does.
//
// # Input and output
//
// It reads through the Source under test and writes nothing. Failures are
// errors wrapping ErrContract, reported on the testing.T by Run. Reads
// sends one GET for a repository's smart-HTTP ref advertisement.
//
// # Dependencies
//
// It imports the standard library, the domain's port package and
// vcslab. The providers' tests run it; no production code imports it.
package sourcetest
