// Package objectstoretest is decision 0016's contract suite for
// objectstore drivers.
//
// Run runs every case of Cases against a Bucket, each under a key prefix
// of its own: round-trips, a missing key, deletes, writes shorter and
// longer than declared, multipart writes and a multipart write whose
// source fails, and signed GET and PUT URLs with their size, SHA-256,
// method, tampering and expiry. A case a driver's emulator cannot decide
// is in Gaps, and one the service itself cannot bind is in Unbound, each
// with its reason; Skip is both, for the emulator suites, and the cloud
// job skips Unbound alone. Options.Identity is what the credentials may
// do: Full for an emulator; Writer, serve's, which runs delete-refused,
// asserting the service refuses its delete, in place of delete; or
// Deleter, the scheduler's. A run whose credentials may delete removes
// every key it named once its cases end. Cloud reads the identity and a
// driver's settings from the environment the objectstore-cloud job sets,
// skipping the driver's cloud test without them. Canceled checks that a driver over the network
// fails every call once its context ends and leaves nothing of a write
// canceled between its parts. Emulator starts a pinned emulator image,
// skipping without Docker unless CI requires it. NoMD5 is an HTTP client
// that fails the test on any request carrying an MD5.
//
// # Input and output
//
// It writes and deletes objects through the Bucket under test and sends
// the signed requests itself. Cloud reads INFRAWEAVE_TEST_CLOUD_IDENTITY
// and the variables its caller names. Failures are reported on the
// testing.T.
//
// # Dependencies
//
// It imports objectstore, testcontainers and sqltest's Docker rule. The
// driver packages' tests run it; no production code imports it.
package objectstoretest
