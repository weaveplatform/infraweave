// Package github is the GitHub provider: Provider implements port.Source,
// port.Browse, port.Tester, port.Apps and port.Minter over GitHub's REST
// API for github.com and GitHub Enterprise Server, acting as the
// integration's GitHub App. A repository's call finds the App's
// installation on it with the App's JWT, then acts as that installation,
// reusing the installation's token across calls until it expires. Mint
// mints a run a separate installation token, never cached, that reads one
// repository's contents alone and expires within the hour; Revoke has
// nothing to revoke. A Test reads the App and checks the permissions and
// webhook events infraweave needs; the manifest flow writes the form that
// creates an App asking for exactly those, and converts the code GitHub
// redirects back with into the App.
//
// ref: decision 0012
//
// # Input and output
//
// It sends HTTPS requests to the integration's API host, the API of
// github.com when it names none, each call bounded by Timeout. A
// repository, ref or path the host lacks, or that the App's installation
// does not reach, is port.ErrNotFound; a rate limit is a
// *port.RateLimited carrying its reset time, never retried here; an App
// ID or key that does not parse, or that the host refuses, is
// ErrCredential; a host that fails or does not answer is
// kernel.ErrUnavailable; and a file over port.MaxFileSize is
// port.ErrTooLarge. A Test or an App report turns a host's failure into a
// failed check or a state rather than an error.
//
// # Dependencies
//
// It imports go-github, ghinstallation for App and installation
// authentication, internal/kernel, and the domain's api and port
// packages. The domain's assembly constructs it.
package github
