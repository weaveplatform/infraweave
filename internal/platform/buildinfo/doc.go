// Package buildinfo carries the version metadata of the binary: Version,
// Commit and BuildDate.
//
// The values are injected with -ldflags -X (see LDFLAGS in the Makefile).
// Release builds set all three; a plain `go build` reports "devel" and empty
// values.
//
// # Input and output
//
// None at run time: the values are fixed by the linker.
//
// # Dependencies
//
// It imports nothing. internal/app reads it for infraweave version, the
// tools/ commands for their own version flags, and the agent process modes
// may too (repo layout, agent-isolation).
package buildinfo
