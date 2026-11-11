// Package bitbucketdcfake is a Bitbucket Data Center REST API for tests:
// Fake holds users and their HTTP access tokens, and repositories in
// projects with the roles users hold on them and their contents kept in
// memory by go-git, so refs, annotated tags, file contents and the files
// two commits differ in behave as Git's do. Handler serves the routes the
// bitbucketdc provider calls under API, and under Tokens the repository
// access tokens a repository's admin mints and revokes, which Minted
// reads back as they were asked for; a minted token reads its repository
// alone and writes nothing. Start serves it over TLS and gives the URL an
// integration names as its API host and the certificate a client trusts,
// such as through vcs.ca_file. Fail and FailPath make it answer with a
// rate limit, a server error or no answer at all, so the provider's
// handling of each is tested without a real server, which the scheduled
// vcs-cloud job's container checks.
//
// ref: decision 0012 § "Every provider is a peer"
//
// # Input and output
//
// It answers HTTP requests whose bearer token is one it issued, and 401
// for any other; a repository's routes answer 404 for a repository the
// user holds no role on, and a token's mint or revoke answers 403 to a
// user who does not administer it. Lists page by start and limit in
// Bitbucket's envelope. Seeding a repository panics on a go-git failure,
// which no test recovers from.
//
// # Dependencies
//
// It imports chi, go-git's object package and the domain's gitmem, which
// holds its repositories. The bitbucketdc provider's tests and the
// acceptance suite construct it.
package bitbucketdcfake
