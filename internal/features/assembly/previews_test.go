package assembly

import (
	"context"
	"errors"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/features"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// The test catalogue's previews: a capability and an option of the
// compiled catalogue, marked as the register marks none.
const (
	previewCapability = "drift-detection"
	previewOption     = "vendor-opentofu"
)

// testPreviews marks the test catalogue's previews, in catalogue order.
func testPreviews() []features.Feature {
	names := []string{previewCapability, previewOption}
	out := make([]features.Feature, 0, len(names))
	for _, name := range names {
		f, _ := features.Lookup(name)
		f.Preview = &features.Preview{
			Name: name, Description: "In preview.", DocsURL: "https://docs.example/" + name,
		}
		out = append(out, f)
	}
	return out
}

// eventually waits for cond, failing t after a bound no healthy
// notification needs.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.After(15 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for !cond() {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		case <-tick.C:
		}
	}
}

// replicas runs n domains on db over the test catalogue, as n serve
// replicas run, each with its own listener whose infraweave_invalidate
// subscription drops its snapshots; it returns once every listener
// listens.
func replicas(t *testing.T, db *pgxpool.Pool, cc notify.Config, n int) []*Domain {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})
	out := make([]*Domain, n)
	for i := range out {
		d := build(Deps{DB: db}, testPreviews)
		l := notify.NewListener(cc, slog.New(slog.DiscardHandler))
		sub, err := l.Subscribe(notify.Invalidate)
		if err != nil {
			t.Fatal(err)
		}
		wg.Go(func() { l.Run(ctx) })
		wg.Go(func() { d.WatchEntitlements(ctx, sub) })
		eventually(t, "the listener", l.Listening)
		out[i] = d
	}
	return out
}

// status is the code d's middleware answers p's request for feature with.
func status(ctx context.Context, d *Domain, p kernel.Principal, feature string) int {
	h := d.Middleware(func(*nethttp.Request) (featuresapi.Gate, bool) {
		return featuresapi.Gate{Feature: feature}, true
	})(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(
		kernel.WithPrincipal(ctx, p), nethttp.MethodGet, "/", nil))
	return rec.Code
}

