package azuredevops_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/azuredevopsfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/azuredevops"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/sourcetest"
)

// user is the lab token's user.
const user = "infraweave"

// recorder sends each request on to next, failing t on any request whose
// route is not in the provider's allowlist: one that reaches here is one
// the provider's own check let through.
type recorder struct {
	t    *testing.T
	next http.RoundTripper
}

func (r recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	rel, ok := strings.CutPrefix(req.URL.EscapedPath(), "/"+azuredevopsfake.Organization)
	if !ok || !azuredevops.Allowed(req.Method, rel) {
		r.t.Errorf("sent %s %s, which the allowlist refuses", req.Method, req.URL)
	}
	return r.next.RoundTrip(req) //nolint:wrapcheck // the client wraps it
}

// trusting is a transport trusting pool alone.
func trusting(pool *x509.CertPool) http.RoundTripper {
	tr := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // the standard library's
	tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	return tr
}

// lab is a fake serving the repository infra of the project Platform,
// which the lab's user administers, the provider trusting it and
// recording its requests, and the integration of the user's token.
type lab struct {
	fake  *azuredevopsfake.Fake
	srv   *azuredevopsfake.Server
	az    *azuredevops.Provider
	token string
	in    port.Integration
	repo  sourcetest.Repository
}

func newLab(t *testing.T) *lab {
	t.Helper()
	f := azuredevopsfake.New()
	srv := f.Start()
	t.Cleanup(srv.Close)
	l := &lab{fake: f, srv: srv, az: azuredevops.NewOver(recorder{t, trusting(srv.Pool())})}
	l.token = f.AddToken(user)
	l.in = integrationOf(srv.OrganizationURL, l.token)
	f.Grant("Platform", user, azuredevopsfake.Administrator)
	f.Repo("Hidden", "infra").Commit("main", "first", map[string]string{"a.tf": "a"})
	l.repo = seed(f.Repo("Platform", "infra"))
	l.repo.Repo.Integration = &l.in
	l.repo.Missing = port.Repository{Namespace: "Hidden", Name: "infra", Integration: &l.in}
	return l
}

func integrationOf(orgURL, token string, projects ...string) port.Integration {
	return port.Integration{
		Provider:   "AZURE_DEVOPS",
		Settings:   port.Settings{OrganizationURL: orgURL, AccessibleProjects: projects},
		Credential: []byte(token),
	}
}

// seed commits three commits on main, the first tagged v1, and returns
// what sourcetest expects of them.
func seed(r *azuredevopsfake.Repo) sourcetest.Repository {
	tagged := r.Commit("main", "first", map[string]string{"README.md": "infra"})
	r.Tag("v1", tagged, "the first release")
	base := r.Commit("main", "second", map[string]string{"main.tf": "a", "old.tf": "x"})
	head := r.Commit("main", "third", map[string]string{
		"main.tf": "b", "modules/new.tf": "n", "old.tf": "",
	})
	return sourcetest.Repository{
		Repo:   port.Repository{Namespace: "Platform", Name: "infra"},
		Branch: "main", Tag: "v1", Tagged: tagged, Base: base, Head: head,
		File: "main.tf", Content: []byte("b"),
		Changed: []string{"main.tf", "modules/new.tf", "old.tf"},
	}
}

