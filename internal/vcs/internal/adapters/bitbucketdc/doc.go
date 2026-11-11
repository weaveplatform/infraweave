// Package bitbucketdc is the Bitbucket Data Center provider: Provider
// implements port.Source, port.Browse and port.Tester over Bitbucket's
// REST API 1.0 on each integration's own host, acting with its HTTP
// access token. It is a small client over net/http that sends only the
// requests in Routes, the allowlist a VCS agent compiles, and refuses any
// other before it leaves. A repository is named by its project's key and
// its slug, and the repositories a token reaches are those its user
// reads. A Test reads the user's recent repositories, which answer only
// an authenticated user, and checks that it administers every repository
// it reads, the permission that mints a run's repository access token and
// installs a repository's webhook.
//
// ref: decision 0012
// ref: https://developer.atlassian.com/server/bitbucket/rest/
//
// # Input and output
//
// It sends HTTPS requests to the integration's API host under API, with
// the token as a bearer token, each call bounded by Timeout and never
// retried; commits and clones link to its user-facing host, the API host
// when it names none. An API or user-facing host that is not an https URL
// is ErrHost. A repository, ref or path the host lacks, or that the
// user cannot read, is port.ErrNotFound; a 429 is a *port.RateLimited
// carrying Retry-After's time, or Backoff when it names none; a missing
// token, or one Bitbucket refuses, is ErrCredential; a host that fails or
// does not answer is kernel.ErrUnavailable; any other answer is
// ErrStatus; a request outside Routes is ErrRoute; and a file over
// port.MaxFileSize is port.ErrTooLarge, read no further than the byte
// past the limit. A Test turns a host's failure into a failed check
// rather than an error.
//
// # Dependencies
//
// It imports internal/kernel and the domain's api and port packages. The
// domain's assembly constructs it.
package bitbucketdc
