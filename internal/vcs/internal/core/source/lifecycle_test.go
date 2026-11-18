package source

import (
	"context"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type identityGuard struct {
	Identities
	err   error
	calls int
}

func (g *identityGuard) Check(context.Context, kernel.AccountID, kernel.ULID, string, string) error {
	g.calls++
	return g.err
}

func TestUnitKnownUnavailableDoesNotReachProvider(t *testing.T) {
	for _, state := range []string{"suspended", "deleted", "removed"} {
		t.Run(state, func(t *testing.T) {
			var called string
			var reached port.Repository
			f := newCredentialFixture(t, func(f *credentialFixture) port.Source { return minter{source: source{repo: &reached}, named: &called} })
			refusal := &kernel.Invalid{Detail: "The GitHub App installation on acme is " + state + "."}
			guard := &identityGuard{err: refusal}
			f.svc.identities = guard
			if _, err := f.svc.Resolve(t.Context(), f.acct, f.stack, "main"); !errors.Is(err, refusal) {
				t.Fatal(err)
			}
			if _, err := f.svc.SourceURL(t.Context(), f.acct, f.stack, "abc"); !errors.Is(err, refusal) {
				t.Fatal(err)
			}
			if _, err := f.svc.Credential(t.Context(), f.acct, f.stack, f.run); !errors.Is(err, refusal) {
				t.Fatal(err)
			}
			if _, _, err := f.svc.PreparePush(
				t.Context(),
				f.acct,
				f.in.ID,
				"GITLAB",
				vcsapi.Event{Repository: vcsapi.Repository{Namespace: "acme", Name: "infra"}},
			); !errors.Is(
				err,
				refusal,
			) {
				t.Fatal(err)
			}
			if called != "" || reached.Integration != nil || guard.calls != 4 || len(f.kept) != 0 {
				t.Fatal("known unavailable source performed outbound work", called, reached, guard.calls, f.kept)
			}
		})
	}
}
