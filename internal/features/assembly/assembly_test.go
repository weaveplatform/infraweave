package assembly

import (
	"context"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/features"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	entitlement "github.com/weaveplatform/infraweave/internal/features/internal/features"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
)

func TestMain(m *testing.M) {
	os.Exit(sqltest.Main(m, schema.Stream(), Stream()))
}

// TestAccDomain checks the domain's entitlements end to end with billing
// on: features.disabled leaves the entitlement, and a limit check runs in
// its own transaction.
func TestAccDomain(t *testing.T) {
	ctx := t.Context()
	db, err := pgxpool.New(ctx, sqltest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	for _, stmt := range []string{
		`INSERT INTO tiers (name) VALUES ('Starter')`,
		`INSERT INTO tier_features (tier, feature) VALUES ('Starter', 'scans'), ('Starter', 'stacks')`,
		`INSERT INTO tier_limits (tier, limit_name, value) VALUES ('Starter', 'seats', 0)`,
		`UPDATE billing SET enabled = true`,
	} {
		if _, err := db.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	e := New(Deps{
		DB:       db,
		Settings: Settings{Disabled: []string{"scans"}},
	}).Entitlements()
	org := kernel.NewOrganizationID()
	if err := e.CreateOrganization(ctx, org, "Starter"); err != nil {
		t.Fatal(err)
	}
	if got, err := e.Entitlement(
		ctx,
		org,
	); err != nil ||
		!slices.Equal(got.Features, []string{"stacks"}) {
		t.Errorf("Entitlement = %+v, %v", got, err)
	}
	none := func(context.Context) (featuresapi.Usage, error) { return featuresapi.Usage{}, nil }
	err = e.CheckLimit(ctx, org, kernel.NewAccountID(), "seats", none)
	if le, ok := errors.AsType[*featuresapi.LimitError](err); !ok || le.Value != 0 {
		t.Errorf("CheckLimit on a pool of none = %v", err)
	}
}

// TestAccDomainGates checks the domain as the server runs it: the seeded
// tiers, the middleware's refusals, and the watch that drops a snapshot.
func TestAccDomainGates(t *testing.T) {
	ctx := t.Context()
	db, err := pgxpool.New(ctx, sqltest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	d := New(Deps{DB: db, Settings: Settings{Disabled: []string{"scans"}}})
	if err := d.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	var seeded []string
	if err := db.QueryRow(ctx, `SELECT array_agg(name ORDER BY name) FROM tiers`).
		Scan(&seeded); err != nil ||
		!slices.Equal(seeded, slices.Sorted(slices.Values(features.SeededTiers))) {
		t.Fatalf("seeded %v, %v", seeded, err)
	}
	org := kernel.NewOrganizationID()
	if err := d.Entitlements().CreateOrganization(ctx, org, features.Free); err != nil {
		t.Fatal(err)
	}
	full := func(context.Context) (featuresapi.Usage, error) { return featuresapi.Usage{Organization: 1 << 20}, nil }
	if e, err := d.Entitlements().
		Entitlement(ctx, org); err != nil || !slices.Contains(e.Features, "policies") ||
		slices.Contains(e.Features, "scans") ||
		len(e.Limits) != 0 {
		t.Errorf("Free with billing off = %+v, %v; want Enterprise less scans", e, err)
	}
	if err := d.Entitlements().
		CheckLimit(ctx, org, kernel.NewAccountID(), "seats", full); err != nil {
		t.Errorf("CheckLimit with billing off = %v", err)
	}
	if _, err := db.Exec(ctx, `UPDATE billing SET enabled = true`); err != nil {
		t.Fatal(err)
	}
	ch := make(chan notify.Message)
	watch, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.WatchEntitlements(watch, &notify.Subscription{C: ch})
	}()
	// The channel is unbuffered, so once a later send is taken the watch
	// has handled the one before.
	ch <- notify.Message{Payload: "features:billing"}
	ch <- notify.Message{Payload: "access:sessions"}
	member := kernel.WithPrincipal(ctx, kernel.Principal{OrganizationID: org})
	status := func(feature string) int {
		t.Helper()
		h := d.Middleware(func(*nethttp.Request) (featuresapi.Gate, bool) {
			return featuresapi.Gate{Feature: feature}, true
		})(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
			w.WriteHeader(nethttp.StatusNoContent)
		}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(member, nethttp.MethodGet, "/", nil))
		return rec.Code
	}
	if got := status("scans"); got != nethttp.StatusForbidden {
		t.Errorf("a switched-off feature: %d", got)
	}
	for _, c := range []struct {
		org     kernel.OrganizationID
		feature string
		want    bool
	}{
		{org, "stacks", true},
		{org, "policies", false},
		{org, "scans", false},
		{kernel.OrganizationID{}, "policies", true},
		{kernel.OrganizationID{}, "scans", false},
	} {
		if ok, err := d.Allowed(ctx, c.org, c.feature); err != nil || ok != c.want {
			t.Errorf("Allowed(%v, %s) = %t, %v", c.org, c.feature, ok, err)
		}
	}
	if got := status("stacks"); got != nethttp.StatusNoContent {
		t.Errorf("a feature Free holds: %d", got)
	}
	if got := status("policies"); got != nethttp.StatusForbidden {
		t.Errorf("a feature Free lacks: %d", got)
	}

	// The operator gives Free policies; the snapshot holds until the watch
	// sees the organization's payload.
	if _, err := db.Exec(
		ctx,
		`INSERT INTO tier_features (tier, feature) VALUES ('Free', 'policies')`,
	); err != nil {
		t.Fatal(err)
	}
	if got := status("policies"); got != nethttp.StatusForbidden {
		t.Errorf("before the invalidation: %d", got)
	}
	ch <- notify.Message{Payload: "access:sessions"}
	ch <- notify.Message{Payload: entitlement.InvalidateKey(org)}
	ch <- notify.Message{Payload: "access:sessions"}
	if got := status("policies"); got != nethttp.StatusNoContent {
		t.Errorf("after the invalidation: %d", got)
	}
	if _, err := db.Exec(
		ctx,
		`DELETE FROM tier_features WHERE tier = 'Free' AND feature = 'policies'`,
	); err != nil {
		t.Fatal(err)
	}
	ch <- notify.Message{Resync: true}
	ch <- notify.Message{Payload: "access:sessions"}
	if got := status("policies"); got != nethttp.StatusForbidden {
		t.Errorf("after a resync: %d", got)
	}
	stop()
	<-done
}

func TestUnitFeaturesConfig(t *testing.T) {
	if err := (Settings{Disabled: []string{"scans", "policies"}}).Validate(); err != nil {
		t.Errorf("valid names: %v", err)
	}
	for _, c := range []struct {
		disabled []string
		want     string
	}{
		{[]string{"SCANS"}, "which the catalogue lacks"},
		{[]string{features.Billing}, "which the installation holds"},
		{[]string{features.Operator}, "which the installation holds"},
		{[]string{"seats"}, "a limit, not a feature"},
		{[]string{"scans", "scans"}, "twice"},
	} {
		err := Settings{Disabled: c.disabled}.Validate()
		if !errors.Is(err, ErrConfig) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v, want %q", c.disabled, err, c.want)
		}
	}
}
