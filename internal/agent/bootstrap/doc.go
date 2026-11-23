// Package bootstrap is the preparation stream a Kubernetes run Pod
// receives before its runner starts: the run's prepared workspace and
// the verified tool executables that replace its image's own, which the
// controller sends over the Pod's attach stream to its bootstrap init
// container, never through a shared or host volume.
//
// A stream is a strict version-1 JSON manifest of at most 64 KiB naming
// the run, its state version and the worker, the workspace payload's
// codec, length and SHA-256, and at most two tools by fixed name,
// release, length and SHA-256; then each payload in that order. Every
// part is framed by its length as a big-endian uint64. Pack archives a
// workspace directory as tar compressed with zstd, at most 2 GiB
// compressed and 8 GiB decoded, refusing a link that leaves it and any
// device, pipe or socket; Send writes a manifest and its payloads,
// failing a payload shorter or longer than its manifest says.
//
// Receive is the bootstrap container's side: it refuses unknown fields,
// another version, another assignment than its Pod's, a frame of another
// length, a truncated or trailing stream and a hash mismatch, extracts
// the workspace with the workspace package's safe rules, writes each
// tool read-only once it is an ELF executable of the Pod's architecture,
// and copies the receiving executable to where the runner starts it.
//
// ref: docs/implementation/worker-pool-contracts.md § "Portable preparation, handoff and recovery"
//
// # Input and output
//
// Pack reads a directory and writes an io.Writer; Send writes an
// io.Writer; Receive reads an io.Reader and writes the Pod's workspace,
// tools and binary paths. Nothing is logged.
//
// # Dependencies
//
// It imports klauspost/compress's zstd and internal/agent/workspace's
// safe extraction. internal/agent/executor/kubernetes sends streams and
// internal/agent/cli's runner bootstrap receives them.
package bootstrap
