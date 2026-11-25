package pg_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/cloud/internal/core"
	"github.com/weaveplatform/infraweave/internal/cloud/internal/pg"
	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// TestAccStackAttachments reads the stack view's AWS integration
// attachments: web, in app, holds a stored read attachment of deploy, in
// team, and a computed write attachment of the worker's integration
// labelled for its prod label, in acme; db, in hidden, holds its legacy
// AWS role. Each shows the external ID its role is assumed with, and a
// caller who may read team alone sees deploy alone.
func TestAccStackAttachments(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.stacks.labels["web"] = []string{"prod"}
	deploy := f.create(t, "deploy", "team", core.Input{})
	direct := f.attach(t, deploy, "web", true, false)
	worker := f.create(t, "worker", "", core.Input{
		Worker: true, ExternalID: "ours", Autoattach: true, Labels: []string{"autoattach_write:prod"},
	})
	legacy, _, err := f.svc.SetLegacy(ctx, f.admin, "db", core.LegacyInput{RoleARN: role("legacy")})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := f.store.Attachments(ctx, f.acct, []kernel.ULID{f.data.ID}, true)
	if err != nil || len(bound[f.data.ID]) != 1 {
		t.Fatalf("legacy attachments = %+v, %v", bound, err)
	}

	got, err := f.svc.AwsAttachments(ctx, f.admin, []kernel.ULID{f.web.ID, f.data.ID, kernel.NewULID()})
	if err != nil {
		t.Fatal(err)
	}
	want := map[kernel.ULID][]stacksapi.AwsAttachment{
		f.web.ID: {
			{
				ID: direct.ID.String(), Integration: deploy.ID, Name: "deploy", RoleARN: role("deploy"),
				ExternalID: "acme@" + deploy.ID.String() + "@web@read", Duration: core.DefaultDuration,
				Labels: []string{}, Read: true,
			},
			{
				ID: core.AutoID(worker.ID, f.web.ID), Integration: worker.ID, Name: "worker",
				RoleARN: role("worker"), ExternalID: "ours", Duration: core.DefaultDuration,
				Labels: []string{"autoattach_write:prod"}, Worker: true, Write: true, Autoattached: true,
			},
		},
		f.data.ID: {{
			ID: bound[f.data.ID][0].ID.String(), Integration: legacy.ID, Name: legacy.Name, RoleARN: role("legacy"),
			ExternalID: "acme@db", Duration: core.DefaultDuration, Labels: []string{}, Legacy: true,
			Read: true, Write: true,
		}},
	}
	for id, as := range want {
		if !reflect.DeepEqual(got[id], as) {
			t.Errorf("attachments of %s = %+v, want %+v", id, got[id], as)
		}
	}
	if len(got) != len(want) {
		t.Errorf("attachments = %+v; want an unknown stack left out", got)
	}

	seen, err := f.svc.AwsAttachments(ctx, f.reader, []kernel.ULID{f.web.ID, f.data.ID})
	if err != nil || len(seen) != 1 || len(seen[f.web.ID]) != 1 || seen[f.web.ID][0].Integration != deploy.ID {
		t.Fatalf("a team reader's attachments = %+v, %v", seen, err)
	}
	if none, err := f.svc.AwsAttachments(ctx, f.admin, nil); err != nil || len(none) != 0 {
		t.Fatalf("no stacks' attachments = %+v, %v", none, err)
	}
	bare := stacksapi.Stack{ID: kernel.NewULID(), Account: f.acct, Slug: "bare", Name: "Bare", Space: f.sp["hidden"]}
	f.stacks.all["bare"] = bare
	if none, err := f.svc.AwsAttachments(ctx, f.admin, []kernel.ULID{bare.ID}); err != nil || len(none) != 0 {
		t.Fatalf("an unattached stack's attachments = %+v, %v", none, err)
	}
	nodes, err := f.spaces.Tree(ctx, f.acct)
	if err != nil {
		t.Fatal(err)
	}
	tree := core.Tree{}
	for _, n := range nodes {
		tree[n.ID] = n
	}
	one, err := f.svc.Effective(ctx, f.acct, f.web, []string{"prod"}, tree)
	if err != nil || len(one) != 2 || one[0].ID != direct.ID || one[1].Integration != worker.ID || !one[1].Auto {
		t.Fatalf("web's effective attachments = %+v, %v", one, err)
	}

	f.accountsBroken(t)
	if _, err := f.svc.AwsAttachments(ctx, f.admin, []kernel.ULID{f.web.ID}); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v, want the account slug's failure", err)
	}
	f.svc = core.New(core.Deps{
		Store: f.store, Azure: pg.NewAzure(f.db), DB: f.db, Spaces: f.spaces, Accounts: accounts{}, Stacks: f.stacks,
		Authz: brokenReach{f.authzOf()}, Roles: f.roles,
	})
	if _, err := f.svc.AwsAttachments(ctx, f.admin, []kernel.ULID{f.web.ID}); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v, want the readable spaces' failure", err)
	}
	f.stacks.err = errBroken
	if _, err := f.svc.AwsAttachments(ctx, f.admin, []kernel.ULID{f.web.ID}); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v, want the stacks' failure", err)
	}
}

// brokenReach fails every read of the spaces a caller may reach.
type brokenReach struct{ authz }

func (brokenReach) Reachable(context.Context, kernel.Principal, accessapi.Action) ([]kernel.ULID, error) {
	return nil, errBroken
}

// queries counts the statements a pool runs.
type queries struct{ n atomic.Int64 }

func (q *queries) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	q.n.Add(1)
	return ctx
}

func (*queries) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// TestAccStackAttachmentsQueries checks that reading the attachments of a
// page of 50 stacks, each with a stored attachment and a computed one,
// runs as many statements as reading those of one stack.
func TestAccStackAttachmentsQueries(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	ids := make([]kernel.ULID, 50)
	for n := range ids {
		slug := fmt.Sprintf("s%02d", n)
		st := stacksapi.Stack{ID: kernel.NewULID(), Account: f.acct, Slug: slug, Name: slug, Space: f.sp["app"]}
		f.stacks.all[slug], f.stacks.labels[slug] = st, []string{"prod"}
		ids[n] = st.ID
	}
	deploy := f.create(t, "deploy", "team", core.Input{})
	for n := range ids {
		f.attach(t, deploy, fmt.Sprintf("s%02d", n), true, false)
	}
	f.create(t, "worker", "", core.Input{
		Worker: true, Autoattach: true, Labels: []string{"autoattach_write:prod"},
	})

	cfg := f.db.Config()
	count := new(queries)
	cfg.ConnConfig.Tracer = count
	traced, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(traced.Close)
	svc := core.New(core.Deps{
		Store: pg.New(traced), Azure: pg.NewAzure(traced), DB: traced, Spaces: f.spaces, Accounts: accounts{}, Stacks: f.stacks,
		Authz: f.authzOf(), Roles: f.roles,
	})
	read := func(stacks []kernel.ULID) int64 {
		t.Helper()
		before := count.n.Load()
		got, err := svc.AwsAttachments(ctx, f.admin, stacks)
		if err != nil || len(got) != len(stacks) || len(got[stacks[0]]) != 2 {
			t.Fatalf("attachments = %+v, %v", got, err)
		}
		return count.n.Load() - before
	}
	one, page := read(ids[:1]), read(ids)
	if one != page || page > 3 {
		t.Fatalf("one stack ran %d statements and a page of %d ran %d; want the same, at most 3", one, len(ids), page)
	}
}