// TestUnitSource checks the provider against the Source contract, a
// commit's link and author, the source URL, a branch with a slash, a
// branch another's name starts, a lightweight tag, a nested file, and a
// project and repository whose names need escaping.
func TestUnitSource(t *testing.T) {
	l := newLab(t)
	ctx := t.Context()
	sourcetest.Run(t, l.az, l.repo)

	got, err := l.az.ResolveRef(ctx, l.repo.Repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	if got.AuthorLogin != nil || got.AuthorName != azuredevopsfake.AuthorName ||
		got.URL != l.srv.OrganizationURL+"/Platform/_git/infra/commit/"+got.Hash {
		t.Errorf("ResolveRef(main) = %+v", got)
	}
	if u := l.az.SourceURL(
		l.repo.Repo,
		got.Hash,
	); u != l.srv.OrganizationURL+"/Platform/_git/infra" {
		t.Errorf("SourceURL = %s", u)
	}
	r := l.fake.Repo("Platform", "infra")
	r.Branch("release/1", l.repo.Base)
	r.Branch("main-next", l.repo.Base)
	if got, err := l.az.ResolveRef(ctx, l.repo.Repo, "release/1"); err != nil ||
		got.Hash != l.repo.Base || got.Tag != "" {
		t.Errorf("a branch with a slash = %+v, %v", got, err)
	}
	if got, err := l.az.ResolveRef(
		ctx,
		l.repo.Repo,
		"main",
	); err != nil ||
		got.Hash != l.repo.Head {
		t.Errorf("main beside main-next = %+v, %v", got, err)
	}
	if b, err := l.az.ReadFile(ctx, l.repo.Repo, "release/1", "old.tf"); err != nil ||
		string(b) != "x" {
		t.Errorf("a file on a branch with a slash = %q, %v", b, err)
	}
	if b, err := l.az.ReadFile(ctx, l.repo.Repo, "main", "/modules/new.tf"); err != nil ||
		string(b) != "n" {
		t.Errorf("a nested file = %q, %v", b, err)
	}
	if _, err := l.az.ResolveRef(ctx, l.repo.Repo, l.repo.Head[:12]); !errors.Is(
		err, port.ErrNotFound) {
		t.Errorf("an abbreviated SHA = %v, want port.ErrNotFound", err)
	}
	if _, err := l.az.ReadFile(ctx, l.repo.Repo, "nope", "main.tf"); !errors.Is(
		err, port.ErrNotFound) {
		t.Errorf("a file at an unknown ref = %v", err)
	}
	if _, err := l.az.AffectedFiles(ctx, l.repo.Repo, l.repo.Base, "nope"); !errors.Is(
		err, port.ErrNotFound) {
		t.Errorf("files to an unknown head = %v", err)
	}

	l.fake.Grant("My Project", user, azuredevopsfake.Reader)
	spaced := l.fake.Repo("My Project", "the infra")
	head := spaced.Commit("main", "first", map[string]string{"a.tf": "a"})
	repo := port.Repository{Namespace: "My Project", Name: "the infra", Integration: &l.in}
	if got, err := l.az.ResolveRef(ctx, repo, "main"); err != nil || got.Hash != head ||
		got.URL != l.srv.OrganizationURL+"/My%20Project/_git/the%20infra/commit/"+head {
		t.Errorf("an escaped project = %+v, %v", got, err)
	}

	w := l.fake.Repo("Platform", "light")
	tip := w.Commit("main", "first", map[string]string{"a.tf": "a"})
	light := port.Repository{Namespace: "Platform", Name: "light", Integration: &l.in}
	if got, err := l.az.ResolveRef(ctx, light, "main"); err != nil || got.Hash != tip {
		t.Errorf("a second repository = %+v, %v", got, err)
	}
}

// TestUnitHosts checks how an integration's settings name its
// organization and user-facing host, and that a host that is not https
// answers ErrHost.
func TestUnitHosts(t *testing.T) {
	for name, c := range map[string]struct {
		in   port.Settings
		want [3]string
	}{
		"services": {
			port.Settings{OrganizationURL: "https://dev.azure.com/acme/"},
			[3]string{"https://dev.azure.com/acme", "/acme", "https://dev.azure.com/acme"},
		},
		"a server": {
			port.Settings{
				OrganizationURL: "https://ado.example/tfs/Default Collection",
				UserFacingHost:  "https://code.example/tfs/Default Collection/",
			},
			[3]string{
				"https://ado.example/tfs/Default Collection", "/tfs/Default%20Collection",
				"https://code.example/tfs/Default Collection",
			},
		},
	} {
		api, base, web, err := azuredevops.Hosts(c.in)
		if err != nil || [3]string{api, base, web} != c.want {
			t.Errorf("%s: Hosts = %s, %s, %s, %v; want %v", name, api, base, web, err, c.want)
		}
	}
	for _, bad := range []port.Settings{
		{},
		{OrganizationURL: "http://dev.azure.com/acme"},
		{OrganizationURL: "dev.azure.com/acme"},
		{OrganizationURL: "https://"},
		{OrganizationURL: "https://u:p@dev.azure.com/acme"},
		{OrganizationURL: "https://dev.azure.com/acme?x=1"},
		{OrganizationURL: "%zz"},
		{OrganizationURL: "https://dev.azure.com/acme", UserFacingHost: "ftp://x"},
	} {
		if _, _, _, err := azuredevops.Hosts(bad); !errors.Is(err, azuredevops.ErrHost) {
			t.Errorf("Hosts(%+v) = %v, want ErrHost", bad, err)
		}
	}
	az := azuredevops.New(azuredevops.Options{})
	in := integrationOf("http://dev.azure.com/acme", "t")
	repo := port.Repository{Namespace: "p", Name: "r", Integration: &in}
	if _, err := az.Branches(t.Context(), repo); !errors.Is(err, azuredevops.ErrHost) {
		t.Errorf("Branches over a bad host = %v", err)
	}
	if _, err := az.ResolveRef(t.Context(), repo, "main"); !errors.Is(err, azuredevops.ErrHost) {
		t.Errorf("ResolveRef over a bad host = %v", err)
	}
	if u := az.SourceURL(repo, "abc"); u != "" {
		t.Errorf("SourceURL over a bad host = %s", u)
	}
	if u := az.SourceURL(port.Repository{Namespace: "p", Name: "r"}, "abc"); u != "" {
		t.Errorf("SourceURL with no integration = %s", u)
	}
	good := integrationOf("https://dev.azure.com/acme", "t")
	good.Settings.UserFacingHost = "https://code.example/acme"
	if u := az.SourceURL(
		port.Repository{Namespace: "p", Name: "r", Integration: &good},
		"abc",
	); u !=
		"https://code.example/acme/p/_git/r" {
		t.Errorf("SourceURL on a user-facing host = %s", u)
	}
}

// TestUnitRoutes checks the allowlist: every route the client calls, and
// no other method, path, dot segment, query or host; and that the client
// refuses to send a request outside it.
func TestUnitRoutes(t *testing.T) {
	for _, p := range []string{
		"/_apis/projects", "/_apis/hooks/subscriptions",
		"/Platform/_apis/git/repositories",
		"/My%20Project/_apis/git/repositories/the%20infra/refs",
		"/Platform/_apis/git/repositories/infra/commits/abc",
		"/Platform/_apis/git/repositories/infra/items",
		"/Platform/_apis/git/repositories/infra/diffs/commits",
	} {
		if !azuredevops.Allowed(http.MethodGet, p) {
			t.Errorf("GET %s refused", p)
		}
	}
	for _, c := range [][2]string{
		{http.MethodPost, "/_apis/hooks/subscriptions"},
		{http.MethodDelete, "/Platform/_apis/git/repositories/infra"},
		{http.MethodGet, "/Platform/_apis/git/repositories/infra"},
		{http.MethodGet, "/Platform/_apis/git/repositories/infra/pushes"},
		{http.MethodGet, "/Platform/_apis/git/repositories/infra/items/../../../_apis/projects"},
		{http.MethodGet, "/_apis/projects/"},
		{http.MethodGet, "/_apis/projects?$top=1"},
		{http.MethodGet, "https://evil.example/_apis/projects"},
		{http.MethodGet, ""},
		{http.MethodGet, "%zz"},
	} {
		if azuredevops.Allowed(c[0], c[1]) {
			t.Errorf("%s %s allowed", c[0], c[1])
		}
	}
	l := newLab(t)
	before := l.fake.Requests()
	if err := l.az.Send(
		t.Context(),
		l.in,
		"/Platform/_apis/git/repositories/infra/pushes",
	); !errors.Is(
		err,
		azuredevops.ErrRoute,
	) {
		t.Errorf("a route outside the allowlist = %v", err)
	}
	if n := l.fake.Requests() - before; n != 0 {
		t.Errorf("sent %d requests outside the allowlist", n)
	}
	if err := l.az.Send(t.Context(), l.in, "/_apis/projects"); err != nil {
		t.Errorf("GET /_apis/projects = %v", err)
	}
}

// TestUnitRefusals checks a repository with no integration, a token that
// is missing, unknown or absent, a file over the limit, and a project the
// user holds no role in.
func TestUnitRefusals(t *testing.T) {
	l := newLab(t)
	ctx := t.Context()
	none := port.Repository{Namespace: "Platform", Name: "infra"}
	for name, err := range map[string]error{
		"ResolveRef":    errOf(l.az.ResolveRef(ctx, none, "main")),
		"ReadFile":      errOf(l.az.ReadFile(ctx, none, "main", "main.tf")),
		"AffectedFiles": errOf(l.az.AffectedFiles(ctx, none, "a", "b")),
		"Branches":      errOf(l.az.Branches(ctx, none)),
	} {
		if !errors.Is(err, azuredevops.ErrNoIntegration) || !errors.Is(err, port.ErrNoIntegration) {
			t.Errorf("%s with no integration = %v", name, err)
		}
	}
	for name, in := range map[string]port.Integration{
		"no token":      integrationOf(l.srv.OrganizationURL, " \n"),
		"unknown token": integrationOf(l.srv.OrganizationURL, "nope"),
	} {
		r := port.Repository{Namespace: "Platform", Name: "infra", Integration: &in}
		if _, err := l.az.ReadFile(ctx, r, l.repo.Head, "main.tf"); !errors.Is(
			err, azuredevops.ErrCredential) {
			t.Errorf("%s: ReadFile = %v, want ErrCredential", name, err)
		}
	}
	big := l.fake.Repo("Platform", "infra").Commit("big", "big", map[string]string{
		"big.bin": strings.Repeat("x", port.MaxFileSize+1),
		"max.bin": strings.Repeat("x", port.MaxFileSize),
	})
	if _, err := l.az.ReadFile(
		ctx,
		l.repo.Repo,
		big,
		"big.bin",
	); !errors.Is(
		err,
		port.ErrTooLarge,
	) {
		t.Errorf("a file over the limit = %v", err)
	}
	if b, err := l.az.ReadFile(ctx, l.repo.Repo, big, "max.bin"); err != nil ||
		len(b) != port.MaxFileSize {
		t.Errorf("a file at the limit = %d bytes, %v", len(b), err)
	}
	if _, err := l.az.Branches(ctx, l.repo.Missing); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("branches of a hidden project = %v", err)
	}
	gone := port.Repository{Namespace: "Platform", Name: "gone", Integration: &l.in}
	if _, err := l.az.ResolveRef(ctx, gone, "main"); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("an unknown repository = %v", err)
	}
}

