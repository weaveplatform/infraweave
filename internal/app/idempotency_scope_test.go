package app

import (
	"net/http/httptest"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestUnitReplayCallerQualifiesOnlyAuthenticatedAccountContext(t *testing.T) {
	p := kernel.Principal{Kind: kernel.PrincipalUser, ActorID: kernel.NewULID(), AccountID: kernel.NewAccountID(), SessionID: kernel.NewULID()}
	request := httptest.NewRequestWithContext(t.Context(), "POST", "/api/v1/policies", nil)
	request.RemoteAddr = "192.0.2.10:12345"
	with := func(principal kernel.Principal) string {
		return replayCaller(request.WithContext(kernel.WithPrincipal(t.Context(), principal)))
	}
	first := with(p)
	if first == p.Caller() {
		t.Fatal("legacy actor-only response namespace reused")
	}
	actor := p.Caller()
	p.SessionID = kernel.NewULID()
	if with(p) != first || p.Caller() != actor {
		t.Fatal("credential renewal changed replay/rate/audit actor identity")
	}
	p.AccountID = kernel.NewAccountID()
	if with(p) == first || p.Caller() != actor {
		t.Fatal("account switching reused response namespace or changed actor identity")
	}
	other := p
	other.ActorID = kernel.NewULID()
	if with(other) == with(p) {
		t.Fatal("independent actors share replay namespace")
	}
	for _, kind := range []kernel.PrincipalKind{kernel.PrincipalAPIKey, kernel.PrincipalRun, kernel.PrincipalOrganization} {
		p.Kind = kind
		p.AccountID = kernel.AccountID{}
		if with(p) != p.Caller() {
			t.Fatalf("accountless %s identity changed", kind)
		}
	}
	if replayCaller(request) != caller(request) {
		t.Fatal("protocol/address identity changed")
	}
}
