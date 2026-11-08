package assembly

import (
	"context"
	"errors"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/space"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
)

// holder is a SpaceGuard whose rows name every space and whose stacks
// reach one entity each move would cut off.
type holder struct{}

func (holder) SpaceInUse(context.Context, kernel.AccountID, kernel.ULID) (bool, error) {
	return true, nil
}

func (holder) LostOnMove(context.Context, accessapi.SpaceMove) ([]accessapi.LostReach, error) {
	return []accessapi.LostReach{{Stack: "web", Entity: "context shared"}}, nil
}

// TestAccGuardSpaces checks that a guard another domain registers refuses
// the deletes and moves of the domain's trees, which a new organization's
// first account holds.
func TestAccGuardSpaces(t *testing.T) {
	ctx := t.Context()
	db := sqltest.Open(t)
	d := mustNew(t, Deps{DB: db.Primary(), Settings: DefaultSettings()})
	o, err := organization.New("guarded", "Guarded")
	if err != nil {
		t.Fatal(err)
	}
	a, err := account.New(o.ID, "firstAccount.", "guarded", "Guarded")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.store.CreateOrganization(ctx, o, a, nil, kernel.OrganizationAuditEntry{
		OrganizationID:   o.ID,
		ActorKind:        kernel.PrincipalOperator,
		ActorID:          kernel.NewULID().String(),
		Action:           "organization.create",
		EventType:        "CREATE",
		AffectedResource: kernel.AuditResource{Type: "ORGANIZATION", ID: "guarded"},
	}); err != nil {
		t.Fatal(err)
	}
	dev, err := d.spaces.Create(ctx, a.ID, "guarded", space.Space{Name: "Dev"})
	if err != nil {
		t.Fatal(err)
	}
	d.GuardSpaces(holder{})
	for name, err := range map[string]error{
		"delete": d.spaces.Delete(ctx, a.ID, dev.Slug),
		"move":   d.spaces.Move(ctx, a.ID, dev.Slug, space.LegacySlug),
	} {
		if _, ok := errors.AsType[*space.Refusal](err); !ok {
			t.Errorf("%s a guard refuses = %v", name, err)
		}
	}
}
