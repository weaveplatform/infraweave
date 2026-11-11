package azuredevops

import (
	"errors"
	"net/http"
	"net/url"
	"path"
	"sync"
)

// ErrRoute is a request to a route the client does not call, which it
// refuses to send.
var ErrRoute = errors.New("azuredevops: route not in the allowlist")

// Route is a request the client sends: its method and its path under the
// organization's URL, in http.ServeMux's pattern syntax.
type Route struct {
	Method string
	Path   string
}

// Routes are every route the client calls, and the allowlist a VCS agent
// compiles for Azure DevOps, which refuses every request outside it, so
// the two cannot drift.
//
// ref: decision 0012 § "VCS agent"
var Routes = []Route{
	{http.MethodGet, "/_apis/projects"},
	{http.MethodGet, "/_apis/hooks/subscriptions"},
	{http.MethodGet, "/{project}/_apis/git/repositories"},
	{http.MethodGet, "/{project}/_apis/git/repositories/{repository}/refs"},
	{http.MethodGet, "/{project}/_apis/git/repositories/{repository}/commits/{commit}"},
	{http.MethodGet, "/{project}/_apis/git/repositories/{repository}/items"},
	{http.MethodGet, "/{project}/_apis/git/repositories/{repository}/diffs/commits"},
}

// routes matches a request against Routes.
var routes = sync.OnceValue(func() *http.ServeMux {
	m := http.NewServeMux()
	for _, r := range Routes {
		m.HandleFunc(r.Method+" "+r.Path, func(http.ResponseWriter, *http.Request) {})
	}
	return m
})

// Allowed reports whether a request of method to escaped, an escaped
// path under the organization's URL, is one of Routes. A path that is not
// in its clean form, such as one with a dot segment, is refused rather
// than matched after cleaning.
func Allowed(method, escaped string) bool {
	u, err := url.Parse(escaped)
	if err != nil || u.Scheme != "" || u.Host != "" || u.RawQuery != "" ||
		u.Path == "" || path.Clean(u.Path) != u.Path {
		return false
	}
	_, pattern := routes().Handler(&http.Request{Method: method, URL: u})
	return pattern != ""
}
