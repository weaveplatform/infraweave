package gitlab_test

import (
	"context"
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
	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/gitlab"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/sourcetest"
)

// lab is a fake serving the project infra in the subgroup acme/platform,
// whose Maintainer the token infraweave acts as, the provider trusting
// it, and the integration of the token.
type lab struct {
	fake  *gitlabfake.Fake
	srv   *gitlabfake.Server
	gl    *gitlab.Provider
	token string
	in    port.Integration
	repo  sourcetest.Repository
}

func newLab(t *testing.T) *lab {
	t.Helper()
	f := gitlabfake.New()
	srv := f.Start()
	t.Cleanup(srv.Close)
	l := &lab{fake: f, srv: srv, gl: gitlab.New(gitlab.Options{RootCAs: srv.Pool()})}
	l.token = f.AddToken("infraweave", "api")
	l.in = integrationOf(l.token, srv.URL)
	f.AddMember("acme/platform", "infra", "infraweave", gitlabfake.Maintainer)
	f.Repo("acme/platform", "hidden").Commit("main", "first", map[string]string{"a.tf": "a"})
	l.repo = seed(f.Repo("acme/platform", "infra"), "acme/platform")
	l.repo.Repo.Integration = &l.in
	l.repo.Missing = port.Repository{
		Namespace: "acme/platform", Name: "hidden", Integration: &l.in,
	}
	return l
}

func integrationOf(token, apiHost string) port.Integration {
	return port.Integration{
		Provider:   "GITLAB",
		Settings:   port.Settings{APIHost: apiHost},
		Credential: []byte(token),
	}
}

// seed commits three commits on main, the first tagged v1, and returns
// what sourcetest expects of them.
func seed(r *gitlabfake.Repo, namespace string) sourcetest.Repository {
	tagged := r.Commit("main", "first", map[string]string{"README.md": "infra"})
	r.Tag("v1", tagged, "the first release")
	base := r.Commit("main", "second", map[string]string{"main.tf": "a", "old.tf": "x"})
	head := r.Commit("main", "third", map[string]string{
		"main.tf": "b", "modules/new.tf": "n", "old.tf": "",
	})
	return sourcetest.Repository{
		Repo:   port.Repository{Namespace: namespace, Name: "infra"},
		Branch: "main", Tag: "v1", Tagged: tagged, Base: base, Head: head,
		File: "main.tf", Content: []byte("b"),
		Changed: []string{"main.tf", "modules/new.tf", "old.tf"},
	}
}

