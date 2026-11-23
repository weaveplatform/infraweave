// Package workers holds the acceptance steps of features/workers: worker
// pools, their queue, user and usage projections, and the worker
// protocol, from registration and run assignment to
// the run specification a worker reads and the launchers that run it in
// Docker, one of which may keep sensitive outputs' values.
//
// # Input and output
//
// It sends requests to the scenario's servers through pkg/sdk as a worker
// signing with its key, seeds and reads their database through SQL, runs
// launcher containers through the Docker daemon, and installs, upgrades
// and uninstalls the infraweave-workers chart in the lab cluster through
// Helm's SDK.
//
// # Dependencies
//
// It builds on harness.World, harness.Launchers and test/deps, and on
// Helm's action and chart loader packages. go-jose
// signs as a worker and agent/wire is the worker API's client;
// agent/executor/docker labels the containers a launcher starts, and
// platform/seal opens the secrets a run's specification seals to its
// worker.
package workers
