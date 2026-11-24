package pg_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/cloud/internal/core"
	"github.com/weaveplatform/infraweave/internal/cloud/internal/pg"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
)

// TestAccStoreUniqueKeys checks that the unique keys refuse a second name
// in an account, a second read or write attachment of a stack and a second
// attachment of one integration to one stack, each as a refusal.
func TestAccStoreUniqueKeys(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	a := f.create(t, "a", "", core.Input{})
	b := f.create(t, "b", "", core.Input{})
	b.Name = "a"
	if err := f.store.Write(ctx, b); !isTaken(err) {
		t.Fatalf("err = %v, want the name taken", err)
	}
	b.ID = kernel.NewULID()
	if err := f.store.Write(ctx, b); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, want not found", err)
	}
	attachment := func(i core.Integration, read, write bool) core.Attachment {
		return core.Attachment{
			Account: f.acct, ID: kernel.NewULID(), Integration: i.ID, Stack: f.web.ID,
			Read: read, Write: write, CreatedAt: f.now,
		}
	}
	first := attachment(a, true, false)
	if err := f.store.Attach(ctx, first); err != nil {
		t.Fatal(err)
	}
	for _, at := range []core.Attachment{
		attachment(a, false, true), attachment(f.create(t, "c", "", core.Input{}), true, false),
	} {
		if err := f.store.Attach(ctx, at); !isConflict(err) {
			t.Fatalf("err = %v, want a conflict", err)
		}
	}
	second := attachment(f.create(t, "d", "", core.Input{}), false, true)
	if err := f.store.Attach(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetSides(ctx, f.acct, first.ID, true, true); !isConflict(err) {
		t.Fatalf("err = %v, want a conflict", err)
	}
	if err := f.store.SetSides(ctx, f.acct, kernel.NewULID(), true, true); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, want not found", err)
	}
	orphan := attachment(core.Integration{ID: kernel.NewULID()}, true, true)
	orphan.Stack = kernel.NewULID()
	if err := f.store.Attach(ctx, orphan); err == nil || isConflict(err) {
		t.Fatalf("err = %v, want a failed write naming no integration", err)
	}
}

// TestAccHeld checks that the held columns name the cloud tables'
// columns of other domains' rows.
func TestAccHeld(t *testing.T) {
	db := pool(t)
	for _, h := range pg.Held() {
		var n int
		cols := slices.Clone(h.Columns)
		q := `SELECT count(*) FROM information_schema.columns WHERE table_name = $1
			AND column_name = ANY($2)`
		if err := db.QueryRow(t.Context(), q, h.Table, cols).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != len(cols) {
			t.Fatalf("%s %v: %d columns exist", h.Table, cols, n)
		}
	}
	if !slices.ContainsFunc(pg.Held(), func(h integrity.Held) bool { return h.Entity == "stack" }) {
		t.Fatal("no column holds a stack")
	}
}

