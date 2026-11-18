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
// Webhooks verifies empty-username Basic credentials against every allowed
// password and returns a body HMAC under the matching password. It never
// retains the Authorization header. Routing comes from the envelope's eventType
// and id. An authenticated invalid routing envelope is consumed with a private
// marker so processing fails durably; a valid unsupported event type is filtered.
// Push refs, pull requests and nested pull-request comments become payload-only
// facts. Preview merge success alone proves neither blocking-policy approval
// nor ancestry, so Mergeable and Undiverged remain false without such evidence.
//
// ref: decision 0012
// ref: https://learn.microsoft.com/rest/api/azure/devops/core/projects/list
// ref: https://learn.microsoft.com/rest/api/azure/devops/git/refs/list
// ref: https://learn.microsoft.com/rest/api/azure/devops/git/annotated-tags/get
// ref: https://learn.microsoft.com/rest/api/azure/devops/git/commits/get
// ref: https://learn.microsoft.com/rest/api/azure/devops/git/items/get
// ref: https://learn.microsoft.com/rest/api/azure/devops/git/diffs/get
// ref: https://learn.microsoft.com/rest/api/azure/devops/hooks/subscriptions/list
// ref: https://learn.microsoft.com/azure/devops/service-hooks/events
//
// # Input and output
//
// Tag resolution peels only delivered immutable annotated-tag object IDs,
// with a bounded native chain; it never substitutes a current tag ref.
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
