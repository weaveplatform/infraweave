package pg_test

import (
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
)

func TestAccPolicyIdentitiesRespectAccountAndDeletion(t *testing.T) {
	f := newFixture(t)
	p := f.create(t, "Original snapshot")
	foreign := p
	foreign.Account = kernel.NewAccountID()
	foreign.Space = foreign.Account.ULID
	foreign.Name = "Other account"
	if err := f.store.Create(t.Context(), foreign); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []core.Policy{p, foreign} {
		got, err := f.svc.Identities(t.Context(), owner.Account, []kernel.ULID{p.ID, kernel.NewULID()})
		if err != nil || len(got) != 1 || got[p.ID].Name != owner.Name || got[p.ID].Version != owner.Version || got[p.ID].Type != policyapi.Plan || got[p.ID].Space != owner.Space {
			t.Fatalf("identity crossed account or lost metadata: %+v %v", got, err)
		}
	}
	if _, err := f.svc.Delete(t.Context(), f.principal, p.ID.String(), kernel.Expect{}, false); err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.Identities(t.Context(), p.Account, []kernel.ULID{p.ID})
	if err != nil || len(got) != 0 {
		t.Fatalf("deleted policy still live: %+v %v", got, err)
	}
	f.db.Close()
	if _, err := f.svc.Identities(t.Context(), foreign.Account, []kernel.ULID{p.ID}); err == nil {
		t.Fatal("unavailable metadata was reported as a deleted policy")
	}
}

var _ policyapi.PolicyIdentities = (*core.Service)(nil)
