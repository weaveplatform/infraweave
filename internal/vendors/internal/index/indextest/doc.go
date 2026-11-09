// Package indextest is the contract suite every release index reader
// passes. Check reads an index and checks that it lists releases, every
// one stable, newest first and each once, that it lists the releases
// Facts names, and that each of their constraints resolves to its
// release, and one no release meets to none; Run fails a test with what
// Check finds. TerraformFacts and OpenTofuFacts are true of the vendors'
// real indexes and of the fixtures recorded from them. Load reads a
// fixture make vendors-fixtures recorded, and HashiCorp and OpenTofu serve
// one as the vendor serves it, so every pull request runs the suite
// against the real format without the internet; the nightly
// vendors-upstream job runs it against the real indexes.
//
// ref: decision 0016
//
// # Input and output
//
// Load reads a fixture file; HashiCorp and OpenTofu answer HTTP requests
// from a fixture's body; Check reads the index it is given.
//
// # Dependencies
//
// It imports chi for the fixture servers, iac/version and the domain's
// index package. The index package's and the domain's tests import it.
package indextest
