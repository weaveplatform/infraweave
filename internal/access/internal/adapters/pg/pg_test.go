package pg_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
)

func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, schema.Stream(), pg.Stream())) }

func pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(t.Context(), sqltest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// tenant is a new organization and its first account.
func tenant(t *testing.T, org, acct string) (organization.Organization, account.Account) {
	t.Helper()
	o, err := organization.New(org, "Org "+org)
	if err != nil {
		t.Fatal(err)
	}
	a, err := account.New(o.ID, "firstAccount.", acct, "Account "+acct)
	if err != nil {
		t.Fatal(err)
	}
	return o, a
}

func TestAccCreateOrganization(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	o, a := tenant(t, "acme", "acme")
	if err := s.CreateOrganization(ctx, o, a, nil, created(o)); err != nil {
		t.Fatal(err)
	}
	got, err := s.Organization(ctx, o.ID)
	if err != nil || got.ID != o.ID || got.Slug != "acme" || got.Name != "Org acme" ||
		got.CreatedAt.IsZero() {
		t.Errorf("Organization = %+v, %v", got, err)
	}
	ga, err := s.Account(ctx, a.ID)
	if err != nil || ga.OrganizationID != o.ID || ga.Slug != "acme" || ga.Claimed() {
		t.Errorf("Account = %+v, %v", ga, err)
	}

	// An account slug is unique across the installation, not per
	// organization; an organization slug among organizations. Neither
	// insert of a refused pair lands.
	o2, a2 := tenant(t, "acme", "other")
	if err := s.CreateOrganization(
		ctx,
		o2,
		a2,
		nil,
		created(o2),
	); !errors.Is(
		err,
		port.ErrSlugTaken,
	) {
		t.Errorf("duplicate organization slug = %v, want ErrSlugTaken", err)
	}
	o3, a3 := tenant(t, "globex", "acme")
	if err := s.CreateOrganization(
		ctx,
		o3,
		a3,
		nil,
		created(o3),
	); !errors.Is(
		err,
		port.ErrSlugTaken,
	) {
		t.Errorf("duplicate account slug = %v, want ErrSlugTaken", err)
	}
	if _, err := s.Organization(ctx, o3.ID); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("the refused account's organization was kept: %v", err)
	}
	if _, err := db.Exec(
		ctx,
		`UPDATE accounts SET claimed_at = now() WHERE id = $1`,
		a.ID,
	); err != nil {
		t.Fatal(err)
	}
	if ga, err := s.Account(ctx, a.ID); err != nil || !ga.Claimed() {
		t.Errorf("claimed account = %+v, %v", ga, err)
	}
	if _, err := s.Account(ctx, kernel.NewAccountID()); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("unknown account = %v, want ErrNotFound", err)
	}
}

func TestAccSlugImmutable(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	o, a := tenant(t, "initech", "initech")
	if err := s.CreateOrganization(ctx, o, a, nil, created(o)); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`UPDATE organizations SET slug = 'initrode' WHERE id = $1`,
		`UPDATE accounts SET slug = 'initrode' WHERE organization_id = $1`,
	} {
		_, err := db.Exec(ctx, stmt, o.ID)
		if pe, ok := errors.AsType[*pgconn.PgError](err); !ok || pe.Code != "23514" {
			t.Errorf("%s: %v, want a check violation", stmt, err)
		}
	}
	// A rename changes the name alone.
	if _, err := db.Exec(
		ctx,
		`UPDATE organizations SET name = 'Initech Ltd' WHERE id = $1`,
		o.ID,
	); err != nil {
		t.Errorf("rename: %v", err)
	}
}

func TestAccTenancyKeys(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	o, a := tenant(t, "umbrella", "umbrella")
	if err := s.CreateOrganization(ctx, o, a, nil, created(o)); err != nil {
		t.Fatal(err)
	}
	other, otherAcct := tenant(t, "cyberdyne", "cyberdyne")
	if err := s.CreateOrganization(ctx, other, otherAcct, nil, created(other)); err != nil {
		t.Fatal(err)
	}
	member := kernel.NewMemberID()
	if _, err := db.Exec(ctx, `INSERT INTO members (organization_id, id, login, name, email, status)
		VALUES ($1, $2, 'alice', 'Alice', 'alice@example.com', 'active')`, o.ID, member); err != nil {
		t.Fatal(err)
	}
	admit := `INSERT INTO account_users (account_id, organization_id, member_id) VALUES ($1, $2, $3)`
	if _, err := db.Exec(ctx, admit, a.ID, o.ID, member); err != nil {
		t.Errorf("admit a member to its organization's account: %v", err)
	}
	// A member of one organization cannot be admitted to another's account.
	_, err := db.Exec(ctx, admit, otherAcct.ID, other.ID, member)
	if pe, ok := errors.AsType[*pgconn.PgError](err); !ok || pe.Code != "23503" {
		t.Errorf("admit across organizations = %v, want a foreign key violation", err)
	}
	// An organization with accounts cannot be deleted from under them.
	_, err = db.Exec(ctx, `DELETE FROM organizations WHERE id = $1`, o.ID)
	if pe, ok := errors.AsType[*pgconn.PgError](err); !ok || pe.Code != "23001" {
		t.Errorf("delete an organization with accounts = %v, want RESTRICT", err)
	}
}

func TestAccStoreUnavailable(t *testing.T) {
	p := pool(t)
	s := pg.New(p)
	p.Close()
	ctx := context.Background()
	o, a := tenant(t, "closed", "closed")
	for name, err := range map[string]error{
		"create":       s.CreateOrganization(ctx, o, a, nil, created(o)),
		"organization": func() error { _, err := s.Organization(ctx, o.ID); return err }(),
		"account":      func() error { _, err := s.Account(ctx, a.ID); return err }(),
	} {
		if err == nil || errors.Is(err, port.ErrNotFound) || errors.Is(err, port.ErrSlugTaken) {
			t.Errorf("%s on a closed pool = %v", name, err)
		}
	}
}

func TestUnitStream(t *testing.T) {
	if pg.Stream().Name != "access" {
		t.Errorf("stream %q", pg.Stream().Name)
	}
	cols := pg.SealedColumns()
	if len(cols) != 1 || cols[0].Table != "signing_keys" || cols[0].Name != "private_key_sealed" {
		t.Errorf("SealedColumns = %+v", cols)
	}
}

// created is the trail entry of an operator creating o.
func created(o organization.Organization) kernel.OrganizationAuditEntry {
	return kernel.OrganizationAuditEntry{
		OrganizationID:   o.ID,
		ActorKind:        kernel.PrincipalOperator,
		ActorID:          kernel.NewULID().String(),
		Action:           "organization.create",
		EventType:        "CREATE",
		AffectedResource: kernel.AuditResource{Type: "ORGANIZATION", ID: string(o.Slug)},
	}
}
