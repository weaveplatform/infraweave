// Package vendors holds the acceptance steps of features/vendors: the
// Version Catalog, refreshed from release indexes the steps serve, and a
// Terragrunt stack's vendor settings and effective versions read back.
//
// # Input and output
//
// It serves the release indexes recorded in internal/vendors' testdata
// over loopback HTTP, and sends requests to the scenario's servers through
// pkg/sdk.
//
// # Dependencies
//
// It builds on harness.World and test/deps. chi serves the indexes, and
// iac/version orders the versions they list.
package vendors
