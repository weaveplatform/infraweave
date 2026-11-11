package bitbucketcloud_test

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
	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketcloudfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/bitbucketcloud"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/sourcetest"
)

// email is the account the lab's token belongs to.
const email = "infraweave@example.com"

// recorder sends each request on to next, failing t on any request whose
// route is not in the provider's allowlist: one that reaches here is one
// the provider's own check let through.
type recorder struct {
	t    *testing.T
	next http.RoundTripper
}

func (r recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	rel, ok := strings.CutPrefix(req.URL.EscapedPath(), "/2.0")
	if !ok || !bitbucketcloud.Allowed(req.Method, rel) {
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

// lab is a fake serving the repository infra in the workspace acme, which
// the lab's account administers, the provider trusting it and recording
// its requests, and the integration of the account's token.
type lab struct {
	fake  *bitbucketcloudfake.Fake
	srv   *bitbucketcloudfake.Server
	bb    *bitbucketcloud.Provider
	token string
	in    port.Integration
	repo  sourcetest.Repository
}

func newLab(t *testing.T) *lab {
	t.Helper()
	f := bitbucketcloudfake.New()
	srv := f.Start()
	t.Cleanup(srv.Close)
	l := &lab{
		fake: f, srv: srv,
		bb: bitbucketcloud.NewOver(recorder{t, trusting(srv.Pool())}, srv.APIHost),
	}
	l.token = f.AddToken(email)
	l.in = integrationOf(email, l.token)
	f.Grant("acme", "infra", email, bitbucketcloudfake.Admin)
	f.Repo("acme", "hidden").Commit("main", "first", map[string]string{"a.tf": "a"})
	l.repo = seed(f.Repo("acme", "infra"))
	l.repo.Repo.Integration = &l.in
	l.repo.Missing = port.Repository{Namespace: "acme", Name: "hidden", Integration: &l.in}
	return l
}

func integrationOf(email, token string) port.Integration {
	return port.Integration{
		Provider:   "BITBUCKET_CLOUD",
		Settings:   port.Settings{Email: email},
		Credential: []byte(token),
	}
}

// seed commits three commits on main, the first tagged v1, and returns
// what sourcetest expects of them.
func seed(r *bitbucketcloudfake.Repo) sourcetest.Repository {
	tagged := r.Commit("main", "first", map[string]string{"README.md": "infra"})
	r.Tag("v1", tagged, "the first release")
	base := r.Commit("main", "second", map[string]string{"main.tf": "a", "old.tf": "x"})
	head := r.Commit("main", "third", map[string]string{
		"main.tf": "b", "modules/new.tf": "n", "old.tf": "",
	})
	return sourcetest.Repository{
		Repo:   port.Repository{Namespace: "acme", Name: "infra"},
		Branch: "main", Tag: "v1", Tagged: tagged, Base: base, Head: head,
		File: "main.tf", Content: []byte("b"),
		Changed: []string{"main.tf", "modules/new.tf", "old.tf"},
	}
}

// TestUnitSource checks the provider against the Source contract, a
// commit's link, author and source URL, a branch with a slash, a nested
// file and an abbreviated SHA.
func TestUnitSource(t *testing.T) {
	l := newLab(t)
	ctx := t.Context()
	sourcetest.Run(t, l.bb, l.repo)

	got, err := l.bb.ResolveRef(ctx, l.repo.Repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	if got.AuthorLogin != nil || got.AuthorName != bitbucketcloudfake.AuthorName ||
		got.URL != l.srv.URL+"/acme/infra/commits/"+got.Hash {
		t.Errorf("ResolveRef(main) = %+v", got)
	}
	if u := l.bb.SourceURL(l.repo.Repo, got.Hash); u != l.srv.URL+"/acme/infra.git" {
		t.Errorf("SourceURL = %s", u)
	}
	l.fake.AddToken(bitbucketcloudfake.AuthorEmail)
	if got, err := l.bb.ResolveRef(ctx, l.repo.Repo, "v1"); err != nil || got.AuthorLogin == nil ||
		*got.AuthorLogin != "author" || got.AuthorName != "author" || got.Tag != "v1" {
		t.Errorf("a commit by an account = %+v, %v", got, err)
	}
	r := l.fake.Repo("acme", "infra")
	r.Branch("release/1", l.repo.Base)
	if got, err := l.bb.ResolveRef(ctx, l.repo.Repo, "release/1"); err != nil ||
		got.Hash != l.repo.Base || got.Tag != "" {
		t.Errorf("a branch with a slash = %+v, %v", got, err)
	}
	if b, err := l.bb.ReadFile(
		ctx,
		l.repo.Repo,
		"release/1",
		"old.tf",
	); err != nil ||
		string(b) != "x" {
		t.Errorf("a file on a branch with a slash = %q, %v", b, err)
	}
	if b, err := l.bb.ReadFile(ctx, l.repo.Repo, "main", "modules/new.tf"); err != nil ||
		string(b) != "n" {
		t.Errorf("a nested file = %q, %v", b, err)
	}
	if got, err := l.bb.ResolveRef(ctx, l.repo.Repo, l.repo.Head[:12]); err != nil ||
		got.Hash != l.repo.Head {
		t.Errorf("an abbreviated SHA = %+v, %v", got, err)
	}
	if _, err := l.bb.ResolveRef(ctx, l.repo.Repo, "abcdef12"); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("an unknown abbreviated SHA = %v", err)
	}
	if _, err := l.bb.ReadFile(
		ctx,
		l.repo.Repo,
		"nope",
		"main.tf",
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("a file at an unknown ref = %v", err)
	}
}

// TestUnitHosts checks how an API host names the API's base and the host
// commits link to, and that a provider over a bad one answers ErrHost.
func TestUnitHosts(t *testing.T) {
	for in, want := range map[string][2]string{
		"":                                    {bitbucketcloud.DefaultAPIHost, "https://bitbucket.org"},
		bitbucketcloud.DefaultAPIHost + "/":   {bitbucketcloud.DefaultAPIHost, "https://bitbucket.org"},
		"https://bb.example/2.0/":             {"https://bb.example/2.0", "https://bb.example"},
		"http://127.0.0.1:8080/bitbucket/2.0": {"http://127.0.0.1:8080/bitbucket/2.0", "http://127.0.0.1:8080"},
	} {
		api, web, err := bitbucketcloud.Hosts(in)
		if err != nil || api != want[0] || web != want[1] {
			t.Errorf("Hosts(%q) = %s, %s, %v; want %s, %s", in, api, web, err, want[0], want[1])
		}
	}
	for _, bad := range []string{"ftp://bb", "bb.example", "https://", "%zz", "https://bb/2.0?x=1"} {
		if _, _, err := bitbucketcloud.Hosts(bad); !errors.Is(err, bitbucketcloud.ErrHost) {
			t.Errorf("Hosts(%s) = %v, want ErrHost", bad, err)
		}
	}
	bb := bitbucketcloud.New(bitbucketcloud.Options{})
	if u := bb.SourceURL(port.Repository{Namespace: "o", Name: "r"}, "abc"); u !=
		"https://bitbucket.org/o/r.git" {
		t.Errorf("SourceURL = %s", u)
	}
	bad := bitbucketcloud.New(bitbucketcloud.Options{APIHost: "nope"})
	in := integrationOf(email, "t")
	repo := port.Repository{Namespace: "o", Name: "r", Integration: &in}
	if _, err := bad.Branches(t.Context(), repo); !errors.Is(err, bitbucketcloud.ErrHost) {
		t.Errorf("Branches over a bad host = %v", err)
	}
	if _, err := bad.Repositories(t.Context(), in); !errors.Is(err, bitbucketcloud.ErrHost) {
		t.Errorf("Repositories over a bad host = %v", err)
	}
	if u := bad.SourceURL(repo, "abc"); u != "" {
		t.Errorf("SourceURL over a bad host = %s", u)
	}
}

// TestUnitRoutes checks the allowlist: every route the client calls, and
// no other method, path, dot segment, query or host; and that the client
// refuses to send a request outside it.
func TestUnitRoutes(t *testing.T) {
	for _, p := range []string{
		"/user", "/user/workspaces", "/repositories/acme",
		"/repositories/acme/infra/refs/branches",
		"/repositories/acme/infra/refs/branches/release/1",
		"/repositories/acme/infra/refs/tags/v1",
		"/repositories/acme/infra/commit/abc",
		"/repositories/acme/infra/src/abc/modules/a%20b.tf",
		"/repositories/acme/infra/diffstat/a..b",
		"/repositories/acme/infra/diffstat/feature%2Fx..main",
	} {
		if !bitbucketcloud.Allowed(http.MethodGet, p) {
			t.Errorf("GET %s refused", p)
		}
	}
	for _, c := range [][2]string{
		{http.MethodPost, "/user"},
		{http.MethodDelete, "/repositories/acme/infra/refs/branches/main"},
		{http.MethodGet, "/repositories/acme/infra/hooks"},
		{http.MethodGet, "/repositories/acme/infra/src/abc/../../hooks"},
		{http.MethodGet, "/repositories/acme/infra/commit/a/b"},
		{http.MethodGet, "/user/"},
		{http.MethodGet, "/user?fields=email"},
		{http.MethodGet, "https://evil.example/user"},
		{http.MethodGet, ""},
		{http.MethodGet, "%zz"},
	} {
		if bitbucketcloud.Allowed(c[0], c[1]) {
			t.Errorf("%s %s allowed", c[0], c[1])
		}
	}
	l := newLab(t)
	before := l.fake.Requests()
	if err := l.bb.Send(t.Context(), l.in, "/repositories/acme/infra/hooks"); !errors.Is(
		err, bitbucketcloud.ErrRoute) {
		t.Errorf("a route outside the allowlist = %v", err)
	}
	if n := l.fake.Requests() - before; n != 0 {
		t.Errorf("sent %d requests outside the allowlist", n)
	}
	if err := l.bb.Send(t.Context(), l.in, "/user"); err != nil {
		t.Errorf("GET /user = %v", err)
	}
}

// TestUnitRefusals checks a repository with no integration, an e-mail or
// token that is missing or that the host does not know, a file over the
// limit, and a repository the account holds no role on.
func TestUnitRefusals(t *testing.T) {
	l := newLab(t)
	ctx := t.Context()
	none := port.Repository{Namespace: "acme", Name: "infra"}
	for name, err := range map[string]error{
		"ResolveRef":    errOf(l.bb.ResolveRef(ctx, none, "main")),
		"ReadFile":      errOf(l.bb.ReadFile(ctx, none, "main", "main.tf")),
		"AffectedFiles": errOf(l.bb.AffectedFiles(ctx, none, "a", "b")),
		"Branches":      errOf(l.bb.Branches(ctx, none)),
	} {
		if !errors.Is(err, bitbucketcloud.ErrNoIntegration) ||
			!errors.Is(err, port.ErrNoIntegration) {
			t.Errorf("%s with no integration = %v", name, err)
		}
	}
	for name, in := range map[string]port.Integration{
		"no token":      integrationOf(email, " \n"),
		"no e-mail":     integrationOf("", l.token),
		"unknown token": integrationOf(email, "ATATTnope"),
		"another email": integrationOf("someone@example.com", l.token),
	} {
		r := port.Repository{Namespace: "acme", Name: "infra", Integration: &in}
		if _, err := l.bb.ReadFile(ctx, r, l.repo.Head, "main.tf"); !errors.Is(
			err, bitbucketcloud.ErrCredential) {
			t.Errorf("%s: ReadFile = %v, want ErrCredential", name, err)
		}
	}
	big := l.fake.Repo("acme", "infra").Commit("big", "big", map[string]string{
		"big.bin": strings.Repeat("x", port.MaxFileSize+1),
		"max.bin": strings.Repeat("x", port.MaxFileSize),
	})
	if _, err := l.bb.ReadFile(
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
	if b, err := l.bb.ReadFile(ctx, l.repo.Repo, big, "max.bin"); err != nil ||
		len(b) != port.MaxFileSize {
		t.Errorf("a file at the limit = %d bytes, %v", len(b), err)
	}
	if _, err := l.bb.Branches(ctx, l.repo.Missing); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("branches of a hidden repository = %v", err)
	}
	if _, err := l.bb.AffectedFiles(
		ctx,
		l.repo.Missing,
		"a",
		"b",
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("files of a hidden repository = %v", err)
	}
}

// errOf is a call's error, its result dropped.
func errOf[T any](_ T, err error) error { return err }

// TestUnitBrowse checks the repositories the account is a member of
// across workspaces and pages, a repository's branches across pages, and
// a rename's two names.
func TestUnitBrowse(t *testing.T) {
	l := newLab(t)
	ctx := t.Context()
	want := make([]port.Listed, 0, 122)
	want = append(want, port.Listed{Namespace: "acme", Name: "infra"})
	for i := range 120 {
		name := fmt.Sprintf("r%03d", i)
		l.fake.Grant("many", name, email, bitbucketcloudfake.Read)
		want = append(want, port.Listed{Namespace: "many", Name: name})
	}
	l.fake.Grant("alice", "dotfiles", email, bitbucketcloudfake.Admin)
	l.fake.Grant("acme", "other", "someone@example.com", bitbucketcloudfake.Admin)
	want = append(want, port.Listed{Namespace: "alice", Name: "dotfiles"})
	slices.SortFunc(want, func(a, b port.Listed) int {
		return strings.Compare(a.Namespace+"/"+a.Name, b.Namespace+"/"+b.Name)
	})
	if got, err := l.bb.Repositories(ctx, l.in); err != nil || !slices.Equal(got, want) {
		t.Errorf("Repositories = %d, %v; want %d", len(got), err, len(want))
	}

	r := l.fake.Repo("acme", "infra")
	names := make([]string, 0, 121)
	names = append(names, "main")
	for i := range 120 {
		b := fmt.Sprintf("b%03d", i)
		r.Branch(b, l.repo.Head)
		names = append(names, b)
	}
	slices.Sort(names)
	if got, err := l.bb.Branches(ctx, l.repo.Repo); err != nil || !slices.Equal(got, names) {
		t.Errorf("Branches = %d names, %v; want %d", len(got), err, len(names))
	}
	moved := r.Commit("main", "move", map[string]string{
		"modules/new.tf": "", "modules/renamed.tf": "n",
	})
	if got, err := l.bb.AffectedFiles(ctx, l.repo.Repo, l.repo.Head, moved); err != nil ||
		!slices.Equal(got, []string{"modules/new.tf", "modules/renamed.tf"}) {
		t.Errorf("AffectedFiles across a rename = %v, %v", got, err)
	}
	if _, err := l.bb.Repositories(
		ctx,
		port.Integration{},
	); !errors.Is(
		err,
		bitbucketcloud.ErrCredential,
	) {
		t.Errorf("Repositories with no token = %v", err)
	}
}

// TestUnitTest checks the checks of an account that administers every
// repository it reaches, one that only reads some, one whose repositories
// cannot be listed, and one that cannot be read at all.
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
	got, err := l.bb.Test(ctx, l.in)
	if err != nil || !slices.Equal(passed(got), []bool{true, true}) ||
		got[0].Type != port.CheckConnection || got[1].Type != port.CheckPermissions {
		t.Errorf("an admin = %+v, %v", got, err)
	}

	reader := integrationOf("reader@example.com", l.fake.AddToken("reader@example.com"))
	for i := range 12 {
		l.fake.Grant(
			"acme",
			fmt.Sprintf("r%02d", i),
			"reader@example.com",
			bitbucketcloudfake.Write,
		)
	}
	l.fake.Grant("acme", "owned", "reader@example.com", bitbucketcloudfake.Admin)
	got, _ = l.bb.Test(ctx, reader)
	if !slices.Equal(passed(got), []bool{true, false}) ||
		got[1].Reason != "The token is not admin of acme/r00, acme/r01, acme/r02, acme/r03, "+
			"acme/r04, acme/r05, acme/r06, acme/r07, acme/r08, acme/r09 and 2 more, "+
			"so it installs no webhook there." {
		t.Errorf("a writer = %+v", got)
	}
	l.fake.Grant("solo", "infra", "solo@example.com", bitbucketcloudfake.Read)
	solo := integrationOf("solo@example.com", l.fake.AddToken("solo@example.com"))
	if got, _ := l.bb.Test(ctx, solo); got[1].Reason !=
		"The token is not admin of solo/infra, so it installs no webhook there." {
		t.Errorf("a reader = %+v", got)
	}

	for _, prefix := range []string{"/user/workspaces", "/repositories"} {
		l.fake.FailPath(prefix, bitbucketcloudfake.ServerError, 0)
		got, err := l.bb.Test(ctx, l.in)
		if err != nil || !slices.Equal(passed(got), []bool{true, false}) ||
			!strings.HasPrefix(got[1].Reason, "The token's repositories could not be listed: ") {
			t.Errorf("failing %s = %+v, %v", prefix, got, err)
		}
	}
	l.fake.Fail(bitbucketcloudfake.None, 0)

	for name, in := range map[string]port.Integration{
		"no token": integrationOf(email, ""),
		"unknown":  integrationOf(email, "ATATTnope"),
	} {
		got, err := l.bb.Test(ctx, in)
		if err != nil || !slices.Equal(passed(got), []bool{false, false}) || got[0].Reason == "" {
			t.Errorf("%s: %+v, %v", name, got, err)
		}
	}
}

// TestUnitRateLimit checks that a rate limit refuses with its reset time,
// from Retry-After or Backoff when Bitbucket names none, after one
// request.
func TestUnitRateLimit(t *testing.T) {
	for name, c := range map[string]struct {
		fault bitbucketcloudfake.Fault
		after time.Duration
		want  time.Duration
	}{
		"no reset":    {bitbucketcloudfake.RateLimited, time.Hour, bitbucketcloud.Backoff},
		"Retry-After": {bitbucketcloudfake.TooManyRequests, 90 * time.Second, 90 * time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			l := newLab(t)
			l.fake.Fail(c.fault, c.after)
			before := l.fake.Requests()
			_, err := l.bb.ResolveRef(t.Context(), l.repo.Repo, "main")
			rl, ok := errors.AsType[*port.RateLimited](err)
			want := time.Now().Add(c.want)
			if !ok || rl.Reset.Before(want.Add(-3*time.Second)) ||
				rl.Reset.After(want.Add(3*time.Second)) {
				t.Errorf("rate limited = %v, want a reset near %s", err, want)
			}
			if n := l.fake.Requests() - before; n != 1 {
				t.Errorf("sent %d requests, want 1", n)
			}
			if _, err := l.bb.Branches(t.Context(), l.repo.Repo); !errors.As(err, &rl) {
				t.Errorf("a rate limit across pages = %v", err)
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
// none of the port's errors, a 403, a reset in X-RateLimit-Reset, a body
// that is not JSON, and a next page off the API.
func TestUnitStatus(t *testing.T) {
	l := newLab(t)
	reset := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	odd := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := r.URL.Path
			switch {
			case strings.Contains(p, "/refs/tags/"):
				w.WriteHeader(http.StatusTeapot)
			case strings.Contains(p, "/src/"):
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"type":"error","error":{"message":"scope missing"}}`))
			case strings.Contains(p, "/diffstat/"):
				w.Header().Set("X-RateLimit-Reset", fmt.Sprint(reset.Unix()))
				w.WriteHeader(http.StatusTooManyRequests)
			case strings.HasSuffix(p, "/user/workspaces"):
				_, _ = w.Write([]byte(`{"values":`))
			case strings.HasSuffix(p, "/refs/branches"):
				_, _ = w.Write([]byte(`{"values":[],"next":"https://evil.example/2.0/user"}`))
			default:
				next.ServeHTTP(w, r)
			}
		})
	}
	bb := bitbucketcloud.NewOver(memory{odd(l.fake.Handler())}, "https://bitbucket.example/2.0")
	ctx := t.Context()
	_, err := bb.ResolveRef(ctx, l.repo.Repo, "v1")
	if _, limited := errors.AsType[*port.RateLimited](err); err == nil || limited ||
		errors.Is(err, port.ErrNotFound) || errors.Is(err, kernel.ErrUnavailable) ||
		errors.Is(err, bitbucketcloud.ErrCredential) || !strings.Contains(err.Error(), "ref v1") {
		t.Errorf("a 418 = %v", err)
	}
	if _, err := bb.ReadFile(ctx, l.repo.Repo, l.repo.Head, "main.tf"); !errors.Is(
		err, bitbucketcloud.ErrCredential) || !strings.Contains(err.Error(), "scope missing") {
		t.Errorf("a 403 = %v", err)
	}
	_, err = bb.AffectedFiles(ctx, l.repo.Repo, l.repo.Base, l.repo.Head)
	if rl, ok := errors.AsType[*port.RateLimited](err); !ok || !rl.Reset.Equal(reset) {
		t.Errorf("X-RateLimit-Reset = %v, want %s", err, reset)
	}
	if _, err := bb.Repositories(ctx, l.in); !errors.Is(err, kernel.ErrUnavailable) {
		t.Errorf("a body that is not JSON = %v", err)
	}
	if _, err := bb.Branches(ctx, l.repo.Repo); !errors.Is(err, kernel.ErrUnavailable) {
		t.Errorf("a next page off the API = %v", err)
	}
}

// TestUnitUnavailable checks that a failing host and a closed one are
// kernel.ErrUnavailable.
func TestUnitUnavailable(t *testing.T) {
	l := newLab(t)
	l.fake.Fail(bitbucketcloudfake.ServerError, 0)
	if _, err := l.bb.ReadFile(t.Context(), l.repo.Repo, "main", "main.tf"); !errors.Is(
		err, kernel.ErrUnavailable) {
		t.Errorf("a failing host = %v", err)
	}
	if _, err := l.bb.Repositories(t.Context(), l.in); !errors.Is(err, kernel.ErrUnavailable) {
		t.Errorf("repositories of a failing host = %v", err)
	}
	l.fake.Fail(bitbucketcloudfake.None, 0)
	l.srv.Close()
	if _, err := l.bb.AffectedFiles(t.Context(), l.repo.Repo, "a", "b"); !errors.Is(
		err, kernel.ErrUnavailable) {
		t.Errorf("a closed host = %v", err)
	}
	if got, err := l.bb.Test(t.Context(), l.in); err != nil || got[0].Passed ||
		!strings.Contains(got[0].Reason, "unavailable") {
		t.Errorf("Test of a closed host = %+v, %v", got, err)
	}
}

// TestUnitTimeout checks that a host that never answers fails the call
// after Timeout as kernel.ErrUnavailable.
func TestUnitTimeout(t *testing.T) {
	f := bitbucketcloudfake.New()
	tok := f.AddToken(email)
	f.Grant("acme", "infra", email, bitbucketcloudfake.Admin)
	r := seed(f.Repo("acme", "infra"))
	in := integrationOf(email, tok)
	r.Repo.Integration = &in
	bb := bitbucketcloud.NewOver(memory{f.Handler()}, "https://bitbucket.example/2.0")
	synctest.Test(t, func(t *testing.T) {
		f.Fail(bitbucketcloudfake.Hang, 0)
		start := time.Now()
		_, err := bb.ResolveRef(context.Background(), r.Repo, "main")
		if !errors.Is(err, kernel.ErrUnavailable) || time.Since(start) != bitbucketcloud.Timeout {
			t.Errorf("a hanging host = %v after %s", err, time.Since(start))
		}
	})
}