// TestUnitSource checks the provider against the Source contract on a
// project in a subgroup and on one in a top-level group, its API host
// named bare and by its API path.
func TestUnitSource(t *testing.T) {
	l := newLab(t)
	sourcetest.Run(t, l.gl, l.repo)

	byPath := integrationOf(l.token, l.srv.URL+"/api/v4/")
	top := seed(l.fake.Repo("acme", "infra"), "acme")
	l.fake.AddMember("acme", "infra", "infraweave", gitlabfake.Developer)
	top.Repo.Integration = &byPath
	top.Missing = port.Repository{Namespace: "acme", Name: "gone", Integration: &byPath}
	sourcetest.Run(t, l.gl, top)

	got, err := l.gl.ResolveRef(t.Context(), l.repo.Repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	if got.AuthorLogin != nil || got.AuthorName != gitlabfake.AuthorName ||
		got.URL != l.srv.URL+"/acme/platform/infra/-/commit/"+got.Hash {
		t.Errorf("ResolveRef(main) = %+v", got)
	}
	want := l.srv.URL + "/api/v4/projects/acme%2Fplatform%2Finfra/repository/archive.tar.gz?sha=" +
		got.Hash
	if u := l.gl.SourceURL(l.repo.Repo, got.Hash); u != want {
		t.Errorf("SourceURL = %s, want %s", u, want)
	}
	r := l.fake.Repo("acme/platform", "infra")
	r.Branch("release/1", l.repo.Base)
	if got, err := l.gl.ResolveRef(
		t.Context(),
		l.repo.Repo,
		"release/1",
	); err != nil ||
		got.Hash != l.repo.Base || got.Tag != "" {
		t.Errorf("a branch with a slash = %+v, %v", got, err)
	}
	if b, err := l.gl.ReadFile(t.Context(), l.repo.Repo, "main", "modules/new.tf"); err != nil ||
		string(b) != "n" {
		t.Errorf("a nested file = %q, %v", b, err)
	}
}

// TestUnitHosts checks how an integration's API host names the API's base
// and the host commits link to.
func TestUnitHosts(t *testing.T) {
	for _, c := range []struct {
		in       port.Settings
		api, web string
	}{
		{port.Settings{}, "https://gitlab.com/api/v4/", "https://gitlab.com"},
		{port.Settings{APIHost: "https://gitlab.com"}, "https://gitlab.com/api/v4/", "https://gitlab.com"},
		{port.Settings{APIHost: "https://git.example/"}, "https://git.example/api/v4/", "https://git.example"},
		{
			port.Settings{APIHost: "https://git.example/gitlab/api/v4", UserFacingHost: "https://code.example/"},
			"https://git.example/gitlab/api/v4/", "https://code.example",
		},
		{port.Settings{UserFacingHost: "https://gitlab.com/"}, "https://gitlab.com/api/v4/", "https://gitlab.com"},
	} {
		api, web, err := gitlab.Hosts(c.in)
		if err != nil || api != c.api || web != c.web {
			t.Errorf("Hosts(%+v) = %s, %s, %v; want %s, %s", c.in, api, web, err, c.api, c.web)
		}
	}
	for _, bad := range []string{"ftp://git.example", "git.example", "https://", "%zz"} {
		if _, _, err := gitlab.Hosts(port.Settings{APIHost: bad}); !errors.Is(err, gitlab.ErrHost) {
			t.Errorf("Hosts(%s) = %v, want ErrHost", bad, err)
		}
	}
	gl := gitlab.New(gitlab.Options{})
	if u := gl.SourceURL(port.Repository{Namespace: "o/g", Name: "r"}, "abc"); u !=
		"https://gitlab.com/api/v4/projects/o%2Fg%2Fr/repository/archive.tar.gz?sha=abc" {
		t.Errorf("SourceURL with no integration = %s", u)
	}
	bad := port.Integration{Settings: port.Settings{APIHost: "nope"}}
	if u := gl.SourceURL(port.Repository{Integration: &bad}, "abc"); u != "" {
		t.Errorf("SourceURL on a bad host = %s", u)
	}
}

// TestUnitRefusals checks a repository with no integration, a token that
// is missing or that the host does not know, a bad host, a file over the
// limit, and a project the token's user is no member of.
func TestUnitRefusals(t *testing.T) {
	l := newLab(t)
	ctx := t.Context()
	none := port.Repository{Namespace: "acme/platform", Name: "infra"}
	if _, err := l.gl.ResolveRef(ctx, none, "main"); !errors.Is(err, gitlab.ErrNoIntegration) ||
		!errors.Is(err, port.ErrNoIntegration) {
		t.Errorf("no integration = %v", err)
	}
	for name, tok := range map[string]string{"missing": " \n", "unknown": "glpat-nope"} {
		in := integrationOf(tok, l.srv.URL)
		r := port.Repository{Namespace: "acme/platform", Name: "infra", Integration: &in}
		if _, err := l.gl.ReadFile(
			ctx,
			r,
			"main",
			"main.tf",
		); !errors.Is(
			err,
			gitlab.ErrCredential,
		) {
			t.Errorf("%s: ReadFile = %v, want ErrCredential", name, err)
		}
	}
	host := port.Integration{Settings: port.Settings{APIHost: "nope"}, Credential: []byte("t")}
	if _, err := l.gl.Branches(ctx, port.Repository{Integration: &host}); !errors.Is(
		err, gitlab.ErrHost) {
		t.Errorf("a bad host = %v", err)
	}
	big := l.fake.Repo("acme/platform", "infra").Commit("big", "big", map[string]string{
		"big.bin": strings.Repeat("x", port.MaxFileSize+1),
		"max.bin": strings.Repeat("x", port.MaxFileSize),
	})
	if _, err := l.gl.ReadFile(ctx, l.repo.Repo, big, "big.bin"); !errors.Is(
		err, port.ErrTooLarge) {
		t.Errorf("a file over the limit = %v", err)
	}
	if b, err := l.gl.ReadFile(ctx, l.repo.Repo, big, "max.bin"); err != nil ||
		len(b) != port.MaxFileSize {
		t.Errorf("a file at the limit = %d bytes, %v", len(b), err)
	}
	if _, err := l.gl.Branches(ctx, l.repo.Missing); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("branches of a hidden project = %v", err)
	}
	if _, err := l.gl.AffectedFiles(ctx, l.repo.Missing, "a", "b"); !errors.Is(
		err, port.ErrNotFound) {
		t.Errorf("files of a hidden project = %v", err)
	}
	if _, err := l.gl.ResolveRef(ctx, l.repo.Repo, l.repo.Head); err != nil {
		t.Errorf("a SHA = %v", err)
	}
}

