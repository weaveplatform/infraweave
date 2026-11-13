package github_test

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/githubfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/github"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/sourcetest"
)

// lab is a fake serving acme's repository infra through an App installed
// on acme, the provider trusting it, and the integration of the App.
type lab struct {
	fake *githubfake.Fake
	srv  *githubfake.Server
	gh   *github.Provider
	app  githubfake.App
	in   port.Integration
	repo sourcetest.Repository
}

func newLab(t *testing.T) *lab {
	t.Helper()
	f := githubfake.New()
	srv := f.Start()
	t.Cleanup(srv.Close)
	l := &lab{fake: f, srv: srv, gh: github.New(github.Options{RootCAs: srv.Pool()})}
	l.app = f.AddApp("infraweave", "acme")
	f.Install(l.app.ID, "acme", "Organization", "infra", "missing-from-host")
	l.in = integrationOf(l.app, srv.URL)
	l.repo = seed(f.Repo("acme", "infra"))
	l.repo.Repo.Integration = &l.in
	l.repo.Missing = port.Repository{
		Namespace:   "acme",
		Name:        "missing-from-host",
		Integration: &l.in,
	}
	return l
}

func integrationOf(a githubfake.App, apiHost string) port.Integration {
	return port.Integration{
		Provider:   "GITHUB_ENTERPRISE",
		Settings:   port.Settings{APIHost: apiHost, AppID: strconv.FormatInt(a.ID, 10)},
		Credential: a.PrivateKey,
	}
}