// TestAccPreviewSettings checks the feature preview settings on
// PostgreSQL across two replicas over a test catalogue: an opt-in written
// through one applies on the other at its next request, a member's
// setting round-trips, a member's removal leaves no row and rolls back
// with its transaction, and the account's purge, alone or with its
// organization, leaves none of its rows.
//
// ref: decision 0021
func TestAccPreviewSettings(t *testing.T) {
	ctx := t.Context()
	db := sqltest.Open(t)
	rs := replicas(t, db.Primary(), db.ConnConfig, 2)
	a, b := rs[0], rs[1]
	if err := a.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	org, acct := kernel.NewOrganizationID(), kernel.NewAccountID()
	alice, bob := kernel.NewMemberID(), kernel.NewMemberID()
	if err := a.Entitlements().CreateOrganization(ctx, org); err != nil {
		t.Fatal(err)
	}
	p := kernel.Principal{OrganizationID: org, AccountID: acct, MemberID: alice}

	// Each replica refuses the preview and keeps the account's opt-in.
	for _, d := range rs {
		if got := status(ctx, d, p, previewCapability); got != nethttp.StatusForbidden {
			t.Fatalf("before opting in: %d", got)
		}
		if got := status(ctx, d, p, "stacks"); got != nethttp.StatusNoContent {
			t.Errorf("a generally available feature: %d", got)
		}
	}
	if err := a.Entitlements().SetFeaturePreviewsEnabled(ctx, acct, true); err != nil {
		t.Fatal(err)
	}
	for i, d := range rs {
		eventually(t, "the opt-in on each replica", func() bool {
			return status(ctx, d, p, previewCapability) == nethttp.StatusNoContent
		})
		if on, err := d.Entitlements().FeaturePreviewsEnabled(ctx, acct); err != nil || !on {
			t.Errorf("replica %d reads %t, %v", i, on, err)
		}
		if ok, err := d.Allowed(ctx, org, acct, previewOption); err != nil || !ok {
			t.Errorf("replica %d's job check: %t, %v", i, ok, err)
		}
	}
	if err := b.Entitlements().SetFeaturePreviewsEnabled(ctx, acct, false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the opt-out on the other replica", func() bool {
		return status(ctx, a, p, previewOption) == nethttp.StatusForbidden
	})

	// A member's personal setting round-trips and refuses nothing.
	if err := a.Entitlements().SetFeaturePreviewsEnabled(ctx, acct, true); err != nil {
		t.Fatal(err)
	}
	for _, m := range []kernel.MemberID{alice, bob} {
		if err := a.Entitlements().SetPersonalFeaturePreviewsEnabled(ctx, acct, m, false); err != nil {
			t.Fatal(err)
		}
		if on, err := b.Entitlements().PersonalFeaturePreviewsEnabled(ctx, acct, m); err != nil || on {
			t.Errorf("an opted-out member reads %t, %v", on, err)
		}
	}
	eventually(t, "the opt-in on the other replica", func() bool {
		return status(ctx, b, p, previewCapability) == nethttp.StatusNoContent
	})

	count := func(table string, acct kernel.AccountID) int {
		t.Helper()
		var n int
		if err := db.Primary().QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE account_id = $1`,
			acct).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// A removal rolled back with access' transaction keeps the setting.
	errRollback := errors.New("rolled back")
	if err := tx.Do(ctx, db.Primary(), tx.Options{}, func(ctx context.Context) error {
		if err := a.Entitlements().RemoveMember(ctx, acct, alice); err != nil {
			t.Fatal(err)
		}
		return errRollback
	}); !errors.Is(err, errRollback) {
		t.Fatal(err)
	}
	if n := count("member_feature_preview_opt_outs", acct); n != 2 {
		t.Errorf("after a rolled-back removal, %d opt-outs, want 2", n)
	}
	if err := tx.Do(ctx, db.Primary(), tx.Options{}, func(ctx context.Context) error {
		return a.Entitlements().RemoveMember(ctx, acct, alice)
	}); err != nil {
		t.Fatal(err)
	}
	if on, err := b.Entitlements().PersonalFeaturePreviewsEnabled(ctx, acct, alice); err != nil ||
		!on || count("member_feature_preview_opt_outs", acct) != 1 {
		t.Errorf("a removed member reads %t, %v, with %d opt-outs left",
			on, err, count("member_feature_preview_opt_outs", acct))
	}

	// The account's purge leaves none of its rows, and its replicas
	// refuse the preview again.
	if err := a.Purge(ctx, org, acct, false); err != nil {
		t.Fatal(err)
	}
	if o, m := count("account_feature_previews", acct),
		count("member_feature_preview_opt_outs", acct); o+m != 0 {
		t.Errorf("a purged account leaves %d opt-ins and %d opt-outs", o, m)
	}
	eventually(t, "the purge on the other replica", func() bool {
		return status(ctx, b, p, previewCapability) == nethttp.StatusForbidden
	})

	// So does the purge of an account deleted with its organization.
	gone := kernel.NewAccountID()
	if err := a.Entitlements().SetFeaturePreviewsEnabled(ctx, gone, true); err != nil {
		t.Fatal(err)
	}
	if err := a.Entitlements().SetPersonalFeaturePreviewsEnabled(ctx, gone, bob, false); err != nil {
		t.Fatal(err)
	}
	if err := b.Purge(ctx, org, gone, true); err != nil {
		t.Fatal(err)
	}
	var entitlements int
	if err := db.Primary().QueryRow(ctx,
		`SELECT count(*) FROM organization_entitlements WHERE organization_id = $1`, org).
		Scan(&entitlements); err != nil {
		t.Fatal(err)
	}
	if o, m := count("account_feature_previews", gone),
		count("member_feature_preview_opt_outs", gone); o+m+entitlements != 0 {
		t.Errorf("the organization's purge leaves %d opt-ins, %d opt-outs, %d entitlements",
			o, m, entitlements)
	}
}

// TestAccPreviewGateBilling checks the gate's order on PostgreSQL with
// billing on: the tier refusal before the preview's, and the switch-off
// before both.
func TestAccPreviewGateBilling(t *testing.T) {
	ctx := t.Context()
	db := sqltest.Open(t)
	d := build(Deps{DB: db.Primary(), Settings: Settings{Disabled: []string{"scans"}}},
		func() []features.Feature {
			return append(testPreviews(), features.Feature{
				Name: "scans", Kind: features.Capability, Preview: &features.Preview{Name: "scans"},
			})
		})
	if err := d.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Primary().Exec(ctx,
		`UPDATE billing SET enabled = true, default_tier = $1`, features.Free); err != nil {
		t.Fatal(err)
	}
	org, acct := kernel.NewOrganizationID(), kernel.NewAccountID()
	if err := d.Entitlements().CreateOrganization(ctx, org); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		feature string
		want    featuresapi.Refusal
	}{
		{"scans", featuresapi.Refusal{Disabled: true, Feature: "scans"}},
		{previewCapability, featuresapi.Refusal{
			Feature: previewCapability, Tiers: []string{features.Business, features.Enterprise},
		}},
		{previewOption, featuresapi.Refusal{Preview: true, Feature: previewOption}},
	} {
		err := d.Entitlements().AllowAccount(ctx, org, acct, c.feature)
		got, ok := errors.AsType[*featuresapi.Refusal](err)
		if !ok || got.Disabled != c.want.Disabled || got.Preview != c.want.Preview ||
			got.Feature != c.want.Feature || len(got.Tiers) != len(c.want.Tiers) {
			t.Errorf("%s: %v; want %+v", c.feature, err, c.want)
		}
	}
}

// accountJobs is queue.Entitlements for one organization's accounts, as
// internal/app resolves a job's account to its organization.
type accountJobs struct {
	d   *Domain
	org kernel.OrganizationID
}

func (j accountJobs) Allowed(ctx context.Context, account kernel.ULID, feature string) (bool, error) {
	return j.d.Allowed(ctx, j.org, kernel.AccountID{ULID: account}, feature)
}

// TestAccPreviewJobSkipped checks the dequeue check over the test
// catalogue: a queued job of a preview in an account that has not opted
// in completes as skipped on its first attempt, counted in
// infraweave.features.skipped, and runs once the account opts in.
//
// ref: decision 0011
func TestAccPreviewJobSkipped(t *testing.T) {
	ctx := t.Context()
	db := sqltest.Open(t)
	d := replicas(t, db.Primary(), db.ConnConfig, 1)[0]
	org, acct := kernel.NewOrganizationID(), kernel.NewAccountID()
	if err := d.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	if err := d.Entitlements().CreateOrganization(ctx, org); err != nil {
		t.Fatal(err)
	}
	reg := queue.NewRegistry()
	kind := queue.Kind{Name: "t.drift", Queue: queue.Default, Policy: queue.Ignore, MaxAttempts: 5}
	var ran atomic.Int32
	if err := reg.Register(kind, func(context.Context, queue.Job) error {
		ran.Add(1)
		return nil
	}, queue.HandlerOptions{Feature: previewCapability}); err != nil {
		t.Fatal(err)
	}
	cfg := queue.Defaults()
	cfg.PollInterval = 20 * time.Millisecond
	q := queue.New(db.Primary())
	w := queue.NewWorkers(q, reg, cfg, accountJobs{d, org}, nil,
		slog.New(slog.DiscardHandler), "test")
	running, stop := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { w.Run(running) })
	t.Cleanup(func() {
		stop()
		wg.Wait()
	})
	finished := func(id int64) (state string, attempt int) {
		t.Helper()
		eventually(t, "the job", func() bool {
			if err := db.Primary().QueryRow(ctx, `SELECT state, attempt FROM jobs WHERE id = $1`, id).
				Scan(&state, &attempt); err != nil {
				t.Fatal(err)
			}
			return state != "pending" && state != "running"
		})
		return state, attempt
	}

	skipped, err := q.Enqueue(ctx, kind, nil, queue.Account(acct.ULID))
	if err != nil {
		t.Fatal(err)
	}
	if state, attempt := finished(skipped); state != "succeeded" || attempt != 1 ||
		ran.Load() != 0 || w.Skipped() != 1 {
		t.Errorf("a preview's job before opting in: %s on attempt %d, ran %d, skipped %d",
			state, attempt, ran.Load(), w.Skipped())
	}

	if err := d.Entitlements().SetFeaturePreviewsEnabled(ctx, acct, true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the opt-in", func() bool {
		ok, err := d.Allowed(ctx, org, acct, previewCapability)
		return err == nil && ok
	})
	ran2, err := q.Enqueue(ctx, kind, nil, queue.Account(acct.ULID))
	if err != nil {
		t.Fatal(err)
	}
	if state, _ := finished(ran2); state != "succeeded" || ran.Load() != 1 || w.Skipped() != 1 {
		t.Errorf("a preview's job after opting in: %s, ran %d, skipped %d",
			state, ran.Load(), w.Skipped())
	}
}