// TestUnitBrowse checks the projects the token's user is a member of and
// a project's branches, across pages, and a rename's two names.
func TestUnitBrowse(t *testing.T) {
	l := newLab(t)
	ctx := t.Context()
	want := make([]port.Listed, 0, 122)
	want = append(want, port.Listed{Namespace: "acme/platform", Name: "infra"})
	for i := range 120 {
		name := fmt.Sprintf("p%03d", i)
		l.fake.AddMember("acme/many", name, "infraweave", gitlabfake.Reporter)
		want = append(want, port.Listed{Namespace: "acme/many", Name: name})
	}
	l.fake.AddMember("alice", "dotfiles", "infraweave", gitlabfake.Owner)
	l.fake.AddMember("acme", "other", "someone", gitlabfake.Owner)
	want = append(want, port.Listed{Namespace: "alice", Name: "dotfiles"})
	slices.SortFunc(want, func(a, b port.Listed) int {
		return strings.Compare(a.Namespace+"/"+a.Name, b.Namespace+"/"+b.Name)
	})
	if got, err := l.gl.Repositories(ctx, l.in); err != nil || !slices.Equal(got, want) {
		t.Errorf("Repositories = %d, %v; want %d", len(got), err, len(want))
	}

	r := l.fake.Repo("acme/platform", "infra")
	names := make([]string, 0, 121)
	names = append(names, "main")
	for i := range 120 {
		b := fmt.Sprintf("b%03d", i)
		r.Branch(b, l.repo.Head)
		names = append(names, b)
	}
	slices.Sort(names)
	if got, err := l.gl.Branches(ctx, l.repo.Repo); err != nil || !slices.Equal(got, names) {
		t.Errorf("Branches = %d names, %v; want %d", len(got), err, len(names))
	}
	moved := r.Commit("main", "move", map[string]string{
		"modules/new.tf": "", "modules/renamed.tf": "n",
	})
	if got, err := l.gl.AffectedFiles(ctx, l.repo.Repo, l.repo.Head, moved); err != nil ||
		!slices.Equal(got, []string{"modules/new.tf", "modules/renamed.tf"}) {
		t.Errorf("AffectedFiles across a rename = %v, %v", got, err)
	}
	if _, err := l.gl.Repositories(ctx, port.Integration{}); !errors.Is(err, gitlab.ErrCredential) {
		t.Errorf("Repositories with no token = %v", err)
	}
}

// TestUnitTest checks the checks of a token that has what infraweave
// needs, one that lacks the scope and the Maintainer role, one whose
// scopes or projects cannot be read, and one that cannot be read at all.
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
	got, err := l.gl.Test(ctx, l.in)
	if err != nil || !slices.Equal(passed(got), []bool{true, true}) ||
		got[0].Type != port.CheckConnection || got[1].Type != port.CheckPermissions {
		t.Errorf("a complete token = %+v, %v", got, err)
	}

	reader := integrationOf(l.fake.AddToken("reader", "read_api", "read_repository"), l.srv.URL)
	for i := range 12 {
		l.fake.AddMember("acme", fmt.Sprintf("r%02d", i), "reader", gitlabfake.Developer)
	}
	got, _ = l.gl.Test(ctx, reader)
	if !slices.Equal(passed(got), []bool{true, false}) ||
		got[1].Reason != "The token lacks the api scope. The token is not Maintainer of "+
			"acme/r00, acme/r01, acme/r02, acme/r03, acme/r04, acme/r05, acme/r06, acme/r07, "+
			"acme/r08, acme/r09 and 2 more, so it mints no run token there." {
		t.Errorf("a lacking token = %+v", got)
	}
	l.fake.AddMember("acme", "solo", "solo", gitlabfake.Developer)
	solo := integrationOf(l.fake.AddToken("solo", "api"), l.srv.URL)
	if got, _ := l.gl.Test(ctx, solo); got[1].Reason !=
		"The token is not Maintainer of acme/solo, so it mints no run token there." {
		t.Errorf("a Developer = %+v", got)
	}

	for prefix, reason := range map[string]string{
		"/personal_access_tokens": "The token's scopes could not be read: ",
		"/projects":               "The token's projects could not be listed: ",
	} {
		l.fake.FailPath(prefix, gitlabfake.ServerError, 0)
		got, err := l.gl.Test(ctx, l.in)
		if err != nil || !slices.Equal(passed(got), []bool{true, false}) ||
			!strings.HasPrefix(got[1].Reason, reason) {
			t.Errorf("failing %s = %+v, %v", prefix, got, err)
		}
	}
	l.fake.Fail(gitlabfake.None, 0)

	for name, in := range map[string]port.Integration{
		"no token": integrationOf("", l.srv.URL),
		"unknown":  integrationOf("glpat-nope", l.srv.URL),
	} {
		got, err := l.gl.Test(ctx, in)
		if err != nil || !slices.Equal(passed(got), []bool{false, false}) || got[0].Reason == "" {
			t.Errorf("%s: %+v, %v", name, got, err)
		}
	}
}

