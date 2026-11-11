// Package azuredevopsfake is an Azure DevOps organization for tests: Fake
// holds personal access tokens of its users with the scopes they hold,
// and projects with the roles users hold in them and their Git
// repositories, kept in memory by go-git, so refs, annotated tags, file
// contents and the files two commits differ in behave as Git's do.
// Handler serves the routes the azuredevops provider calls under the
// organization Organization; Start serves it over TLS and gives the
// organization URL to name and the certificate a client trusts, such as
// through vcs.ca_file. Fail and FailPath make it answer with a rate limit,
// a server error or no answer at all, so the provider's handling of each
// is tested without the real service, which the scheduled vcs-lab job
// checks.
//
// ref: decision 0012 § "Every provider is a peer"
//
// # Input and output
//
// It answers HTTP requests whose basic password is a token it issued; a
// request with no credential gets the sign-in page with 203, and a token
// it did not issue 401, as does one lacking a route's scope. A project's
// routes answer 404 for a project the token's user holds no role in. The
// refs route lists the branches its filter starts and the one tag the
// filter names in full. Seeding a repository panics on a go-git failure,
// which no test recovers from.
//
// # Dependencies
//
// It imports chi, go-git's object package and the domain's gitmem, which
// holds its repositories. The azuredevops provider's tests and the
// acceptance suite construct it.
package azuredevopsfake
