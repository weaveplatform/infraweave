package app

import (
	"context"
	"testing"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
)

// TestAccJobEntitlements checks the queue's entitlement check as the
// scheduler runs it: a job's account names its organization, whose tier
// decides with billing on; a job of no account still meets features.disabled.
func TestAccJobEntitlements(t *testing.T) {
	ctx := t.Context()
	db := sqltest.Open(t)
	c := config.Defaults()
	c.Features.Disabled = []string{"scans"}
	d, err := newDomains(c, db, testKeyring(t, db.Primary()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Features.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	org, acct := kernel.NewOrganizationID(), kernel.NewAccountID()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO organizations (id, slug, name) VALUES ($1, 'o', 'o')`, []any{org}},
		{`INSERT INTO accounts (organization_id, id, slug, name) VALUES ($1, $2, 'o', 'o')`, []any{org, acct}},
		{`UPDATE billing SET enabled = true`, nil},
	} {
		if _, err := db.Primary().Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Features.Entitlements().CreateOrganization(ctx, org, "Free"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		account kernel.ULID
		feature string
		want    bool
	}{
		{acct.ULID, "stacks", true},
		{acct.ULID, "policies", false},
		{kernel.NewULID(), "policies", true},
		{kernel.NewULID(), "scans", false},
	} {
		if ok, err := d.Allowed(ctx, c.account, c.feature); err != nil || ok != c.want {
			t.Errorf("Allowed(%v, %s) = %t, %v; want %t", c.account, c.feature, ok, err, c.want)
		}
	}
	db.Close()
	if _, err := d.Allowed(context.Background(), acct.ULID, "stacks"); err == nil {
		t.Error("a closed database answered")
	}
}
