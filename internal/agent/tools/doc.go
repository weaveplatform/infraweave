// Package tools downloads, verifies and caches the Terraform, OpenTofu
// and Terragrunt binaries the launcher mounts into a run's container. A
// Fetcher answers the path of a release's binary for the container's
// platform: from the cache at <dir>/binaries/<tool>/<version>/<os>_<arch>
// when it is there, or else fetched from the vendor, or from a mirror
// that serves each release's files under <tool>/<version>/ with the
// vendor's names. A fetched release is used only once its SHA256SUMS's
// detached signature verifies against the vendor's public key, which the
// binary carries: HashiCorp's, OpenTofu's or Gruntwork's. Then the
// build's SHA-256 must be the one SHA256SUMS lists: a Terraform or
// OpenTofu zip archive, from which only the tool's binary is extracted,
// or Terragrunt's bare binary, terragrunt_<os>_<arch>, listed in a
// SHA256SUMS of that name alone. The binary is renamed into place, so
// launchers that share the cache never see a partial one.
//
// ref: decision 0008 § "Runner images"
// ref: decision 0013
//
// # Input and output
//
// It reads releases over HTTP through the caller's client, at most 256
// MiB an archive and 512 MiB a bare binary, and writes binaries under the
// cache directory. Each error names the release and the step that refused
// it.
//
// # Dependencies
//
// It imports internal/agent/wire for the tools' names and
// ProtonMail/go-crypto's openpgp to check signatures.
// internal/agent/launcher calls it while it prepares a run.
package tools
