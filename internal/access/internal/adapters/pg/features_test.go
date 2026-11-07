package pg_test

import (
	"context"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// features is the features domain's rows as the organization delete sees
// them: it records whether its delete ran inside a transaction.
type features struct {
	featuresapi.Entitlements
	deleted []kernel.OrganizationID
	inTx    bool
	err     error
}

func (f *features) DeleteOrganization(ctx context.Context, org kernel.OrganizationID) error {
	f.deleted = append(f.deleted, org)
	f.inTx = tx.In(ctx)
	return f.err
}

// TestAccDeleteOrganizationFeatures checks that deleting an organization
// deletes its features rows in the same transaction, and that a failure
// there keeps the organization.
func TestAccDeleteOrganizationFeatures(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	f := &features{err: errors.New("features down")}
	s := pg.New(db).WithFeatures(f)
	o, a := tenant(t, "pied", "pied")
	if err := s.CreateOrganization(ctx, o, a, nil, created(o)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteOrganization(ctx, o.ID, created(o)); !errors.Is(err, f.err) {
		t.Fatalf("a failed features delete = %v", err)
	}
	if _, err := s.Organization(ctx, o.ID); err != nil {
		t.Errorf("the organization after a failed features delete = %v", err)
	}
	f.err = nil
	if _, err := s.DeleteOrganization(ctx, o.ID, created(o)); err != nil {
		t.Fatal(err)
	}
	if len(f.deleted) != 2 || f.deleted[1] != o.ID || !f.inTx {
		t.Errorf("features deletes = %v, in a transaction %t", f.deleted, f.inTx)
	}
}
