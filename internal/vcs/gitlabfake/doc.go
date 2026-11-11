// Package gitlabfake is a GitLab API for tests: Fake holds users and
// their access tokens with scopes, and projects in groups and subgroups
// with their members' access levels and repositories kept in memory by
// go-git, so refs, annotated tags, file contents and the files two
// commits differ in behave as Git's do. Handler serves the routes the
// gitlab provider calls under /api/v4; Start serves it over TLS and gives
// the certificate a client trusts, such as through vcs.ca_file. A
// Maintainer's token with the api scope mints and revokes project access
// tokens, each acting as a bot user that is a member of its project
// alone; a bot user's token, as AddBotToken issues, may mint none, which
// GitLab answers with a 400 rather than a 403. Minted lists what each mint asked for and whether it has
// been revoked. Fail and FailPath make it answer with a rate limit, a
// server error or no answer at all, so the provider's handling of each is
// tested without the real service, which the scheduled vcs-lab job
// checks.
//
// ref: decision 0012 § "Every provider is a peer"
//
// # Input and output
//
// It answers HTTP requests bearing a token it issued, in PRIVATE-TOKEN or
// as a bearer token, and 401 for any other; a project's routes answer 404
// for a project the token's user is no member of, and a token holding
// neither api nor read_api reads a project's repository routes alone,
// answering 403 for any other. Seeding a repository panics on a go-git
// failure, which no test recovers from.
//
// # Dependencies
//
// It imports chi, go-git's object package and the domain's gitmem, which
// holds its repositories. The gitlab provider's tests and the acceptance
// suite construct it.
package gitlabfake
