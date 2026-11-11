package bitbucketdc_test

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
	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketdcfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/bitbucketdc"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/sourcetest"
)

// username is the user the lab's token belongs to.
const username = "infraweave"

// recorder sends each request on to next, failing t on any request whose
// route is not in the provider's allowlist: one that reaches here is one
// the provider's own check let through.
type recorder struct {
	t    *testing.T
	next http.RoundTripper
}

func (r recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	rel, ok := strings.CutPrefix(req.URL.EscapedPath(), bitbucketdc.API)
	if !ok || !bitbucketdc.Allowed(req.Method, rel) {
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

// lab is a fake serving the repository infra in the project ACME, which
// the lab's user administers, the provider trusting it and recording its
// requests, and the integration of the user's token.
type lab struct {
	fake  *bitbucketdcfake.Fake
	srv   *bitbucketdcfake.Server
	bb    *bitbucketdc.Provider
	token string
	in    port.Integration
	repo  sourcetest.Repository
}

func newLab(t *testing.T) *lab {
	t.Helper()
	f := bitbucketdcfake.New()
	srv := f.Start()
	t.Cleanup(srv.Close)
	l := &lab{fake: f, srv: srv, bb: bitbucketdc.NewOver(recorder{t, trusting(srv.Pool())})}
	l.token = f.AddToken(username)
	l.in = integrationOf(srv.URL, l.token)
	f.Grant("ACME", "infra", username, bitbucketdcfake.Admin)
	f.Repo("ACME", "hidden").Commit("main", "first", map[string]string{"a.tf": "a"})
	l.repo = seed(f.Repo("ACME", "infra"))
	l.repo.Repo.Integration = &l.in
	l.repo.Missing = port.Repository{Namespace: "ACME", Name: "hidden", Integration: &l.in}
	return l
}

func integrationOf(host, token string) port.Integration {
	return port.Integration{
		Provider:   "BITBUCKET_DATACENTER",
		Settings:   port.Settings{APIHost: host},
		Credential: []byte(token),
	}
}

// seed commits three commits on main, the first tagged v1, and returns
// what sourcetest expects of them.
func seed(r *bitbucketdcfake.Repo) sourcetest.Repository {
	tagged := r.Commit("main", "first", map[string]string{"README.md": "infra"})
	r.Tag("v1", tagged, "the first release")
	base := r.Commit("main", "second", map[string]string{"main.tf": "a", "old.tf": "x"})
	head := r.Commit("main", "third", map[string]string{
		"main.tf": "b", "modules/new.tf": "n", "old.tf": "",
	})
	return sourcetest.Repository{
		Repo:   port.Repository{Namespace: "ACME", Name: "infra"},
		Branch: "main", Tag: "v1", Tagged: tagged, Base: base, Head: head,
		File: "main.tf", Content: []byte("b"),
		Changed: []string{"main.tf", "modules/new.tf", "old.tf"},
	}
}

// TestUnitSource checks the provider against the Source contract, a
// commit's link, author and source URL, a branch whose name another's
// holds, a branch with a slash, a nested file and an abbreviated SHA.
func TestUnitSource(t *testing.T) {
	l := newLab(t)
	ctx := t.Context()
	sourcetest.Run(t, l.bb, l.repo)

	got, err := l.bb.ResolveRef(ctx, l.repo.Repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	if got.AuthorLogin != nil || got.AuthorName != bitbucketdcfake.AuthorName ||
		got.URL != l.srv.URL+"/projects/ACME/repos/infra/commits/"+got.Hash {
		t.Errorf("ResolveRef(main) = %+v", got)
	}
	if u := l.bb.SourceURL(l.repo.Repo, got.Hash); u != l.srv.URL+"/scm/acme/infra.git" {
		t.Errorf("SourceURL = %s", u)
	}
	l.fake.AddToken(bitbucketdcfake.AuthorUser)
	if got, err := l.bb.ResolveRef(ctx, l.repo.Repo, "v1"); err != nil || got.AuthorLogin == nil ||
		*got.AuthorLogin != "author" || got.AuthorName != "author" || got.Tag != "v1" {
		t.Errorf("a commit by a user = %+v, %v", got, err)
	}
	r := l.fake.Repo("ACME", "infra")
	r.Branch("release/1", l.repo.Base)
	r.Branch("release/10", l.repo.Head)
	if got, err := l.bb.ResolveRef(ctx, l.repo.Repo, "release/1"); err != nil ||
		got.Hash != l.repo.Base || got.Tag != "" {
		t.Errorf("a branch with a slash = %+v, %v", got, err)
	}
	if b, err := l.bb.ReadFile(ctx, l.repo.Repo, "release/1", "old.tf"); err != nil ||
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
	if _, err := l.bb.ReadFile(ctx, l.repo.Repo, "nope", "main.tf"); !errors.Is(
		err, port.ErrNotFound) {
		t.Errorf("a file at an unknown ref = %v", err)
	}
	moved := r.Commit("main", "move", map[string]string{
		"modules/new.tf": "", "modules/renamed.tf": "n",
	})
	if got, err := l.bb.AffectedFiles(ctx, l.repo.Repo, l.repo.Head, moved); err != nil ||
		!slices.Equal(got, []string{"modules/new.tf", "modules/renamed.tf"}) {
		t.Errorf("AffectedFiles across a move = %v, %v", got, err)
	}
}

// TestUnitHosts checks how an integration's hosts name the REST API's
// base and the host commits link to, and that a host that is not an https
// URL answers ErrHost and no source URL.
func TestUnitHosts(t *testing.T) {
	for _, c := range []struct {
		s        port.Settings
		api, web string
	}{
		{
			port.Settings{APIHost: "https://bb.example/"},
			"https://bb.example" + bitbucketdc.API, "https://bb.example",
		},
		{
			port.Settings{APIHost: "https://bb.internal/bitbucket", UserFacingHost: "https://bb.example/"},
			"https://bb.internal/bitbucket" + bitbucketdc.API, "https://bb.example",
		},
	} {
		api, web, err := bitbucketdc.Hosts(c.s)
		if err != nil || api != c.api || web != c.web {
			t.Errorf("Hosts(%+v) = %s, %s, %v; want %s, %s", c.s, api, web, err, c.api, c.web)
		}
	}
	for _, bad := range []port.Settings{
		{},
		{APIHost: "http://bb.example"},
		{APIHost: "bb.example"},
		{APIHost: "https://"},
		{APIHost: "%zz"},
		{APIHost: "https://u:p@bb.example"},
		{APIHost: "https://bb?x=1"},
		{APIHost: "https://bb.example", UserFacingHost: "ftp://bb"},
	} {
		if _, _, err := bitbucketdc.Hosts(bad); !errors.Is(err, bitbucketdc.ErrHost) {
			t.Errorf("Hosts(%+v) = %v, want ErrHost", bad, err)
		}
	}
	bb := bitbucketdc.New(bitbucketdc.Options{})
	in := integrationOf("http://bb.example", "t")
	repo := port.Repository{Namespace: "P", Name: "r", Integration: &in}
	if _, err := bb.Branches(t.Context(), repo); !errors.Is(err, bitbucketdc.ErrHost) {
		t.Errorf("Branches over a bad host = %v", err)
	}
	if _, err := bb.ResolveRef(t.Context(), repo, "main"); !errors.Is(err, bitbucketdc.ErrHost) {
		t.Errorf("ResolveRef over a bad host = %v", err)
	}
	if _, err := bb.Repositories(t.Context(), in); !errors.Is(err, bitbucketdc.ErrHost) {
		t.Errorf("Repositories over a bad host = %v", err)
	}
	for name, r := range map[string]port.Repository{
		"a bad host": repo, "no integration": {Namespace: "P", Name: "r"},
	} {
		if u := bb.SourceURL(r, "abc"); u != "" {
			t.Errorf("SourceURL with %s = %s", name, u)
		}
	}
}

// TestUnitRoutes checks the allowlist: every route the client calls, and
// no other method, path, dot segment, query or host; and that the client
// refuses to send a request outside it.
func TestUnitRoutes(t *testing.T) {
	for _, p := range []string{
		"/profile/recent/repos", "/repos",
		"/projects/ACME/repos/infra/branches",
		"/projects/ACME/repos/infra/tags/release/1",
		"/projects/ACME/repos/infra/commits/abc",
		"/projects/ACME/repos/infra/raw/modules/a%20b.tf",
		"/projects/ACME/repos/infra/compare/changes",
	} {
		if !bitbucketdc.Allowed(http.MethodGet, p) {
			t.Errorf("GET %s refused", p)
		}
	}
	for _, c := range [][2]string{
		{http.MethodPost, "/repos"},
		{http.MethodDelete, "/projects/ACME/repos/infra/branches"},
		{http.MethodGet, "/projects/ACME/repos/infra/webhooks"},
		{http.MethodGet, "/projects/ACME/repos/infra/raw/../../webhooks"},
		{http.MethodGet, "/projects/ACME/repos/infra/commits/a/b"},
		{http.MethodGet, "/repos/"},
		{http.MethodGet, "/repos?permission=REPO_ADMIN"},
		{http.MethodGet, "https://evil.example/repos"},
		{http.MethodGet, ""},
		{http.MethodGet, "%zz"},
	} {
		if bitbucketdc.Allowed(c[0], c[1]) {
			t.Errorf("%s %s allowed", c[0], c[1])
		}
	}
	l := newLab(t)
	before := l.fake.Requests()
	if err := l.bb.Send(t.Context(), l.in, "/projects/ACME/repos/infra/webhooks"); !errors.Is(
		err, bitbucketdc.ErrRoute) {
		t.Errorf("a route outside the allowlist = %v", err)
	}
	if n := l.fake.Requests() - before; n != 0 {
		t.Errorf("sent %d requests outside the allowlist", n)
	}
	if err := l.bb.Send(t.Context(), l.in, "/repos"); err != nil {
		t.Errorf("GET /repos = %v", err)
	}
	unbased := bitbucketdc.Transport(bitbucketdc.NewOver(trusting(l.srv.Pool())))
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, l.srv.URL+"/repos", nil)
	if resp, err := unbased.RoundTrip(req); !errors.Is(err, bitbucketdc.ErrRoute) {
		if resp != nil {
			_ = resp.Body.Close()
		}
		t.Errorf("a request with no REST API base = %v", err)
	}
}

// TestUnitRefusals checks a repository with no integration, a token that
// is missing or that the host does not know, a file over the limit, and a
// repository the user holds no role on.
func TestUnitRefusals(t *testing.T) {
	l := newLab(t)
	ctx := t.Context()
	none := port.Repository{Namespace: "ACME", Name: "infra"}
	for name, err := range map[string]error{
		"ResolveRef":    errOf(l.bb.ResolveRef(ctx, none, "main")),
		"ReadFile":      errOf(l.bb.ReadFile(ctx, none, "main", "main.tf")),
		"AffectedFiles": errOf(l.bb.AffectedFiles(ctx, none, "a", "b")),
		"Branches":      errOf(l.bb.Branches(ctx, none)),
	} {
		if !errors.Is(err, bitbucketdc.ErrNoIntegration) || !errors.Is(err, port.ErrNoIntegration) {
			t.Errorf("%s with no integration = %v", name, err)
		}
	}
	for name, in := range map[string]port.Integration{
		"no token":      integrationOf(l.srv.URL, " \n"),
		"unknown token": integrationOf(l.srv.URL, "BBDC-nope"),
	} {
		r := port.Repository{Namespace: "ACME", Name: "infra", Integration: &in}
		if _, err := l.bb.ReadFile(ctx, r, l.repo.Head, "main.tf"); !errors.Is(
			err, bitbucketdc.ErrCredential) {
			t.Errorf("%s: ReadFile = %v, want ErrCredential", name, err)
		}
	}
	big := l.fake.Repo("ACME", "infra").Commit("big", "big", map[string]string{
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
	if _, err := l.bb.AffectedFiles(ctx, l.repo.Missing, "a", "b"); !errors.Is(
		err, port.ErrNotFound) {
		t.Errorf("files of a hidden repository = %v", err)
	}
}

// errOf is a call's error, its result dropped.
func errOf[T any](_ T, err error) error { return err }

// TestUnitBrowse checks the repositories the user reads across projects
// and pages, and a repository's branches across pages.
func TestUnitBrowse(t *testing.T) {
	l := newLab(t)
	ctx := t.Context()
	want := make([]port.Listed, 0, 122)
	want = append(want, port.Listed{Namespace: "ACME", Name: "infra"})
	for i := range 120 {
		name := fmt.Sprintf("r%03d", i)
		l.fake.Grant("MANY", name, username, bitbucketdcfake.Read)
		want = append(want, port.Listed{Namespace: "MANY", Name: name})
	}
	l.fake.Grant("~ALICE", "dotfiles", username, bitbucketdcfake.Admin)
	l.fake.Grant("ACME", "other", "someone", bitbucketdcfake.Admin)
	want = append(want, port.Listed{Namespace: "~ALICE", Name: "dotfiles"})
	slices.SortFunc(want, func(a, b port.Listed) int {
		return strings.Compare(a.Namespace+"/"+a.Name, b.Namespace+"/"+b.Name)
	})
	if got, err := l.bb.Repositories(ctx, l.in); err != nil || !slices.Equal(got, want) {
		t.Errorf("Repositories = %d, %v; want %d", len(got), err, len(want))
	}

	r := l.fake.Repo("ACME", "infra")
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
	if _, err := l.bb.Repositories(ctx, integrationOf(l.srv.URL, "")); !errors.Is(
		err, bitbucketdc.ErrCredential) {
		t.Errorf("Repositories with no token = %v", err)
	}
}

// TestUnitTest checks the checks of a user that administers every
// repository it reads, one that only writes to some, one whose
// repositories cannot be listed, and one that cannot be read at all.
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

	writer := integrationOf(l.srv.URL, l.fake.AddToken("writer"))
	for i := range 12 {
		l.fake.Grant("ACME", fmt.Sprintf("r%02d", i), "writer", bitbucketdcfake.Write)
	}
	l.fake.Grant("ACME", "owned", "writer", bitbucketdcfake.Admin)
	got, _ = l.bb.Test(ctx, writer)
	if !slices.Equal(passed(got), []bool{true, false}) ||
		got[1].Reason != "The token is not admin of ACME/r00, ACME/r01, ACME/r02, ACME/r03, "+
			"ACME/r04, ACME/r05, ACME/r06, ACME/r07, ACME/r08, ACME/r09 and 2 more, "+
			"so it mints no run token and installs no webhook there." {
		t.Errorf("a writer = %+v", got)
	}
	l.fake.Grant("SOLO", "infra", "solo", bitbucketdcfake.Read)
	solo := integrationOf(l.srv.URL, l.fake.AddToken("solo"))
	if got, _ := l.bb.Test(ctx, solo); got[1].Reason !=
		"The token is not admin of SOLO/infra, so it mints no run token and installs no webhook there." {
		t.Errorf("a reader = %+v", got)
	}

	l.fake.FailPath("/repos", bitbucketdcfake.ServerError, 0)
	got, err = l.bb.Test(ctx, l.in)
	if err != nil || !slices.Equal(passed(got), []bool{true, false}) ||
		!strings.HasPrefix(got[1].Reason, "The token's repositories could not be listed: ") {
		t.Errorf("failing /repos = %+v, %v", got, err)
	}
	l.fake.Fail(bitbucketdcfake.None, 0)

	for name, in := range map[string]port.Integration{
		"no token": integrationOf(l.srv.URL, ""),
		"unknown":  integrationOf(l.srv.URL, "BBDC-nope"),
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
		fault bitbucketdcfake.Fault
		after time.Duration
		want  time.Duration
	}{
		"no reset":    {bitbucketdcfake.RateLimited, time.Hour, bitbucketdc.Backoff},
		"Retry-After": {bitbucketdcfake.TooManyRequests, 90 * time.Second, 90 * time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			l := newLab(t)
			l.fake.Fail(c.fault, c.after)
			before := l.fake.Requests()
			_, err := l.bb.ResolveRef(t.Context(), l.repo.Repo, l.repo.Head)
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
// none of the port's errors, a 403, a body that is not JSON, a page that
// names no next page, and an error with no message.
func TestUnitStatus(t *testing.T) {
	l := newLab(t)
	odd := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := r.URL.Path
			switch {
			case strings.Contains(p, "/tags/"):
				w.WriteHeader(http.StatusTeapot)
			case strings.Contains(p, "/raw/"):
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"errors":[{"message":"REPO_READ missing"}]}`))
			case strings.Contains(p, "/compare/"):
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"errors":[]}`))
			case strings.HasSuffix(p, "/repos"):
				_, _ = w.Write([]byte(`{"values":`))
			case strings.HasSuffix(p, "/branches") && r.URL.Query().Get("filterText") == "":
				_, _ = w.Write([]byte(`{"values":[],"isLastPage":false}`))
			default:
				next.ServeHTTP(w, r)
			}
		})
	}
	bb := bitbucketdc.NewOver(memory{odd(l.fake.Handler())})
	ctx := t.Context()
	_, err := bb.ResolveRef(ctx, l.repo.Repo, "v1")
	if _, limited := errors.AsType[*port.RateLimited](err); err == nil || limited ||
		errors.Is(err, port.ErrNotFound) || errors.Is(err, kernel.ErrUnavailable) ||
		errors.Is(err, bitbucketdc.ErrCredential) || !strings.Contains(err.Error(), "ref v1") {
		t.Errorf("a 418 = %v", err)
	}
	if _, err := bb.ReadFile(ctx, l.repo.Repo, l.repo.Head, "main.tf"); !errors.Is(
		err, bitbucketdc.ErrCredential) || !strings.Contains(err.Error(), "REPO_READ missing") {
		t.Errorf("a 403 = %v", err)
	}
	if _, err := bb.AffectedFiles(ctx, l.repo.Repo, l.repo.Base, l.repo.Head); !errors.Is(
		err, bitbucketdc.ErrStatus) || !strings.Contains(err.Error(), "409") {
		t.Errorf("a 409 with no message = %v", err)
	}
	if _, err := bb.Repositories(ctx, l.in); !errors.Is(err, kernel.ErrUnavailable) {
		t.Errorf("a body that is not JSON = %v", err)
	}
	if _, err := bb.Branches(ctx, l.repo.Repo); !errors.Is(err, kernel.ErrUnavailable) {
		t.Errorf("a page with no next page = %v", err)
	}
}

