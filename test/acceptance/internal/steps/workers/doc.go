// Package workers holds the acceptance steps of features/workers: worker
// pools and the worker protocol, from registration and run assignment to
// the run specification a worker reads and the launchers that run it in
// Docker.
//
// # Input and output
//
// It sends requests to the scenario's servers through pkg/sdk as a worker
// signing with its key, seeds and reads their database through SQL, and
// runs launcher containers through the Docker daemon.
//
// # Dependencies
//
// It builds on harness.World, harness.Launchers and test/deps. go-jose
// signs as a worker and agent/wire is the worker API's client;
// agent/executor/docker labels the containers a launcher starts, and
// platform/seal opens the secrets a run's specification seals to its
// worker.
package workers