// TestUnitRateLimit checks that a rate limit refuses with its reset time,
// from RateLimit-Reset or from Retry-After, after one request.
func TestUnitRateLimit(t *testing.T) {
	for name, c := range map[string]struct {
		fault gitlabfake.Fault
		after time.Duration
	}{
		"RateLimit-Reset": {gitlabfake.RateLimited, time.Hour},
		"Retry-After":     {gitlabfake.TooManyRequests, 90 * time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			l := newLab(t)
			l.fake.Fail(c.fault, c.after)
			before := l.fake.Requests()
			_, err := l.gl.ResolveRef(t.Context(), l.repo.Repo, "main")
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
			if _, err := l.gl.Branches(t.Context(), l.repo.Repo); !errors.As(err, &rl) {
				t.Errorf("a rate limit across pages = %v", err)
			}
		})
	}
}

// TestUnitStatus checks a refusal that is none of the port's errors, a
// 403 carrying no rate limit on a tag lookup after its commit resolved,
// and a comparison GitLab cut short.
func TestUnitStatus(t *testing.T) {
	l := newLab(t)
	odd := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.Contains(r.URL.Path, "/repository/tags/"):
				w.WriteHeader(http.StatusForbidden)
			case strings.HasSuffix(r.URL.Path, "/repository/compare"):
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"diffs":[],"compare_timeout":true}`))
			default:
				next.ServeHTTP(w, r)
			}
		})
	}
	gl := gitlab.NewOver(memory{odd(l.fake.Handler())})
	in := integrationOf(l.token, "https://gitlab.example")
	repo := l.repo.Repo
	repo.Integration = &in
	_, err := gl.ResolveRef(t.Context(), repo, "main")
	if _, limited := errors.AsType[*port.RateLimited](err); err == nil || limited ||
		errors.Is(err, port.ErrNotFound) || errors.Is(err, kernel.ErrUnavailable) ||
		!strings.Contains(err.Error(), "tag main") {
		t.Errorf("a 403 = %v", err)
	}
	if _, err := gl.AffectedFiles(t.Context(), repo, l.repo.Base, l.repo.Head); !errors.Is(
		err, kernel.ErrUnavailable) {
		t.Errorf("a comparison cut short = %v", err)
	}
	none := port.Repository{Namespace: "acme", Name: "infra"}
	if _, err := gl.AffectedFiles(t.Context(), none, "a", "b"); !errors.Is(
		err, gitlab.ErrNoIntegration) {
		t.Errorf("files with no integration = %v", err)
	}
}

// TestUnitUnavailable checks that a failing host and a closed one are
// kernel.ErrUnavailable.
func TestUnitUnavailable(t *testing.T) {
	l := newLab(t)
	l.fake.Fail(gitlabfake.ServerError, 0)
	if _, err := l.gl.ReadFile(t.Context(), l.repo.Repo, "main", "main.tf"); !errors.Is(
		err, kernel.ErrUnavailable) {
		t.Errorf("a failing host = %v", err)
	}
	if _, err := l.gl.Repositories(t.Context(), l.in); !errors.Is(err, kernel.ErrUnavailable) {
		t.Errorf("projects of a failing host = %v", err)
	}
	l.fake.Fail(gitlabfake.None, 0)
	l.srv.Close()
	if _, err := l.gl.AffectedFiles(t.Context(), l.repo.Repo, "a", "b"); !errors.Is(
		err, kernel.ErrUnavailable) {
		t.Errorf("a closed host = %v", err)
	}
	if got, err := l.gl.Test(t.Context(), l.in); err != nil || got[0].Passed ||
		!strings.Contains(got[0].Reason, "unavailable") {
		t.Errorf("Test of a closed host = %+v, %v", got, err)
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
	f := gitlabfake.New()
	tok := f.AddToken("infraweave", "api")
	f.AddMember("acme/platform", "infra", "infraweave", gitlabfake.Maintainer)
	r := seed(f.Repo("acme/platform", "infra"), "acme/platform")
	in := integrationOf(tok, "https://gitlab.example")
	r.Repo.Integration = &in
	gl := gitlab.NewOver(memory{f.Handler()})
	synctest.Test(t, func(t *testing.T) {
		f.Fail(gitlabfake.Hang, 0)
		start := time.Now()
		_, err := gl.ResolveRef(context.Background(), r.Repo, "main")
		if !errors.Is(err, kernel.ErrUnavailable) || time.Since(start) != gitlab.Timeout {
			t.Errorf("a hanging host = %v after %s", err, time.Since(start))
		}
	})
}
