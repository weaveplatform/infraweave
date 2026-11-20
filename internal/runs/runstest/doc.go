// Package runstest exposes bounded run seams to app-owned composition tests.
// Released binaries must not import this package.
//
// ref: decision 0009
//
// # Input and output
//
// Input exercises the real snapshot and typed builder; Ready exercises the
// existing queued-to-ready transition with receipts from a checked approval.
// App tests construct real domain graphs and supply only their public ports.
//
// # Effects
//
// All reads and writes join the caller's transaction. These helpers expose no
// served endpoint, production workflow gate or domain constructor.
//
// # Dependencies
//
// The runs private lifecycle/store, kernel/platform and declared public owner
// ports. Only test files in other packages may import this support package.
package runstest
