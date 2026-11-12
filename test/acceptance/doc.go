// Package acceptance runs the Gherkin features in features/ with godog
// against the binary a user runs. Each area of the product has its
// features in features/<area>/<topic>.feature and its steps in
// internal/steps/<area>, whose Register adds them to every scenario;
// TestAccFeatures hands internal/harness the areas, and the harness gives
// each scenario a World the areas' steps share. No step group reaches
// into another, and no step text matches the steps of two areas.
//
// TestAccFeatures builds cmd/infraweave with coverage once, gives each
// scenario that needs a server its own infraweave all subprocess (or
// scheduler, for a scenario about that mode), reading its listeners from
// the JSON logs on its stdout, and starts the dependencies a selected
// scenario's tags name (@postgres, @objectstore, @oidc, @vault, @vcs) once,
// on first use, from test/deps. Without a Docker daemon it skips the
// scenarios that need one, unless INFRAWEAVE_ACCEPT_REQUIRE_DOCKER=1, which
// fails the run instead. A failed scenario leaves its evidence in
// out/<scenario>/. make accept PARTITION=core|runs|integrations selects one
// CI partition of Partitions; PARTITION=vcs-lab selects the @vcs-lab
// scenarios, which reach the real VCS providers only the scheduled vcs-lab
// job provisions, and which every other run excludes.
// INFRAWEAVE_ACCEPT_TAGS narrows a run further, as the job does to one
// provider's @lab: tag.
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
// internal/harness runs the scenarios and internal/steps holds each area's
// steps; test/deps starts the containers they need. Every step and every
// capture replay calls the API through pkg/sdk, never the generated client
// beneath it (TestUnitStepsUseTheSDK).
package acceptance
