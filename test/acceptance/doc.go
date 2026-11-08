// Package acceptance runs the Gherkin features in features/ with godog
// against the binary a user runs. TestAccFeatures builds
// cmd/infraweave with coverage once, gives each scenario that needs a server
// its own infraweave all subprocess (or scheduler, for a scenario about that
// mode), reading its listeners from the JSON logs on its stdout, and starts
// the dependencies a
// selected scenario's tags name (@postgres, @objectstore, @oidc, @vault) once, on
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
// ref: decision 0016, report 30
//
// # Input and output
//
// It reads features/, test/fixtures/captures, the open-questions register
// in docs/data/open-questions.json and the committed reference and bundle,
// builds and runs infraweave, starts containers through the Docker daemon,
// and writes godog's cucumber report and the subprocesses' coverage to make
// accept's -test.gocoverdir, cover/accept.
//
// # Dependencies
//
// godog runs the features; testcontainers-go starts PostgreSQL (images.go),
// shared or a scenario's own (OwnPostgres, on a fixed port so it can stop
// and start, and which the moby client pauses; OldPostgres, below the
// floor),
// RustFS and Vault; chi serves the in-process OIDC issuer. The secrets
// steps load the keyring and serve's blob store from internal/platform to
// seal and read what no operation serves yet, and run infraweave secrets
// as a subprocess. Every step and every
// capture replay calls the API through pkg/sdk, never the generated client
// beneath it (TestUnitStepsUseTheSDK).
package acceptance