// errOf is a call's error, its result dropped.
func errOf[T any](_ T, err error) error { return err }

// TestUnitBrowse checks the repositories of every project the token
// reads across pages, of the accessible projects alone, an accessible
// project the organization lacks, a repository's branches across pages,
// and a rename's two names.
func TestUnitBrowse(t *testing.T) {
	l := newLab(t)
	ctx := t.Context()
	want := make([]port.Listed, 0, 122)
	want = append(want, port.Listed{Namespace: "Platform", Name: "infra"})
	for i := range 120 {
		name := fmt.Sprintf("p%03d", i)
		l.fake.Grant(name, user, azuredevopsfake.Reader)
		l.fake.Repo(name, "app")
		want = append(want, port.Listed{Namespace: name, Name: "app"})
	}
	l.fake.Repo("Platform", "docs")
	want = append(want, port.Listed{Namespace: "Platform", Name: "docs"})
	l.fake.Grant("Theirs", "someone", azuredevopsfake.Administrator)
	l.fake.Repo("Theirs", "infra")
	slices.SortFunc(want, func(a, b port.Listed) int {
		return strings.Compare(a.Namespace+"/"+a.Name, b.Namespace+"/"+b.Name)
	})
	if got, err := l.az.Repositories(ctx, l.in); err != nil || !slices.Equal(got, want) {
		t.Errorf("Repositories = %d, %v; want %d", len(got), err, len(want))
	}
	scoped := integrationOf(l.srv.OrganizationURL, l.token, "Platform", "Retired", "Platform")
	if got, err := l.az.Repositories(ctx, scoped); err != nil || !slices.Equal(got, []port.Listed{
		{Namespace: "Platform", Name: "docs"}, {Namespace: "Platform", Name: "infra"},
	}) {
		t.Errorf("Repositories of the accessible projects = %v, %v", got, err)
	}

	r := l.fake.Repo("Platform", "infra")
	names := make([]string, 0, 121)
	names = append(names, "main")
	for i := range 120 {
		b := fmt.Sprintf("b%03d", i)
		r.Branch(b, l.repo.Head)
		names = append(names, b)
	}
	slices.Sort(names)
	if got, err := l.az.Branches(ctx, l.repo.Repo); err != nil || !slices.Equal(got, names) {
		t.Errorf("Branches = %d names, %v; want %d", len(got), err, len(names))
	}
	moved := r.Commit("main", "move", map[string]string{
		"modules/new.tf": "", "modules/renamed.tf": "n",
	})
	if got, err := l.az.AffectedFiles(ctx, l.repo.Repo, l.repo.Head, moved); err != nil ||
		!slices.Equal(got, []string{"modules/new.tf", "modules/renamed.tf"}) {
		t.Errorf("AffectedFiles across a rename = %v, %v", got, err)
	}
	files := map[string]string{}
	wantFiles := make([]string, 0, 150)
	for i := range 150 {
		name := fmt.Sprintf("f%03d.tf", i)
		files[name] = "x"
		wantFiles = append(wantFiles, name)
	}
	many := r.Commit("main", "many", files)
	if got, err := l.az.AffectedFiles(ctx, l.repo.Repo, moved, many); err != nil ||
		!slices.Equal(got, wantFiles) {
		t.Errorf(
			"AffectedFiles across pages = %d paths, %v; want %d",
			len(got),
			err,
			len(wantFiles),
		)
	}
	if _, err := l.az.Repositories(ctx, port.Integration{}); !errors.Is(err, azuredevops.ErrHost) {
		t.Errorf("Repositories with no organization = %v", err)
	}
	if _, err := l.az.Repositories(ctx, integrationOf(l.srv.OrganizationURL, "")); !errors.Is(
		err, azuredevops.ErrCredential) {
		t.Errorf("Repositories with no token = %v", err)
	}
}

