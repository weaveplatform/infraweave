// Package planfixtures records Terraform's and OpenTofu's plans as
// internal/iac/plan's test fixtures (make iac-fixtures). For each pinned
// release and each Scenario it applies one configuration and plans
// another in a fresh workspace, running the release's Linux binary in a
// container with no network, and writes `show -json` of the plan,
// indented, with a README naming the releases.
//
// ref: decision 0016
//
// # Input and output
//
// Record takes the test dependencies that fetch and check the pinned
// binaries, the tools and scenarios, and the directory it writes into.
//
// # Dependencies
//
// It imports the standard library, the Docker client testcontainers
// wraps, and test/deps. It reaches Docker, and the releases' download
// sites when test/deps' cache lacks a binary.
package planfixtures
