// Package vendorsfixtures records the vendors' release indexes as the
// index readers' test fixtures (make vendors-fixtures). Run fetches each
// Source as its index serves it, all of HashiCorp's pages, and keeps its
// fixed set of releases, the newest stable release and the newest
// prerelease, in the index's order, dropping the download lists no reader
// reads; it writes each as a Fixture naming its source and the date it
// was fetched. The fixed sets cover every shape the readers handle, so a
// later run changes only the fetch date and the newest releases.
//
// # Input and output
//
// Run sends GET requests to each source's index and writes
// <name>.json to the directory it is given
// (internal/vendors/internal/index/testdata).
//
// # Dependencies
//
// It imports the standard library and iac/version. tools/vendorsfixtures
// runs it.
package vendorsfixtures
