// Package acceptance runs the Gherkin features in features/ with godog
// against the binary a user runs (decision 0016). TestAccFeatures builds
// cmd/infraweave with coverage once, gives each scenario that needs a server
// its own infraweave all subprocess (or scheduler, for a scenario about that
// mode), reading its listeners from the JSON logs on its stdout, and starts
// the dependencies a
// selected scenario's tags name (@postgres, @objectstore, @oidc) once, on
// first use. Without a Docker daemon it skips the scenarios that need one,
// unless INFRAWEAVE_ACCEPT_REQUIRE_DOCKER=1, which fails the run instead. A
// failed scenario leaves its evidence in out/<scenario>/. make accept
// PARTITION=core|runs|integrations selects one CI partition of Partitions.
//
// TestAccCaptures replays the capture fixtures in test/fixtures/captures
// (LoadCaptures, Replay) against the same binary; a failed expectation is a
// parity bug named by its capture. TestUnitCaptureRedaction fails on a
// fixture that Leaks a credential or a real e-mail address.
//
// # Input and output
//
// It reads features/, test/fixtures/captures, the report 30 register and
// the committed reference and bundle, builds and runs infraweave, starts
// containers through the Docker daemon, and writes godog's cucumber report
// and the subprocesses' coverage to make accept's -test.gocoverdir,
// cover/accept.
//
// # Dependencies
//
// godog runs the features; testcontainers-go starts PostgreSQL (images.go),
// shared or a scenario's own (OwnPostgres, which the moby client pauses),
// and RustFS; chi serves the in-process OIDC issuer. Every step and every
// capture replay calls the API through pkg/sdk, never the generated client
// beneath it (TestUnitStepsUseTheSDK).
package acceptance