// TestAccPurge purges a deleted account's attachments, then its
// integrations, a batch at a time; another account's rows stay.
func TestAccPurge(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	a := f.create(t, "a", "team", core.Input{})
	b := f.create(t, "b", "team", core.Input{})
	f.attach(t, a, "web", true, false)
	f.attach(t, b, "web", false, true)
	kept := kernel.NewAccountID()
	if err := f.store.Create(ctx, core.Integration{
		Account: kept, ID: kernel.NewULID(), Space: kept.ULID, Name: "kept", RoleARN: role("k"),
		Duration: core.DefaultDuration, Labels: []string{}, CreatedAt: f.now, UpdatedAt: f.now, Version: 1,
	}); err != nil {
		t.Fatal(err)
	}
	rows := func(acct kernel.AccountID) int {
		var n int
		if err := f.db.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM aws_integration_attachments WHERE account_id = $1)
			+ (SELECT count(*) FROM aws_integrations WHERE account_id = $1)`, acct).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, want := range []struct {
		done bool
		left int
	}{{false, 3}, {false, 2}, {false, 1}, {false, 0}, {true, 0}} {
		if done, err := f.store.Purge(ctx, f.acct, 1); err != nil || done != want.done ||
			rows(f.acct) != want.left {
			t.Fatalf("a batch: done %t, %v, %d rows left, want %+v", done, err, rows(f.acct), want)
		}
	}
	if rows(kept) != 1 {
		t.Fatal("the purge deleted another account's integration")
	}
}

// TestAccStoreUnavailable checks that every read and write on a closed
// pool fails, and not as a missing row or a refusal.
func TestAccStoreUnavailable(t *testing.T) {
	db := pool(t)
	s := pg.New(db)
	db.Close()
	ctx := context.Background()
	acct, id := kernel.NewAccountID(), kernel.NewULID()
	ids := []kernel.ULID{id}
	i := core.Integration{Account: acct, ID: id, Name: "x"}
	at := core.Attachment{Account: acct, ID: id, Integration: id, Stack: id, Read: true}
	for name, err := range map[string]error{
		"Create":     s.Create(ctx, i),
		"Write":      s.Write(ctx, i),
		"Get":        func() error { _, err := s.Get(ctx, acct, id, core.Update); return err }(),
		"ByName":     func() error { _, err := s.ByName(ctx, acct, "x"); return err }(),
		"Legacy":     func() error { _, err := s.Legacy(ctx, acct, id); return err }(),
		"ByIDs":      func() error { _, err := s.ByIDs(ctx, acct, ids); return err }(),
		"List":       func() error { _, _, err := s.List(ctx, acct, ids, true, core.Page{Limit: 1}); return err }(),
		"Labelled":   func() error { _, err := s.Labelled(ctx, acct, ids, []string{"autoattach:*"}); return err }(),
		"InSpace":    func() error { _, err := s.InSpace(ctx, acct, id); return err }(),
		"Delete":     s.Delete(ctx, acct, id),
		"Attach":     s.Attach(ctx, at),
		"SetSides":   s.SetSides(ctx, acct, id, true, true),
		"Attachment": func() error { _, err := s.Attachment(ctx, acct, id); return err }(),
		"Detach":     s.Detach(ctx, acct, id),
		"Attachments": func() error {
			_, err := s.Attachments(ctx, acct, ids, true)
			return err
		}(),
		"AttachedStacks": func() error { _, err := s.AttachedStacks(ctx, acct); return err }(),
		"DeleteStack":    s.DeleteStack(ctx, acct, id),
		"Purge":          func() error { _, err := s.Purge(ctx, acct, 1); return err }(),
		"Audit":          s.Audit(ctx, kernel.AuditEntry{}),
	} {
		if err == nil || errors.Is(err, core.ErrNotFound) || isConflict(err) || isTaken(err) {
			t.Errorf("%s on a closed pool = %v", name, err)
		}
	}
}

// TestAccServiceUnavailable checks that the operations fail when the
// database is down, none as a missing entity, and that those reading the
// spaces or the stacks fail when those are.
func TestAccServiceUnavailable(t *testing.T) {
	f := newFixture(t)
	i := f.create(t, "prod", "team", core.Input{})
	a := f.attach(t, i, "web", true, false)
	ctx := t.Context()
	p := f.admin
	in := core.Input{Name: "prod", Space: "team", RoleARN: role("prod")}
	ops := map[string]func() error{
		"ByName": func() error { _, err := f.svc.ByName(ctx, p, "prod"); return err },
		"List":   func() error { _, _, err := f.svc.List(ctx, p, false, core.Page{Limit: 1}); return err },
		"Attachable": func() error {
			_, _, err := f.svc.Attachable(ctx, p, "team", core.Page{Limit: 1})
			return err
		},
		"Update": func() error {
			_, err := f.svc.Update(ctx, p, i.ID.String(), kernel.Expect{}, in)
			return err
		},
		"Move": func() error {
			moved := in
			moved.Space = "app"
			_, err := f.svc.Update(ctx, p, i.ID.String(), kernel.Expect{}, moved)
			return err
		},
		"Delete": func() error {
			_, err := f.svc.Delete(ctx, p, i.ID.String(), kernel.Expect{})
			return err
		},
		"Attach": func() error {
			_, _, err := f.svc.Attach(ctx, p, "web", i.ID.String(), false, true)
			return err
		},
		"UpdateAttachment": func() error {
			_, _, err := f.svc.UpdateAttachment(ctx, p, a.ID.String(), true, true)
			return err
		},
		"Detach": func() error { _, _, err := f.svc.Detach(ctx, p, a.ID.String()); return err },
		"SetLegacy": func() error {
			_, _, err := f.svc.SetLegacy(ctx, p, "web", core.LegacyInput{RoleARN: role("l")})
			return err
		},
		"DeleteLegacy": func() error { _, _, err := f.svc.DeleteLegacy(ctx, p, "web"); return err },
		"AttachmentSpaceOf": func() error {
			_, err := f.svc.AttachmentSpaceOf(ctx, f.acct, a.ID.String())
			return err
		},
		"SpaceInUse": func() error {
			_, err := f.svc.SpaceGuard().SpaceInUse(ctx, f.acct, f.sp["team"])
			return err
		},
		"Deleting": func() error { return f.svc.StackGuard().Deleting(ctx, f.web) },
	}
	readers := map[string][]string{
		"spaces": {"Attachable", "Move", "Attach"},
		"stacks": {
			"Attach", "UpdateAttachment", "Detach", "SetLegacy", "DeleteLegacy",
			"AttachmentSpaceOf",
		},
	}
	for _, broken := range []string{"spaces", "stacks"} {
		f.spaces.err, f.stacks.err = nil, nil
		if broken == "spaces" {
			f.spaces.err = errBroken
		} else {
			f.stacks.err = errBroken
		}
		for _, name := range readers[broken] {
			if err := ops[name](); !errors.Is(err, errBroken) {
				t.Errorf("%s with the %s down = %v", name, broken, err)
			}
		}
	}
	f.spaces.err, f.stacks.err = nil, nil
	f.db.Close()
	for name, op := range ops {
		if err := op(); err == nil || errors.Is(err, core.ErrNotFound) || isMissing(err) {
			t.Errorf("%s with the database down = %v", name, err)
		}
	}
}