// TestUnitUnavailable checks that a failing host and a closed one are
// kernel.ErrUnavailable.
func TestUnitUnavailable(t *testing.T) {
	l := newLab(t)
	l.fake.Fail(bitbucketdcfake.ServerError, 0)
	if _, err := l.bb.ReadFile(t.Context(), l.repo.Repo, "main", "main.tf"); !errors.Is(
		err, kernel.ErrUnavailable) {
		t.Errorf("a failing host = %v", err)
	}
	if _, err := l.bb.ReadFile(t.Context(), l.repo.Repo, l.repo.Head, "main.tf"); !errors.Is(
		err, kernel.ErrUnavailable) {
		t.Errorf("a failing host at a SHA = %v", err)
	}
	if _, err := l.bb.Repositories(t.Context(), l.in); !errors.Is(err, kernel.ErrUnavailable) {
		t.Errorf("repositories of a failing host = %v", err)
	}
	l.fake.Fail(bitbucketdcfake.None, 0)
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
	f := bitbucketdcfake.New()
	tok := f.AddToken(username)
	f.Grant("ACME", "infra", username, bitbucketdcfake.Admin)
	r := seed(f.Repo("ACME", "infra"))
	in := integrationOf("https://bitbucket.example", tok)
	r.Repo.Integration = &in
	bb := bitbucketdc.NewOver(memory{f.Handler()})
	synctest.Test(t, func(t *testing.T) {
		f.Fail(bitbucketdcfake.Hang, 0)
		start := time.Now()
		_, err := bb.ResolveRef(context.Background(), r.Repo, "main")
		if !errors.Is(err, kernel.ErrUnavailable) || time.Since(start) != bitbucketdc.Timeout {
			t.Errorf("a hanging host = %v after %s", err, time.Since(start))
		}
	})
}
