// Package azuredevops is the Azure DevOps provider: Provider implements
// port.Source, port.Browse and port.Tester over Azure DevOps's REST API,
// acting with a personal access token on the organization each
// integration names, Azure DevOps Services' or a server's collection. It
// is a small client over net/http that sends only the requests in Routes,
// the allowlist a VCS agent compiles and refuses every other request
// against, and refuses any other before it leaves. A repository is named
// by its project and its name, and the repositories an integration
// reaches are those of its accessible projects, or of every project the
// token reads when it names none. A Test reads the organization's
// projects, then checks that the token lists the repositories of each
// project the integration reaches and reads the organization's service
// hook subscriptions, which a webhook is. Lend lends a run the
// integration's own personal access token, as Azure DevOps mints none
// narrower through its API.
//
// ref: decision 0012
// ref: https://learn.microsoft.com/rest/api/azure/devops/core/projects/list
// ref: https://learn.microsoft.com/rest/api/azure/devops/git/refs/list
// ref: https://learn.microsoft.com/rest/api/azure/devops/git/commits/get
// ref: https://learn.microsoft.com/rest/api/azure/devops/git/items/get
// ref: https://learn.microsoft.com/rest/api/azure/devops/git/diffs/get
// ref: https://learn.microsoft.com/rest/api/azure/devops/hooks/subscriptions/list
//
// # Input and output
//
// It sends HTTPS requests to the integration's organization URL, naming
// API version APIVersion, with HTTP basic authentication carrying the
// token alone, each call bounded by Timeout and never retried. Links and
// clone URLs are on the user-facing host, the organization URL when the
// integration names none; a host that is not an https URL is ErrHost. A
// project, repository, ref or path the organization lacks, or that the
// token cannot see, is port.ErrNotFound; a 429 is a *port.RateLimited
// carrying X-RateLimit-Reset, Retry-After's time, or Backoff when it names
// neither; a missing token, one Azure DevOps refuses, and its sign-in
// page, answered with 203 in place of JSON, are ErrCredential; a host that
// fails or does not answer is kernel.ErrUnavailable; any other answer is
// ErrStatus; a request outside Routes is ErrRoute; and a file over
// port.MaxFileSize is port.ErrTooLarge, read no further than the byte
// past the limit. A Test turns a host's failure into a failed check
// rather than an error.
//
// # Dependencies
//
// It imports internal/kernel and the domain's api and port packages. The
// domain's assembly constructs it.
package azuredevops
