// Package http is the vcs domain's HTTP adapter: Server, the domain's
// partial of the API's strict server. It lists, reads and searches the
// VCS integrations of the caller's account in the spaces it may read,
// lists those of a provider a stack in a space may bind, and tests one
// through its provider's Tester, answering 501 for a provider with none.
// An integration's provider details reach only a caller with SPACE_ADMIN
// in its space: GitHub's, GitHub Enterprise's, GitLab's, both Bitbucket
// providers' and Azure DevOps's from the service, and other providers'
// from their Describe. It serves the GitHub operations: the operator's
// App's check options, GitHub Enterprise integrations' create, read,
// update, delete and test, the last answering whether every check passed,
// and the App manifest flow. It serves GitLab integrations' create, with
// a generated webhook secret, read, update, keeping each host and the
// token an update leaves out, delete and test; Bitbucket Cloud
// integrations' the same, over an email and API token, each always
// checking out with git; and Bitbucket Data Center integrations' the
// same, over an HTTP access token and the username git checks out as; and
// Azure DevOps integrations' the same, over a personal access token and
// the projects it reaches, their webhook secret answered as the webhook
// password. Other providers' create and change operations answer 501
// until their providers serve them. A provider not configured answers 503
// and a provider's rate limit 502. It lists the repositories and branches
// an integration reaches, or a raw Git repository's branches by its URL,
// each sorted and paged by its last name, a repository the provider lacks
// answering 404 and a credential it refuses 502; and searches the stacks
// that use an integration.
//
// # Input and output
//
// None: the router passes it each request.
//
// # Dependencies
//
// It imports the access api package, internal/kernel and its search
// package, the generated API, platform/httpkit's apisearch, page, problem
// and strict packages and the domain's integration and port packages. The vcs
// domain's assembly constructs it.
package http
