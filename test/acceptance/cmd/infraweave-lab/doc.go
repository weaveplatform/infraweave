// Package main provides the verification-only infraweave-lab command.
//
// # Input and output
//
// Subcommands write batch TLS fixtures, validate the dedicated App's setup attestation, write a new
// owner-only setup file, restore owned hook journals, and supervise a pinned
// short-lived tunnel. Operational errors contain no credential values.
//
// # Dependencies
//
// The command uses cobra, the verification-private vcslab package, and pinned
// test dependencies. It owns signal cancellation; resource cleanup remains
// bounded and an unsuccessful restore or established tunnel is a failure.
package main