// seed commits three commits on main, the first tagged v1, and returns
// what sourcetest expects of them.
func seed(r *githubfake.Repo) sourcetest.Repository {
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

// TestUnitSource checks the provider against the Source contract on a
// GitHub Enterprise Server host, named bare and by its API path.
func TestUnitSource(t *testing.T) {
	l := newLab(t)
	sourcetest.Run(t, l.gh, l.repo)

	byPath := integrationOf(l.app, l.srv.URL+"/api/v3/")
	l.repo.Repo.Integration, l.repo.Missing.Integration = &byPath, &byPath
	sourcetest.Run(t, l.gh, l.repo)

	got, err := l.gh.ResolveRef(t.Context(), l.repo.Repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	if got.AuthorLogin == nil || *got.AuthorLogin != githubfake.AuthorLogin ||
		got.URL != l.srv.URL+"/acme/infra/commit/"+got.Hash {
		t.Errorf("ResolveRef(main) = %+v", got)
	}
	l.fake.Unlink(githubfake.AuthorEmail)
	if c, err := l.gh.ResolveRef(t.Context(), l.repo.Repo, "main"); err != nil ||
		c.AuthorLogin != nil || c.Hash != got.Hash {
		t.Errorf("ResolveRef(main) by an e-mail no account owns = %+v, %v", c, err)
	}
	want := l.srv.URL + "/acme/infra.git"
	if u := l.gh.SourceURL(l.repo.Repo, got.Hash); u != want {
		t.Errorf("SourceURL = %s, want %s", u, want)
	}
}

// TestUnitHosts checks how an integration's API host names the API's base
// and the host commits link to.
func TestUnitHosts(t *testing.T) {
	for _, c := range []struct {
		in       port.Settings
		api, web string
	}{
		{port.Settings{}, "https://api.github.com/", "https://github.com"},
		{port.Settings{APIHost: "https://api.github.com"}, "https://api.github.com/", "https://github.com"},
		{port.Settings{APIHost: "https://ghe.example"}, "https://ghe.example/api/v3/", "https://ghe.example"},
		{
			port.Settings{APIHost: "https://ghe.example/api/v3", UserFacingHost: "https://code.example/"},
			"https://ghe.example/api/v3/", "https://code.example",
		},
	} {
		api, web, err := github.Hosts(c.in)
		if err != nil || api != c.api || web != c.web {
			t.Errorf("Hosts(%+v) = %s, %s, %v; want %s, %s", c.in, api, web, err, c.api, c.web)
		}
	}
	for _, bad := range []string{"ftp://ghe.example", "ghe.example", "https://"} {
		if _, _, err := github.Hosts(port.Settings{APIHost: bad}); !errors.Is(err, github.ErrHost) {
			t.Errorf("Hosts(%s) = %v, want ErrHost", bad, err)
		}
	}
	gh := github.New(github.Options{})
	if u := gh.SourceURL(port.Repository{Namespace: "o", Name: "r"}, "abc"); u !=
		"https://github.com/o/r.git" {
		t.Errorf("SourceURL with no integration = %s", u)
	}
	bad := port.Integration{Settings: port.Settings{APIHost: "nope"}}
	if u := gh.SourceURL(port.Repository{Integration: &bad}, "abc"); u != "" {
		t.Errorf("SourceURL on a bad host = %s", u)
	}
}

// TestUnitRefusals checks a repository with no integration, a credential
// that does not parse or that the host does not know, a file over the
// limit, and paths the repository lacks.
func TestUnitRefusals(t *testing.T) {
	l := newLab(t)
	ctx := t.Context()
	none := port.Repository{Namespace: "acme", Name: "infra"}
	if _, err := l.gh.ResolveRef(ctx, none, "main"); !errors.Is(err, github.ErrNoIntegration) {
		t.Errorf("no integration = %v", err)
	}
	for name, in := range map[string]port.Integration{
		"app ID":  {Settings: port.Settings{APIHost: l.srv.URL, AppID: "x"}},
		"key":     {Settings: port.Settings{APIHost: l.srv.URL, AppID: "1"}, Credential: []byte("no")},
		"unknown": integrationOf(githubfake.App{ID: 1, PrivateKey: l.app.PrivateKey}, l.srv.URL),
	} {
		r := port.Repository{Namespace: "acme", Name: "infra", Integration: &in}
		if _, err := l.gh.ReadFile(
			ctx,
			r,
			"main",
			"main.tf",
		); !errors.Is(
			err,
			github.ErrCredential,
		) {
			t.Errorf("%s: ReadFile = %v, want ErrCredential", name, err)
		}
	}
	host := port.Integration{Settings: port.Settings{APIHost: "nope"}}
	if _, err := l.gh.Branches(
		ctx,
		port.Repository{Integration: &host},
	); !errors.Is(
		err,
		github.ErrHost,
	) {
		t.Errorf("a bad host = %v", err)
	}
	big := l.fake.Repo("acme", "infra").Commit("big", "big", map[string]string{
		"big.bin": strings.Repeat("x", port.MaxFileSize+1),
		"max.bin": strings.Repeat("x", port.MaxFileSize),
	})
	if _, err := l.gh.ReadFile(
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
	if b, err := l.gh.ReadFile(
		ctx,
		l.repo.Repo,
		big,
		"max.bin",
	); err != nil ||
		len(b) != port.MaxFileSize {
		t.Errorf("a file at the limit = %d bytes, %v", len(b), err)
	}
	if _, err := l.gh.Branches(ctx, l.repo.Missing); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("branches of a missing repository = %v", err)
	}
	if _, err := l.gh.AffectedFiles(
		ctx,
		l.repo.Missing,
		"a",
		"b",
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("files of a missing repository = %v", err)
	}
	if _, err := l.gh.ReadFile(
		ctx,
		l.repo.Missing,
		"main",
		"x",
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("a file of a missing repository = %v", err)
	}
	if _, err := l.gh.ResolveRef(ctx, l.repo.Repo, l.repo.Head); err != nil {
		t.Errorf("a SHA = %v", err)
	}
}

// TestUnitBrowse checks the repositories every installation reaches and
// a repository's branches, across pages.
func TestUnitBrowse(t *testing.T) {
	l := newLab(t)
	ctx := t.Context()
	l.fake.Install(l.app.ID, "alice", "User", "dotfiles")
	got, err := l.gh.Repositories(ctx, l.in)
	want := []port.Listed{
		{Namespace: "acme", Name: "infra"},
		{Namespace: "acme", Name: "missing-from-host"},
		{Namespace: "alice", Name: "dotfiles"},
	}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("Repositories = %v, %v; want %v", got, err, want)
	}

	r := l.fake.Repo("acme", "infra")
	names := make([]string, 0, 121)
	names = append(names, "main")
	files := map[string]string{}
	for i := range 120 {
		b := fmt.Sprintf("b%03d", i)
		r.Branch(b, l.repo.Head)
		names = append(names, b)
		files[fmt.Sprintf("f%03d.tf", i)] = "x"
	}
	slices.Sort(names)
	if got, err := l.gh.Branches(ctx, l.repo.Repo); err != nil || !slices.Equal(got, names) {
		t.Errorf("Branches = %d names, %v; want %d", len(got), err, len(names))
	}
	wide := r.Commit("wide", "wide", files)
	if got, err := l.gh.AffectedFiles(
		ctx,
		l.repo.Repo,
		l.repo.Head,
		wide,
	); err != nil ||
		len(got) != 120 {
		t.Errorf("AffectedFiles across pages = %d, %v", len(got), err)
	}
	if _, err := l.gh.Repositories(ctx, port.Integration{}); !errors.Is(err, github.ErrCredential) {
		t.Errorf("Repositories with no App = %v", err)
	}
}

// TestUnitTokens checks that calls reuse an installation's token, and a
// changed key mints another.
func TestUnitTokens(t *testing.T) {
	l := newLab(t)
	for range 3 {
		if _, err := l.gh.ResolveRef(t.Context(), l.repo.Repo, "main"); err != nil {
			t.Fatal(err)
		}
	}
	if n := l.fake.Tokens(); n != 1 {
		t.Errorf("three calls minted %d tokens, want 1", n)
	}
	other := l.fake.AddApp("other", "acme")
	l.fake.Install(other.ID, "acme", "Organization", "infra")
	in := integrationOf(other, l.srv.URL)
	l.repo.Repo.Integration = &in
	if _, err := l.gh.ResolveRef(
		t.Context(),
		l.repo.Repo,
		"main",
	); err != nil ||
		l.fake.Tokens() != 2 {
		t.Errorf("another App: %v, %d tokens", err, l.fake.Tokens())
	}
}

// TestUnitMint checks that a run's token is narrowed to its repository
// and contents: read, reads that repository and no other the
// installation reaches, leaves the cached installation token alone, and
// that a repository no installation reaches is not found.
func TestUnitMint(t *testing.T) {
	l := newLab(t)
	ctx := t.Context()
	app := l.fake.AddApp("runs", "acme")
	install := l.fake.Install(app.ID, "acme", "Organization", "infra", "web")
	l.fake.Repo("acme", "web").Commit("main", "web", map[string]string{"main.tf": "w"})
	in := integrationOf(app, l.srv.URL)
	repo := port.Repository{Namespace: "acme", Name: "infra", Integration: &in}
	expires := time.Now().Add(24 * time.Hour)
	c, err := l.gh.Mint(ctx, repo, "run", expires)
	if err != nil {
		t.Fatal(err)
	}
	if c.Username != "x-access-token" || len(c.Token) == 0 || c.ID != "" ||
		c.ExpiresAt.IsZero() || !c.ExpiresAt.Before(expires) {
		t.Errorf("credential = %s, %d bytes, %q, %v", c.Username, len(c.Token), c.ID, c.ExpiresAt)
	}
	want := githubfake.TokenRequest{
		Installation: install, Repositories: []string{"infra"},
		Permissions: map[string]string{"contents": "read"},
	}
	got := l.fake.TokenRequests()
	if len(got) != 1 || got[0].Installation != want.Installation ||
		!slices.Equal(got[0].Repositories, want.Repositories) ||
		len(got[0].Permissions) != 1 || got[0].Permissions["contents"] != "read" {
		t.Errorf("token requests = %+v, want %+v", got, want)
	}

	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs: l.srv.Pool(), MinVersion: tls.VersionTLS12,
	}}}
	status := func(name string) int {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			l.srv.URL+"/api/v3/repos/acme/"+name+"/commits/main", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "token "+string(c.Token))
		res, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	if s := status("infra"); s != http.StatusOK {
		t.Errorf("the run's repository answers %d", s)
	}
	if s := status("web"); s != http.StatusNotFound {
		t.Errorf("another repository of the installation answers %d", s)
	}

	if _, err := l.gh.ResolveRef(ctx, repo, "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.gh.ReadFile(ctx, port.Repository{
		Namespace: "acme", Name: "web", Integration: &in,
	}, "main", "main.tf"); err != nil {
		t.Errorf("the installation's own token, after a mint, reads web: %v", err)
	}

	missing := port.Repository{Namespace: "acme", Name: "nowhere", Integration: &in}
	if _, err := l.gh.Mint(ctx, missing, "run", expires); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("Mint where no installation reaches = %v", err)
	}
	if _, err := l.gh.Mint(ctx, port.Repository{}, "run", expires); !errors.Is(
		err, port.ErrNoIntegration) {
		t.Errorf("Mint with no integration = %v", err)
	}
	bad := in
	bad.Settings.AppID = "x"
	if _, err := l.gh.Mint(ctx, port.Repository{
		Namespace: "acme", Name: "infra", Integration: &bad,
	}, "run", expires); !errors.Is(err, github.ErrCredential) {
		t.Errorf("Mint with a bad App ID = %v", err)
	}
	l.fake.FailPath("/app/installations/", githubfake.ServerError, 0)
	if _, err := l.gh.Mint(ctx, repo, "run", expires); !errors.Is(err, kernel.ErrUnavailable) {
		t.Errorf("Mint on a failing host = %v", err)
	}
	if err := l.gh.Revoke(ctx, repo, ""); err != nil {
		t.Errorf("Revoke = %v", err)
	}
}

