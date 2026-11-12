// Package platform holds the acceptance steps of features/platform: what
// the platform runs beside the API. They cover startup refusals, health
// and observability, the queue's periodic tasks, dead letters and stats,
// secrets at rest and their key rotation, signed URLs, purges and the
// operator API.
//
// # Input and output
//
// It sends requests to the scenario's servers through pkg/sdk, runs
// infraweave as a subprocess, seeds and reads their
// database through SQL, and reads the object storage the servers write.
//
// # Dependencies
//
// It builds on harness.World and test/deps. The secrets steps load the
// keyring and serve's blob store from platform/crypt, platform/seal,
// platform/objectstore and platform/queue, through pgx and platform/tx, to
// seal and read what no operation serves yet.
package platform