// TestUnitTest checks the checks of a token that reads every project it
// reaches and the service hooks, one that misses accessible projects and
// the hooks' scope, one whose projects cannot be listed, and one that
// cannot be read at all.
func TestUnitTest(t *testing.T) {
	l := newLab(t)
	ctx := t.Context()
	passed := func(checks []port.Check) []bool {
		out := make([]bool, 0, len(checks))
		for _, c := range checks {
			out = append(out, c.Passed)
		}
		return out
	}
	got, err := l.az.Test(ctx, l.in)
	if err != nil || !slices.Equal(passed(got), []bool{true, true}) ||
		got[0].Type != port.CheckConnection || got[1].Type != port.CheckPermissions {
		t.Errorf("an administrator = %+v, %v", got, err)
	}

	tok := l.fake.AddToken(user, azuredevopsfake.ScopeProject, azuredevopsfake.ScopeCode)
	projects := make([]string, 0, 12)
	projects = append(projects, "Platform")
	for i := range 11 {
		projects = append(projects, fmt.Sprintf("x%02d", i))
	}
	got, _ = l.az.Test(ctx, integrationOf(l.srv.OrganizationURL, tok, projects...))
	if !slices.Equal(passed(got), []bool{true, false}) ||
		!strings.HasPrefix(got[1].Reason, "The token cannot read the repositories of x00, x01, "+
			"x02, x03, x04, x05, x06, x07, x08, x09 and 1 more, so it reads none there. "+
			"The token cannot read service hooks, so it installs no webhook: ") {
		t.Errorf("a token missing projects and hooks = %+v", got)
	}
	code := l.fake.AddToken(user, azuredevopsfake.ScopeProject, azuredevopsfake.ScopeHooks)
	if got, _ := l.az.Test(ctx, integrationOf(l.srv.OrganizationURL, code)); got[1].Reason !=
		"The token cannot read the repositories of Platform, so it reads none there." {
		t.Errorf("a token without the code scope = %+v", got)
	}

	l.fake.FailPath("/Platform", azuredevopsfake.ServerError, 0)
	got, err = l.az.Test(ctx, l.in)
	if err != nil || !slices.Equal(passed(got), []bool{true, false}) ||
		!strings.HasPrefix(got[1].Reason, "The token's projects could not be listed: ") {
		t.Errorf("failing repositories = %+v, %v", got, err)
	}
	l.fake.Fail(azuredevopsfake.None, 0)

	for name, in := range map[string]port.Integration{
		"no token": integrationOf(l.srv.OrganizationURL, ""),
		"unknown":  integrationOf(l.srv.OrganizationURL, "nope"),
		"no host":  integrationOf("", l.token),
	} {
		got, err := l.az.Test(ctx, in)
		if err != nil || !slices.Equal(passed(got), []bool{false, false}) || got[0].Reason == "" {
			t.Errorf("%s: %+v, %v", name, got, err)
		}
	}
}