// TestUnitRateLimit checks that a primary and a secondary rate limit
// each refuse with their reset time after one request.
func TestUnitRateLimit(t *testing.T) {
	for name, c := range map[string]struct {
		fault githubfake.Fault
		after time.Duration
	}{
		"primary":   {githubfake.RateLimited, time.Hour},
		"secondary": {githubfake.TooManyRequests, 90 * time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			l := newLab(t)
			l.fake.Fail(c.fault, c.after)
			before := l.fake.Requests()
			_, err := l.gh.ResolveRef(t.Context(), l.repo.Repo, "main")
			rl, ok := errors.AsType[*port.RateLimited](err)
			want := time.Now().Add(c.after)
			if !ok || rl.Reset.Before(want.Add(-3*time.Second)) ||
				rl.Reset.After(want.Add(3*time.Second)) {
				t.Errorf("rate limited = %v, want a reset near %s", err, want)
			}
			if n := l.fake.Requests() - before; n != 1 {
				t.Errorf("sent %d requests, want 1", n)
			}
			if !strings.Contains(err.Error(), "rate limited until") {
				t.Errorf("error = %q", err)
			}
		})
	}
}

// TestUnitTokenRateLimit checks that a rate limit on minting the
// installation's token refuses with its reset time, from Retry-After or
// from X-RateLimit-Reset.
func TestUnitTokenRateLimit(t *testing.T) {
	for _, fault := range []githubfake.Fault{githubfake.RateLimited, githubfake.TooManyRequests} {
		l := newLab(t)
		l.fake.FailPath("/app/installations/", fault, time.Hour)
		_, err := l.gh.Branches(t.Context(), l.repo.Repo)
		want := time.Now().Add(time.Hour)
		if rl, ok := errors.AsType[*port.RateLimited](err); !ok ||
			rl.Reset.Before(want.Add(-3*time.Second)) || rl.Reset.After(want.Add(3*time.Second)) {
			t.Errorf("fault %d minting the token = %v", fault, err)
		}
	}
}

