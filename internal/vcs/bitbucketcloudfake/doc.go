// Package bitbucketcloudfake is a Bitbucket Cloud API for tests: Fake
// holds Atlassian accounts and their API tokens, and repositories in
// workspaces with the roles accounts hold on them and their contents kept
// in memory by go-git, so refs, annotated tags, file contents and the
// files two commits differ in behave as Git's do. Handler serves the
// routes the bitbucketcloud provider calls under /2.0; Start serves it
// over TLS and gives the API host to name and the certificate a client
// trusts, such as through vcs.ca_file. Fail and FailPath make it answer
// with a rate limit, a server error or no answer at all, so the
// provider's handling of each is tested without the real service, which
// the scheduled vcs-lab job checks.
//
// ref: decision 0012 § "Every provider is a peer"
//
// # Input and output
//
// It answers HTTP requests whose basic credentials are an account's
// e-mail and an API token it issued that account, and 401 for any other;
// a repository's routes answer 404 for a repository the account holds no
// role on. Seeding a repository panics on a go-git failure, which no test
// recovers from.
//
// # Dependencies
//
// It imports chi, go-git's object package and the domain's gitmem, which
// holds its repositories. The bitbucketcloud provider's tests and the
// acceptance suite construct it.
package bitbucketcloudfake
