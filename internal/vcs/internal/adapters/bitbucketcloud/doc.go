// Package bitbucketcloud is the Bitbucket Cloud provider: Provider
// implements port.Source, port.Browse and port.Tester over Bitbucket's
// REST API 2.0, acting with an Atlassian account's e-mail and API token.
// It is a small client over net/http that sends only the requests in
// Routes, the allowlist a VCS agent compiles, and refuses any other
// before it leaves. A repository is named by its workspace and its slug,
// and the repositories an account reaches are those of the workspaces it
// belongs to. A Test reads the account and checks that it is admin of
// every repository it is a member of, the permission that installs a
// repository's webhook.
//
// ref: decision 0012
//
// # Input and output
//
// It sends HTTPS requests to the API host, Bitbucket Cloud's unless
// Options names another, with HTTP basic authentication, each call
// bounded by Timeout and never retried. A repository, ref or path the
// host lacks, or that the account cannot see, is port.ErrNotFound; a 429
// is a *port.RateLimited carrying X-RateLimit-Reset, Retry-After's time,
// or Backoff when it names neither; a missing e-mail or token, or one
// Bitbucket refuses, is ErrCredential; a host that fails or does not
// answer is kernel.ErrUnavailable; any other answer is ErrStatus; a
// request outside Routes is ErrRoute;
// and a file over port.MaxFileSize is port.ErrTooLarge, read no further
// than the byte past the limit. A Test turns a host's failure into a
// failed check rather than an error.
//
// # Dependencies
//
// It imports internal/kernel and the domain's api and port packages. The
// domain's assembly constructs it.
package bitbucketcloud