// TestUnitRateLimit checks that a rate limit refuses with its reset time,
// from X-RateLimit-Reset or Retry-After, after one request.
func TestUnitRateLimit(t *testing.T) {
	for name, c := range map[string]struct {
		fault azuredevopsfake.Fault
		after time.Duration
	}{
		"X-RateLimit-Reset": {azuredevopsfake.RateLimited, time.Hour},
		"Retry-After":       {azuredevopsfake.TooManyRequests, 90 * time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			l := newLab(t)
			l.fake.Fail(c.fault, c.after)
			before := l.fake.Requests()
			_, err := l.az.ResolveRef(t.Context(), l.repo.Repo, "main")
			rl, ok := errors.AsType[*port.RateLimited](err)
			want := time.Now().Add(c.after)
			if !ok || rl.Reset.Before(want.Add(-3*time.Second)) ||
				rl.Reset.After(want.Add(3*time.Second)) {
				t.Errorf("rate limited = %v, want a reset near %s", err, want)
			}
			if n := l.fake.Requests() - before; n != 1 {
				t.Errorf("sent %d requests, want 1", n)
			}
		})
	}
}

// memory serves h in the calling goroutine, so a hanging handler blocks
// durably inside a synctest bubble, and answers a request whose context
// ended with its error, as a network transport does.
type memory struct{ h http.Handler }

