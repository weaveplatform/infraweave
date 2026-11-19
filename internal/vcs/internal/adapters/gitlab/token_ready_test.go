package gitlab_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/gitlab"
)

// A successful create response can precede the bot's project authorization.
// The same token must become usable before handover; every failure must revoke
// it, including cancellation, without returning the integration's wider token.
func TestUnitMintWaitsForRepositoryAccess(t *testing.T) {
	for _, tc := range []struct {
		name    string
		refused int
		status  int
		cancel  bool
		wantErr error
		elapsed time.Duration
	}{
		{name: "authorizations catch up", refused: 2, elapsed: 500 * time.Millisecond},
		{name: "authorization never arrives", refused: 1000, wantErr: kernel.ErrUnavailable, elapsed: gitlab.Timeout},
		{name: "cancelled", refused: 1, cancel: true, wantErr: context.Canceled},
		{name: "forbidden", status: http.StatusForbidden, wantErr: gitlab.ErrCredential},
		{name: "missing", status: http.StatusNotFound, wantErr: gitlab.ErrCredential},
		{name: "login page", status: http.StatusOK, wantErr: gitlab.ErrCredential},
		{name: "redirect", status: http.StatusFound, wantErr: gitlab.ErrCredential},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := gitlabfake.New()
				token := f.AddToken("infraweave", "api")
				f.AddMember("acme/platform", "infra", "infraweave", gitlabfake.Maintainer)
				repo := seed(f.Repo("acme/platform", "infra"), "acme/platform").Repo
				in := integrationOf(token, "https://gitlab.example")
				repo.Integration = &in
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				attempts := 0
				handler := f.Handler()
				gl := gitlab.NewOver(memory{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/info/refs") {
						attempts++
						user, password, ok := r.BasicAuth()
						if !ok || user != gitlab.Username || password == "" || password == token {
							t.Fatal("readiness did not authenticate with the newly minted token")
						}
						if tc.cancel {
							cancel()
						}
						if attempts <= tc.refused {
							w.WriteHeader(http.StatusUnauthorized)
							return
						}
						if tc.status != 0 {
							w.Header().Set("Location", "https://elsewhere.example/login")
							w.WriteHeader(tc.status)
							return
						}
					}
					handler.ServeHTTP(w, r)
				})})
				start := time.Now()
				credential, err := gl.Mint(ctx, repo, "run", start.Add(24*time.Hour))
				if !errors.Is(err, tc.wantErr) || time.Since(start) != tc.elapsed {
					t.Fatalf("Mint error = %v after %s; want %v after %s", err, time.Since(start), tc.wantErr, tc.elapsed)
				}
				minted := f.Minted()
				if len(minted) != 1 || minted[0].Revoked != (tc.wantErr != nil) {
					t.Fatalf("minted token cleanup = %+v", minted)
				}
				if tc.wantErr == nil {
					if attempts != tc.refused+1 || len(credential.Token) == 0 {
						t.Fatal("handed over before the minted token could read its repository")
					}
				} else if len(credential.Token) != 0 {
					t.Fatal("returned a token after failed authorization")
				}
			})
		})
	}
}
