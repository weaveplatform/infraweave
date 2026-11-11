// Package githubfake is a GitHub API for tests: Fake holds GitHub Apps
// with their RSA keys, their installations on owners and the tokens those
// mint, and repositories kept in memory by go-git, so refs, annotated
// tags, file contents and the files two commits differ in behave as
// Git's do. Handler serves the routes the github provider calls under
// /api/v3, as GitHub Enterprise Server does; Start serves it over TLS and
// gives the certificate a client trusts, such as through vcs.ca_file.
// Fail and FailPath make it answer with a primary or secondary rate
// limit, a server error or no answer at all, so the provider's handling
// of each is tested without the real service, which the scheduled
// vcs-cloud job checks.
//
// ref: decision 0012 § "Every provider is a peer"
//
// # Input and output
//
// It answers HTTP requests: /app routes need a JWT an App signed with
// RS256, its issuer the App's ID; repository routes need a token one of
// the App's installations minted, and answer 404 for a repository the
// installation does not reach. Seeding a repository panics on a go-git
// failure, which no test recovers from.
//
// # Dependencies
//
// It imports chi, golang-jwt and the domain's gitmem, which holds its
// repositories. The github provider's tests and the acceptance suite
// construct it.
package githubfake
