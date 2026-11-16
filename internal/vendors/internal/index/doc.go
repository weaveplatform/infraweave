// Package index reads the vendors' release indexes. HashiCorp reads
// HashiCorp's releases API, page by page, each after the creation time of
// the last before it; OpenTofu reads the one document OpenTofu's
// installer reads; GitHub reads a repository's releases from GitHub's
// REST API, as Terragrunt publishes them, following each page's Link to
// the next on the same host. Each Reader lists stable releases only,
// newest first and each once: none the index marks a prerelease, a draft
// or withdrawn, and none whose version is a prerelease or does not parse,
// as Terragrunt's alpha, beta and release-candidate tags do not. An index
// that cannot be read, or that lists no stable release, is ErrIndex,
// naming the index and why. Resolve picks the newest of a list of
// releases that constraints allow.
//
// ref: decision 0008 § "The run specification and its secrets"
// ref: decision 0016
//
// # Input and output
//
// It sends GET requests to the indexes it is given and reads their JSON
// bodies, up to 64 MiB each.
//
// # Dependencies
//
// It imports the standard library and iac/version. The vendors domain's
// core package reads its releases; its assembly constructs the
// readers.
package index
