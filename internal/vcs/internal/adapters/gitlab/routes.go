package gitlab

import (
	"errors"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
)

// ErrRoute is a request to a route the client does not call, which it
// refuses to send.
var ErrRoute = errors.New("gitlab: route not in the allowlist")

// Route is a request the client sends: its method and its path under the
// API's base, in http.ServeMux's pattern syntax, where {name...} matches
// the rest of the path.
type Route struct {
	Method string
	Path   string
}

// Routes are every route the client calls, and the allowlist a VCS agent
// compiles for GitLab, so the two cannot drift.
//
// ref: decision 0012 § "VCS agent"
var Routes = []Route{
	{http.MethodGet, "/projects/{project}/merge_requests/{request}/notes"},
	{http.MethodPost, "/projects/{project}/merge_requests/{request}/notes"},
	{http.MethodGet, "/projects/{project}/merge_requests/{request}/notes/{note}"},
	{http.MethodPut, "/projects/{project}/merge_requests/{request}/notes/{note}"},
	{http.MethodGet, "/user"},
	{http.MethodGet, "/users/{user}"},
	{http.MethodGet, "/personal_access_tokens/self"},
	{http.MethodGet, "/projects"},
	{http.MethodGet, "/projects/{project}"},
	{http.MethodGet, "/projects/{project}/hooks"},
	{http.MethodGet, "/projects/{project}/environments"},
	{http.MethodPost, "/projects/{project}/environments"},
	{http.MethodGet, "/projects/{project}/deployments"},
	{http.MethodPost, "/projects/{project}/deployments"},
	{http.MethodPut, "/projects/{project}/deployments/{deployment}"},
	{http.MethodGet, "/projects/{project}/repository/commits/{commit}"},
	{http.MethodGet, "/projects/{project}/repository/tags/{tag}"},
	{http.MethodGet, "/projects/{project}/repository/files/{file}/raw"},
	{http.MethodGet, "/projects/{project}/repository/compare"},
	{http.MethodGet, "/projects/{project}/repository/branches"},
	{http.MethodPost, "/projects/{project}/access_tokens"},
	{http.MethodDelete, "/projects/{project}/access_tokens/{token}"},
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
// path under the API's base, is one of Routes. A path that is not in
// its clean form, such as one with a dot segment, is refused rather than
// matched after cleaning.
func Allowed(method, escaped string) bool {
	u, err := url.Parse(escaped)
	if err != nil || u.Scheme != "" || u.Host != "" || u.RawQuery != "" ||
		u.Path == "" || path.Clean(u.Path) != u.Path {
		return false
	}
	_, pattern := routes().Handler(&http.Request{Method: method, URL: u})
	return pattern != ""
}

// routeTransport keeps the SDK's requests within the same route inventory
// that the VCS agent consumes, including escaped subgroup project paths.
type routeTransport struct {
	base *url.URL
	next http.RoundTripper
}

func (t routeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rel, ok := strings.CutPrefix(r.URL.EscapedPath(), strings.TrimSuffix(t.base.EscapedPath(), "/"))
	if !ok || r.URL.Scheme != t.base.Scheme || r.URL.Host != t.base.Host || !Allowed(r.Method, rel) {
		return nil, ErrRoute
	}
	return t.next.RoundTrip(r) //nolint:wrapcheck // the provider classifies the transport result
}
