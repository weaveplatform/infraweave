// Package tools downloads, verifies and caches the Terraform and OpenTofu
// binaries the launcher mounts into a run's container. A Fetcher answers
// the path of a release's binary for the container's platform: from the
// cache at <dir>/binaries/<tool>/<version>/<os>_<arch> when it is there,
// or else fetched from the vendor, or from a mirror of the same layout.
// A fetched release is used only once its SHA256SUMS's detached
// signature verifies against the vendor's public key, which the binary
// carries, and its archive's SHA-256 is the one SHA256SUMS lists; then
// only the tool's binary is extracted, and renamed into place, so
// launchers that share the cache never see a partial one.
//
// ref: decision 0008 § "Runner images"
// ref: decision 0013
//
// # Input and output
//
// It reads releases over HTTP through the caller's client, at most 256
// MiB an archive, and writes binaries under the cache directory. Each
// error names the release and the step that refused it.
//
// # Dependencies
//
// It imports internal/agent/wire for the tools' names and
// ProtonMail/go-crypto's openpgp to check signatures.
// internal/agent/launcher calls it while it prepares a run.
package tools