// TestUnitUnavailable checks that a failing host and a closed one are
// kernel.ErrUnavailable.
func TestUnitUnavailable(t *testing.T) {
	l := newLab(t)
	l.fake.Fail(githubfake.ServerError, 0)
	if _, err := l.gh.ResolveRef(
		t.Context(),
		l.repo.Repo,
		"main",
	); !errors.Is(
		err,
		kernel.ErrUnavailable,
	) {
		t.Errorf("a failing host = %v", err)
	}
	l.fake.Fail(githubfake.None, 0)
	l.srv.Close()
	if _, err := l.gh.AffectedFiles(
		t.Context(),
		l.repo.Repo,
		"a",
		"b",
	); !errors.Is(
		err,
		kernel.ErrUnavailable,
	) {
		t.Errorf("a closed host = %v", err)
	}
}

// memory serves h in the calling goroutine, so a hanging handler blocks
// durably inside a synctest bubble, and answers a request whose context
// ended with its error, as a network transport does.
type memory struct{ h http.Handler }

func (m memory) RoundTrip(r *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	m.h.ServeHTTP(rec, r)
	if err := r.Context().Err(); err != nil {
		return nil, err
	}
	return rec.Result(), nil
}

// TestUnitTimeout checks that a host that never answers fails the call
// after Timeout as kernel.ErrUnavailable.
func TestUnitTimeout(t *testing.T) {
	f := githubfake.New()
	app := f.AddApp("infraweave", "acme")
	f.Install(app.ID, "acme", "Organization", "infra")
	r := seed(f.Repo("acme", "infra"))
	in := integrationOf(app, "https://ghe.example")
	r.Repo.Integration = &in
	gh := github.NewOver(memory{f.Handler()})
	synctest.Test(t, func(t *testing.T) {
		f.Fail(githubfake.Hang, 0)
		start := time.Now()
		_, err := gh.ResolveRef(context.Background(), r.Repo, "main")
		if !errors.Is(err, kernel.ErrUnavailable) || time.Since(start) != github.Timeout {
			t.Errorf("a hanging host = %v after %s", err, time.Since(start))
		}
	})
}
