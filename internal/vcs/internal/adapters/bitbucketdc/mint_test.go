package bitbucketdc_test

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketdcfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/bitbucketdc"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// TestUnitMint checks that a minted token asks for REPO_READ for the
// days until it expires, reads its repository alone and mints nothing,
// and that revoking it twice refuses it after the first.
func TestUnitMint(t *testing.T) {
	l := newLab(t)
	ctx := t.Context()
	l.fake.Grant("ACME", "web", username, bitbucketdcfake.Admin)
	l.fake.Repo("ACME", "web").Commit("main", "first", map[string]string{"a.tf": "a"})

	got, err := l.bb.Mint(ctx, l.repo.Repo, "infraweave-run-1", time.Now().Add(50*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != bitbucketdc.TokenUser || got.ID == "" || len(got.Token) == 0 ||
		got.ExpiresAt.Before(time.Now().Add(71*time.Hour)) ||
		got.ExpiresAt.After(time.Now().Add(73*time.Hour)) {
		t.Errorf("Mint = %+v", got)
	}
	want := bitbucketdcfake.Minted{
		ID: got.ID, Project: "ACME", Slug: "infra", Name: "infraweave-run-1",
		Permissions: []string{"REPO_READ"}, ExpiryDays: 3,
	}
	if m := l.fake.Minted(); len(m) != 1 || !mintedEqual(m[0], want) {
		t.Errorf("minted %+v, want %+v", m, want)
	}

	run := integrationOf(l.srv.URL, string(got.Token))
	mine := port.Repository{Namespace: "ACME", Name: "infra", Integration: &run}
	if _, err := l.bb.ResolveRef(ctx, mine, "main"); err != nil {
		t.Errorf("the minted token reads its repository: %v", err)
	}
	other := port.Repository{Namespace: "ACME", Name: "web", Integration: &run}
	if _, err := l.bb.ResolveRef(ctx, other, "main"); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("the minted token reads another repository: %v", err)
	}
	if _, err := l.bb.Mint(ctx, mine, "again", time.Now()); !errors.Is(err, port.ErrCannotMint) {
		t.Errorf("the minted token mints: %v", err)
	}
	if err := l.bb.Revoke(ctx, mine, got.ID); !errors.Is(err, bitbucketdc.ErrCredential) {
		t.Errorf("the minted token revokes itself: %v", err)
	}

	if err := l.bb.Revoke(ctx, l.repo.Repo, got.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.bb.ResolveRef(ctx, mine, "main"); !errors.Is(err, bitbucketdc.ErrCredential) {
		t.Errorf("a revoked token reads: %v", err)
	}
	if m := l.fake.Minted(); !m[0].Revoked {
		t.Errorf("minted %+v, not revoked", m)
	}
	if err := l.bb.Revoke(ctx, l.repo.Repo, got.ID); err != nil {
		t.Errorf("revoking again = %v", err)
	}

	soon, err := l.bb.Mint(ctx, l.repo.Repo, "soon", time.Now().Add(-time.Hour))
	if err != nil || l.fake.Minted()[1].ExpiryDays != 1 ||
		soon.ExpiresAt.Before(time.Now().Add(23*time.Hour)) {
		t.Errorf("a token expiring in the past = %+v, %v; minted %+v",
			soon, err, l.fake.Minted())
	}
}

// mintedEqual compares a and b, their permissions by value.
func mintedEqual(a, b bitbucketdcfake.Minted) bool {
	return a.ID == b.ID && a.Project == b.Project && a.Slug == b.Slug && a.Name == b.Name &&
		slices.Equal(a.Permissions, b.Permissions) && a.ExpiryDays == b.ExpiryDays &&
		a.Revoked == b.Revoked
}

// TestUnitMintRefusals checks a token that only reads the repository,
// which mints none, a repository the token cannot see, a repository with
// no integration, and an answer with no token.
func TestUnitMintRefusals(t *testing.T) {
	l := newLab(t)
	ctx := t.Context()
	l.fake.Grant("ACME", "docs", username, bitbucketdcfake.Read)
	l.fake.Repo("ACME", "docs").Commit("main", "first", map[string]string{"a.tf": "a"})
	docs := port.Repository{Namespace: "ACME", Name: "docs", Integration: &l.in}
	if _, err := l.bb.Mint(ctx, docs, "run", time.Now()); !errors.Is(err, port.ErrCannotMint) {
		t.Errorf("a reader mints: %v", err)
	}
	if _, err := l.bb.Mint(ctx, l.repo.Missing, "run", time.Now()); !errors.Is(
		err, port.ErrNotFound) {
		t.Errorf("Mint of a repository the token cannot see = %v", err)
	}
	if err := l.bb.Revoke(ctx, l.repo.Repo, "999"); err != nil {
		t.Errorf("Revoke of a token the repository lacks = %v", err)
	}
	none := port.Repository{Namespace: "ACME", Name: "infra"}
	if _, err := l.bb.Mint(ctx, none, "run", time.Now()); !errors.Is(
		err, bitbucketdc.ErrNoIntegration) {
		t.Errorf("Mint with no integration = %v", err)
	}
	if err := l.bb.Revoke(ctx, none, "1"); !errors.Is(err, bitbucketdc.ErrNoIntegration) {
		t.Errorf("Revoke with no integration = %v", err)
	}
	bad := integrationOf(l.srv.URL, " ")
	if _, err := l.bb.Mint(ctx, port.Repository{
		Namespace: "ACME", Name: "infra", Integration: &bad,
	}, "run", time.Now()); !errors.Is(err, bitbucketdc.ErrCredential) {
		t.Errorf("Mint with no token = %v", err)
	}
	if err := l.bb.Revoke(ctx, port.Repository{
		Namespace: "ACME", Name: "infra", Integration: &bad,
	}, "1"); !errors.Is(err, bitbucketdc.ErrCredential) {
		t.Errorf("Revoke with no token = %v", err)
	}

	odd := func(body string, status int) *bitbucketdc.Provider {
		return bitbucketdc.NewOver(memory{http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasPrefix(r.URL.Path, bitbucketdc.Tokens) {
					t.Errorf("sent %s", r.URL.Path)
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte(body))
			})})
	}
	if _, err := odd(`{"id":"1"}`, http.StatusOK).Mint(
		ctx, l.repo.Repo, "run", time.Now()); !errors.Is(err, kernel.ErrUnavailable) {
		t.Errorf("an answer with no token = %v", err)
	}
	got, err := odd(`{"id":"1","token":"t"}`, http.StatusOK).Mint(
		ctx, l.repo.Repo, "run", time.Now().Add(time.Hour))
	if err != nil || got.ExpiresAt.Before(time.Now().Add(23*time.Hour)) {
		t.Errorf("an answer with no expiry = %+v, %v", got, err)
	}
	if _, err := odd(`{}`, http.StatusBadGateway).Mint(
		ctx, l.repo.Repo, "run", time.Now()); !errors.Is(err, kernel.ErrUnavailable) {
		t.Errorf("a failing host's mint = %v", err)
	}
	if err := odd(`{}`, http.StatusBadGateway).Revoke(
		ctx, l.repo.Repo, "1"); !errors.Is(err, kernel.ErrUnavailable) {
		t.Errorf("a failing host's revoke = %v", err)
	}
}

// TestUnitLend checks that the integration's own token is lent as the
// username it names.
func TestUnitLend(t *testing.T) {
	in := integrationOf("https://bb.example", " BBDC-t\n")
	in.Settings.Username = "infraweave"
	got := bitbucketdc.New(bitbucketdc.Options{}).Lend(in)
	if got.Username != "infraweave" || string(got.Token) != "BBDC-t" || got.ID != "" ||
		!got.ExpiresAt.IsZero() {
		t.Errorf("Lend = %+v", got)
	}
}
