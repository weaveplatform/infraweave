package github

import (
	"errors"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
)

// ErrRoute refuses a method, path, host or purpose outside the client inventory.
var ErrRoute = errors.New("github: route not in the allowlist")

// RoutePurpose keeps source reads, deployment writes and App setup separate.
// ReadOnly includes installation-token creation, needed to authenticate reads.
type RoutePurpose string

const (
	ReadOnly        RoutePurpose = "read"
	DeploymentWrite RoutePurpose = "deployment"
	CheckWrite      RoutePurpose = "check"
	CommentWrite    RoutePurpose = "comment"
	AppSetup        RoutePurpose = "app_setup"
	allRoutes       RoutePurpose = "all"
)

// Route is one GitHub API request and the capability that may send it.
type Route struct {
	Method  string
	Path    string
	Purpose RoutePurpose
}

// Routes is the client's inventory and the future VCS agent's allowlist.
// App authentication shares it, so automatic token refresh cannot bypass it.
//
// ref: decision 0012 § "VCS agent pools"
var Routes = []Route{
	{http.MethodGet, "/repos/{owner}/{repo}/pulls/{request}", ReadOnly},
	{http.MethodGet, "/repos/{owner}/{repo}/commits/{ref}/check-runs", ReadOnly},
	{http.MethodPost, "/repos/{owner}/{repo}/check-runs", CheckWrite},
	{http.MethodPatch, "/repos/{owner}/{repo}/check-runs/{check}", CheckWrite},
	{http.MethodGet, "/repos/{owner}/{repo}/issues/{issue}/comments", CommentWrite},
	{http.MethodGet, "/repos/{owner}/{repo}/issues/comments/{comment}", CommentWrite},
	{http.MethodPost, "/repos/{owner}/{repo}/issues/{issue}/comments", CommentWrite},
	{http.MethodPatch, "/repos/{owner}/{repo}/issues/comments/{comment}", CommentWrite},
	{http.MethodPost, "/graphql", CommentWrite},
	{http.MethodPost, "/api/graphql", CommentWrite},
	{http.MethodGet, "/app", ReadOnly},
	{http.MethodGet, "/app/installations", ReadOnly},
	{http.MethodPost, "/app/installations/{installation}/access_tokens", ReadOnly},
	{http.MethodPost, "/app-manifests/{code}/conversions", AppSetup},
	{http.MethodGet, "/installation/repositories", ReadOnly},
	{http.MethodGet, "/repos/{owner}/{repo}", ReadOnly},
	{http.MethodGet, "/repos/{owner}/{repo}/installation", ReadOnly},
	{http.MethodGet, "/repos/{owner}/{repo}/collaborators/{username}/permission", ReadOnly},
	{http.MethodGet, "/repos/{owner}/{repo}/commits/{ref...}", ReadOnly},
	{http.MethodGet, "/repos/{owner}/{repo}/git/ref/{ref...}", ReadOnly},
	{http.MethodGet, "/repos/{owner}/{repo}/git/trees/{tree...}", ReadOnly},
	{http.MethodGet, "/repos/{owner}/{repo}/contents/{file...}", ReadOnly},
	{http.MethodGet, "/repos/{owner}/{repo}/compare/{comparison...}", ReadOnly},
	{http.MethodGet, "/repos/{owner}/{repo}/branches", ReadOnly},
	{http.MethodGet, "/repos/{owner}/{repo}/deployments", ReadOnly},
	{http.MethodGet, "/repos/{owner}/{repo}/deployments/{deployment}/statuses", ReadOnly},
	{http.MethodPost, "/repos/{owner}/{repo}/deployments", DeploymentWrite},
	{http.MethodPost, "/repos/{owner}/{repo}/deployments/{deployment}/statuses", DeploymentWrite},
}

type routeInventory struct {
	mux      *http.ServeMux
	comments *http.ServeMux
	routes   map[string]Route
}

var inventory = sync.OnceValue(func() routeInventory {
	i := routeInventory{mux: http.NewServeMux(), comments: http.NewServeMux(), routes: map[string]Route{}}
	for _, route := range Routes {
		pattern := route.Method + " " + route.Path
		i.routes[pattern] = route
		// The native list and individual comment paths overlap for literal
		// wildcard values (issues/comments/comments), which ServeMux rejects
		// in one tree. Separate trees retain both exact native route shapes.
		mux := i.mux
		if strings.Contains(route.Path, "/issues/comments/") {
			mux = i.comments
		}
		mux.HandleFunc(pattern, func(http.ResponseWriter, *http.Request) {})
	}
	return i
})

// Allowed names every declared method/path, without narrowing its capability.
func Allowed(method, escaped string) bool { return AllowedFor(allRoutes, method, escaped) }

// AllowedFor permits reads for each client, and only that purpose's writes.
// Methods are exact: ServeMux's implicit HEAD-for-GET is not an inventory entry.
func AllowedFor(purpose RoutePurpose, method, escaped string) bool {
	switch purpose {
	case ReadOnly, DeploymentWrite, CheckWrite, CommentWrite, AppSetup, allRoutes:
	default:
		return false
	}
	u, err := url.Parse(escaped)
	if err != nil || u.Scheme != "" || u.Host != "" || u.RawQuery != "" || u.Fragment != "" ||
		u.Path == "" || path.Clean(u.Path) != u.Path {
		return false
	}
	i := inventory()
	request := &http.Request{Method: method, URL: u}
	_, pattern := i.mux.Handler(request)
	if pattern == "" {
		_, pattern = i.comments.Handler(request)
	}
	route, exists := i.routes[pattern]
	return exists && route.Method == method &&
		(purpose == allRoutes || route.Purpose == ReadOnly || route.Purpose == purpose)
}

type routeTransport struct {
	base    *url.URL
	next    http.RoundTripper
	purpose RoutePurpose
}

func (t routeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if (t.purpose == CommentWrite || t.purpose == allRoutes) && req.Method == http.MethodPost && req.URL.Scheme == t.base.Scheme && strings.EqualFold(req.URL.Host, t.base.Host) &&
		req.URL.EscapedPath() == commentGraphQLPath(t.base.EscapedPath()) &&
		req.URL.RawQuery == "" && req.URL.Fragment == "" && req.URL.User == nil {
		return t.next.RoundTrip(req) //nolint:wrapcheck // the SDK supplies request context
	}
	rel, ok := strings.CutPrefix(req.URL.EscapedPath(), strings.TrimSuffix(t.base.EscapedPath(), "/"))
	if !ok || rel == "/graphql" || rel == "/api/graphql" || req.URL.Scheme != t.base.Scheme || !strings.EqualFold(req.URL.Host, t.base.Host) ||
		!AllowedFor(t.purpose, req.Method, rel) {
		return nil, ErrRoute
	}
	return t.next.RoundTrip(req) //nolint:wrapcheck // the SDK supplies request context
}

func guarded(h host, next http.RoundTripper, purpose RoutePurpose) http.RoundTripper {
	base, _ := url.Parse(h.api) //nolint:errcheck // hostOf validates h.api
	return routeTransport{base: base, next: next, purpose: purpose}
}