func (m memory) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Host = r.URL.Host
	rec := httptest.NewRecorder()
	m.h.ServeHTTP(rec, r)
	if err := r.Context().Err(); err != nil {
		return nil, err
	}
	return rec.Result(), nil
}

// TestUnitStatus checks answers the fake never gives: a status that is
// none of the port's errors, a 403, a 429 naming no reset, a body that is
// not JSON, a 203 sign-in page, a 203 in JSON, and a continuation token
// that does not move.
func TestUnitStatus(t *testing.T) {
	l := newLab(t)
	odd := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := r.URL.Path
			switch {
			case strings.HasSuffix(p, "/refs") && r.URL.Query().Get("filter") == "tags/v1":
				w.WriteHeader(http.StatusTeapot)
			case strings.HasSuffix(p, "/items"):
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"message":"TF400813: scope missing"}`))
			case strings.HasSuffix(p, "/diffs/commits"):
				w.WriteHeader(http.StatusTooManyRequests)
			case strings.HasSuffix(p, "/_apis/projects") && r.URL.Query().Get("$top") != "1":
				_, _ = w.Write([]byte(`{"value":`))
			case strings.HasSuffix(p, "/_apis/hooks/subscriptions"):
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(http.StatusNonAuthoritativeInfo)
			case strings.HasSuffix(p, "/repositories"):
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNonAuthoritativeInfo)
				_, _ = w.Write([]byte(`{"value":[{"name":"json"}]}`))
			case strings.HasSuffix(p, "/refs") && r.URL.Query().Get("filter") == "heads/":
				w.Header().Set("X-MS-ContinuationToken", "same")
				_, _ = w.Write([]byte(`{"value":[{"name":"refs/heads/x"}]}`))
			default:
				next.ServeHTTP(w, r)
			}
		})
	}
	az := azuredevops.NewOver(memory{odd(l.fake.Handler())})
	in := integrationOf("https://ado.example/"+azuredevopsfake.Organization, l.token)
	repo := l.repo.Repo
	repo.Integration = &in
	ctx := t.Context()
	_, err := az.ResolveRef(ctx, repo, "v1")
	if _, limited := errors.AsType[*port.RateLimited](err); err == nil || limited ||
		errors.Is(err, port.ErrNotFound) || errors.Is(err, kernel.ErrUnavailable) ||
		errors.Is(err, azuredevops.ErrCredential) || !strings.Contains(err.Error(), "ref v1") {
		t.Errorf("a 418 = %v", err)
	}
	if _, err := az.ReadFile(ctx, repo, l.repo.Head, "main.tf"); !errors.Is(
		err, azuredevops.ErrCredential) || !strings.Contains(err.Error(), "scope missing") {
		t.Errorf("a 403 = %v", err)
	}
	before := time.Now()
	_, err = az.AffectedFiles(ctx, repo, l.repo.Base, l.repo.Head)
	if rl, ok := errors.AsType[*port.RateLimited](err); !ok ||
		rl.Reset.Before(before.Add(azuredevops.Backoff-time.Second)) {
		t.Errorf("a 429 naming no reset = %v, want Backoff from now", err)
	}
	if _, err := az.Repositories(ctx, in); !errors.Is(err, kernel.ErrUnavailable) {
		t.Errorf("a body that is not JSON = %v", err)
	}
	if got, err := az.Test(ctx, in); err != nil || !got[0].Passed || got[1].Passed ||
		!strings.HasPrefix(got[1].Reason, "The token's projects could not be listed: ") {
		t.Errorf("Test of a broken projects list = %+v, %v", got, err)
	}
	scoped := integrationOf(in.Settings.OrganizationURL, l.token, "Platform")
	got, err := az.Test(ctx, scoped)
	if err != nil || !got[0].Passed || got[1].Passed ||
		!strings.Contains(got[1].Reason, "sign-in page") {
		t.Errorf("a 203 sign-in page for the hooks = %+v, %v", got, err)
	}
	if rs, err := az.Repositories(ctx, scoped); err != nil || len(rs) != 1 || rs[0].Name != "json" {
		t.Errorf("a 203 in JSON = %v, %v", rs, err)
	}
	if bs, err := az.Branches(ctx, repo); err != nil || !slices.Equal(bs, []string{"x", "x"}) {
		t.Errorf("a continuation token that does not move = %v, %v", bs, err)
	}
}

// TestUnitUnavailable checks that a failing host and a closed one are
// kernel.ErrUnavailable.
func TestUnitUnavailable(t *testing.T) {
	l := newLab(t)
	l.fake.Fail(azuredevopsfake.ServerError, 0)
	if _, err := l.az.ReadFile(t.Context(), l.repo.Repo, "main", "main.tf"); !errors.Is(
		err, kernel.ErrUnavailable) {
		t.Errorf("a failing host = %v", err)
	}
	if _, err := l.az.Repositories(t.Context(), l.in); !errors.Is(err, kernel.ErrUnavailable) {
		t.Errorf("repositories of a failing host = %v", err)
	}
	l.fake.FailPath("/Platform/_apis/git/repositories/infra", azuredevopsfake.ServerError, 0)
	if _, err := l.az.Repositories(t.Context(), l.in); err != nil {
		t.Errorf("repositories with a failing repository = %v", err)
	}
	l.fake.FailPath("/Platform/_apis/git/repositories", azuredevopsfake.ServerError, 0)
	if _, err := l.az.Repositories(t.Context(), l.in); !errors.Is(err, kernel.ErrUnavailable) {
		t.Errorf("a failing project's repositories = %v", err)
	}
	l.fake.Fail(azuredevopsfake.None, 0)
	l.srv.Close()
	if _, err := l.az.AffectedFiles(t.Context(), l.repo.Repo, l.repo.Base, l.repo.Head); !errors.Is(
		err, kernel.ErrUnavailable) {
		t.Errorf("a closed host = %v", err)
	}
	if got, err := l.az.Test(t.Context(), l.in); err != nil || got[0].Passed ||
		!strings.Contains(got[0].Reason, "unavailable") {
		t.Errorf("Test of a closed host = %+v, %v", got, err)
	}
}

// TestUnitTimeout checks that a host that never answers fails the call
// after Timeout as kernel.ErrUnavailable.
func TestUnitTimeout(t *testing.T) {
	f := azuredevopsfake.New()
	tok := f.AddToken(user)
	f.Grant("Platform", user, azuredevopsfake.Administrator)
	r := seed(f.Repo("Platform", "infra"))
	in := integrationOf("https://ado.example/"+azuredevopsfake.Organization, tok)
	r.Repo.Integration = &in
	az := azuredevops.NewOver(memory{f.Handler()})
	synctest.Test(t, func(t *testing.T) {
		f.Fail(azuredevopsfake.Hang, 0)
		start := time.Now()
		_, err := az.ResolveRef(context.Background(), r.Repo, "main")
		if !errors.Is(err, kernel.ErrUnavailable) || time.Since(start) != azuredevops.Timeout {
			t.Errorf("a hanging host = %v after %s", err, time.Since(start))
		}
	})
}
