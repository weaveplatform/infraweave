package source

import (
	"errors"
	"fmt"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// TestUnitRunsSource checks that a run's source names the provider's URL
// for the commit with a minted, narrowed credential; the integration's
// own token, not narrowed, where it may not mint; no credential for raw
// Git; and that a failing mint or stack read fails it.
func TestUnitRunsSource(t *testing.T) {
	var repo port.Repository
	minted := newCredentialFixture(t, func(f *credentialFixture) port.Source {
		return minter{source: source{repo: &repo}, id: "7", revoked: &f.revoked}
	})
	got, err := Runs{minted.svc}.Source(t.Context(), minted.acct, minted.stack, minted.run, "abc")
	if err != nil || got.URL != "#abc" || got.Credential == nil || !got.Credential.Narrowed ||
		string(got.Credential.Token) != "narrow" || got.Credential.Username != "minted" ||
		len(minted.kept) != 1 {
		t.Errorf("a minted source = %+v, %v, kept %+v", got, err, minted.kept)
	}

	cannot := fmt.Errorf("gitlab: below Maintainer: %w", port.ErrCannotMint)
	lent := newCredentialFixture(t, func(f *credentialFixture) port.Source {
		return lender{minter{source: source{repo: &repo}, err: cannot, revoked: &f.revoked}}
	})
	got, err = Runs{lent.svc}.Source(t.Context(), lent.acct, lent.stack, lent.run, "abc")
	if err != nil || got.Credential == nil || got.Credential.Narrowed ||
		string(got.Credential.Token) != "wide" {
		t.Errorf("a lent source = %+v, %v", got, err)
	}

	git := newCredentialFixture(t, func(*credentialFixture) port.Source {
		return source{repo: &repo}
	})
	git.svc.integrations = integrations{}
	got, err = Runs{git.svc}.Source(t.Context(), git.acct, git.stack, git.run, "abc")
	if err != nil || got.URL != "#abc" || got.Credential != nil {
		t.Errorf("a raw Git source = %+v, %v", got, err)
	}

	offline := newCredentialFixture(t, func(f *credentialFixture) port.Source {
		return lender{minter{source: source{repo: &repo}, err: errOffline, revoked: &f.revoked}}
	})
	_, err = Runs{offline.svc}.Source(t.Context(), offline.acct, offline.stack, offline.run, "abc")
	if !errors.Is(err, errOffline) {
		t.Errorf("a failing mint = %v", err)
	}
	gone := newCredentialFixture(t, func(*credentialFixture) port.Source { return source{} })
	gone.svc.stacks = stacks{err: errOffline}
	_, err = Runs{gone.svc}.Source(t.Context(), gone.acct, gone.stack, gone.run, "abc")
	if !errors.Is(err, errOffline) {
		t.Errorf("a failing stack read = %v", err)
	}
}
