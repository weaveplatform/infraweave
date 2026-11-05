// Package acceptance runs the Gherkin features in features/ with godog
// against the binary a user runs (decision 0016). TestAccFeatures builds
// cmd/infraweave with coverage once, gives each scenario that needs a server
// its own infraweave serve subprocess, and starts the dependencies a
// selected scenario's tags name (@postgres, @objectstore, @oidc) once, on
// first use. Without a Docker daemon it skips the scenarios that need one,
// unless INFRAWEAVE_ACCEPT_REQUIRE_DOCKER=1, which fails the run instead. A
// failed scenario leaves its evidence in out/<scenario>/. make accept
// PARTITION=core|runs|integrations selects one CI partition of Partitions.
//
// # Input and output
//
// It reads features/ and the committed reference and bundle, builds and
// runs infraweave, starts containers through the Docker daemon, and writes
// godog's cucumber report and the subprocesses' coverage to make accept's
// -test.gocoverdir, cover/accept.
//
// # Dependencies
//
// godog runs the features; testcontainers-go starts PostgreSQL (images.go)
// and RustFS; chi serves the in-process OIDC issuer.
package acceptance
