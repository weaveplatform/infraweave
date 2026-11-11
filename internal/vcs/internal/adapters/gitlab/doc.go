// Package gitlab is the GitLab provider: Provider implements port.Source,
// port.Browse and port.Tester over GitLab's REST API for GitLab.com and
// self-managed GitLab, acting with the integration's access token. A
// repository is a project, named by its namespace's full path, a
// subgroup's included, and its own path. A Test reads the token's user and
// scopes and checks the api scope, and the Maintainer role on every
// project the token reaches, which mints a run's project access token.
//
// ref: decision 0012
//
// # Input and output
//
// It sends HTTPS requests to the integration's API host, the API of
// GitLab.com when it names none, each call bounded by Timeout and never
// retried. A project, ref or path the host lacks, or that the token's
// user is no member of, is port.ErrNotFound; a rate limit is a
// *port.RateLimited carrying its reset time; a missing or refused token
// is ErrCredential; a host that fails or does not answer is
// kernel.ErrUnavailable; and a file over port.MaxFileSize is
// port.ErrTooLarge, read no further than the byte past the limit. A Test
// turns a host's failure into a failed check rather than an error.
//
// # Dependencies
//
// It imports GitLab's client-go, x/time/rate to switch off the client's
// own pacing, internal/kernel, and the domain's api and port packages.
// The domain's assembly constructs it.
package gitlab
