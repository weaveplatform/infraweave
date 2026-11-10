package app

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/kernel"
	opapi "github.com/weaveplatform/infraweave/internal/platform/gen/operator"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
)

// TestAccPurges checks the purges as the server wires them: deleting an
// organization removes access' rows at once, and the outbox's workers then
// run the features, stacks and configuration purges, which remove the
// organization's entitlement, its accounts' limits, their stacks, and
// their contexts with their elements and attachments and the stacks' own
// elements, and leave another organization's. An account's own purge
// removes its limits alone.
func TestAccPurges(t *testing.T) {
	ctx := t.Context()
	db := sqltest.Open(t)
	q := db.Primary()
	events := testEvents(t, q)
	d, err := newDomains(config.Defaults(), db, testKeyring(t, q), nil, events)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Features.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := newDomains(config.Defaults(), db, testKeyring(t, q), nil, events); err == nil {
		t.Error("the purges subscribed twice to one outbox")
	}
	op := kernel.WithPrincipal(ctx, kernel.Principal{Kind: kernel.PrincipalOperator})
	type tenant struct {
		org  kernel.OrganizationID
		acct kernel.AccountID
	}
	create := func(slug string) tenant {
		if _, err := d.Access.OperatorAPI().
			OrganizationCreate(op, opapi.OrganizationCreateRequestObject{
				Body: &opapi.OrganizationCreateInput{
					Slug: slug, Name: slug, Account: opapi.AccountInput{Slug: slug, Name: slug},
				},
			}); err != nil {
			t.Fatal(err)
		}
		var x tenant
		if err := q.QueryRow(ctx, `SELECT organization_id, id FROM accounts WHERE slug = $1`,
			slug).Scan(&x.org, &x.acct); err != nil {
			t.Fatal(err)
		}
		limit := d.Features.Entitlements().LimitNames()[0]
		if err := d.Features.Entitlements().
			SetAccountLimit(ctx, x.org, x.acct, limit, 1); err != nil {
			t.Fatal(err)
		}
		for i := range 3 {
			if _, err := q.Exec(
				ctx,
				`INSERT INTO stacks (account_id, id, slug, space_id, name,
				repository, branch, provider, vendor)
				VALUES ($1, $2, $3, $4, 'Infra', 'infra', 'main', 'GITHUB', 'terraform')`,
				x.acct,
				kernel.NewULID(),
				fmt.Sprintf("infra-%d", i),
				kernel.NewULID(),
			); err != nil {
				t.Fatal(err)
			}
		}
		shared, stack := kernel.NewULID(), kernel.NewULID()
		for _, stmt := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO contexts (account_id, id, slug, space_id, name)
				VALUES ($1, $2, 'shared', $3, 'Shared')`, []any{shared, kernel.NewULID()}},
			{
				`INSERT INTO config_elements (account_id, id, context_id, stack_id, element,
				file_mount, value_sealed, checksum, write_only)
				VALUES ($1, $2, $3, NULL, 'A', false, '\x00', '', false),
					($1, $4, NULL, $5, 'B', false, '\x00', '', false)`,
				[]any{kernel.NewULID(), shared, kernel.NewULID(), stack},
			},
			{`INSERT INTO context_attachments (account_id, id, context_id, stack_id)
				VALUES ($1, $2, $3, $4)`, []any{kernel.NewULID(), shared, stack}},
		} {
			if _, err := q.Exec(ctx, stmt.sql, append([]any{x.acct}, stmt.args...)...); err != nil {
				t.Fatal(err)
			}
		}
		return x
	}
	gone, kept := create("gone"), create("kept")
	count := func(query string, args ...any) int {
		var n int
		if err := q.QueryRow(ctx, query, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return n
	}
	held := func(x tenant) int {
		return count(`SELECT
			(SELECT count(*) FROM stacks WHERE account_id = $2)
			+ (SELECT count(*) FROM account_limits WHERE account_id = $2)
			+ (SELECT count(*) FROM organization_entitlements WHERE organization_id = $1)
			+ (SELECT count(*) FROM contexts WHERE account_id = $2)
			+ (SELECT count(*) FROM config_elements WHERE account_id = $2)
			+ (SELECT count(*) FROM context_attachments WHERE account_id = $2)`,
			x.org, x.acct)
	}

	if _, err := d.Access.OperatorAPI().OrganizationDelete(op,
		opapi.OrganizationDeleteRequestObject{Organization: "gone"}); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM accounts WHERE id = $1`, gone.acct); n != 0 {
		t.Errorf("the deleted organization's account is still there")
	}
	if n := held(gone); n != 9 {
		t.Fatalf("before the purges: %d rows of the deleted organization, want 9", n)
	}

	reg := queue.NewRegistry()
	if err := events.Register(reg); err != nil {
		t.Fatal(err)
	}
	cfg := queue.Defaults()
	cfg.PollInterval = 20 * time.Millisecond
	w := queue.NewWorkers(queue.New(q), reg, cfg, queue.AllowAll{}, nil, discard(), "test")
	running, stop := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { w.Run(running) })
	t.Cleanup(func() {
		stop()
		wg.Wait()
	})
	deadline := time.After(15 * time.Second)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for held(gone) != 0 {
		select {
		case <-deadline:
			t.Fatalf("after the purges: %d rows of the deleted organization", held(gone))
		case <-tick.C:
		}
	}
	if n := held(kept); n != 9 {
		t.Errorf("another organization keeps %d rows, want 9", n)
	}

	if err := d.Features.Purge(ctx, kept.org, kept.acct, false); err != nil {
		t.Fatal(err)
	}
	if n := held(kept); n != 8 {
		t.Errorf("after an account's own features purge, %d rows, want 8", n)
	}
}

// TestUnitPurgeHandler checks that a document that does not decode never
// reaches the purge and kills its job at once.
func TestUnitPurgeHandler(t *testing.T) {
	ran := false
	h := purge(func(context.Context, accessapi.Deleted) error {
		ran = true
		return nil
	})
	err := h(t.Context(), outbox.Event{Kind: accessapi.DeletedKind, Document: []byte(`[]`)},
		queue.Job{})
	if err == nil || ran {
		t.Errorf("a bad document = %v, ran %t", err, ran)
	}
	if err := h(t.Context(), outbox.Event{Document: []byte(`{}`)}, queue.Job{}); err != nil ||
		!ran {
		t.Errorf("a document = %v, ran %t", err, ran)
	}
}
