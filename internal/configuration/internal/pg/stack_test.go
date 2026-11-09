package pg_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/configuration/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// TestAccStackElements sets, reads and deletes a stack's own elements,
// holding the stack: a replace keeps a value it is not sent, a write-only
// value is never read back, and the stack's view reads the elements.
func TestAccStackElements(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	set := func(in core.ElementInput) (core.Element, bool, error) {
		return f.svc.SetStackElement(ctx, f.writer, "web", in, "", "/configId")
	}
	e, created, err := set(core.ElementInput{Name: "TOKEN", Value: new("s3cret"), WriteOnly: true})
	if err != nil || !created || e.Value != nil || e.Checksum != sum("s3cret") ||
		e.Owner != (core.Owner{Stack: f.web.ID}) {
		t.Fatalf("created %+v, %v, %v", e, created, err)
	}
	if !slices.Contains(f.stacks.holds, f.web.ID) {
		t.Errorf("holds %v", f.stacks.holds)
	}
	desc := "rotated"
	again, created, err := set(
		core.ElementInput{Name: "TOKEN", WriteOnly: true, Description: &desc},
	)
	if err != nil || created || again.ID != e.ID || again.Checksum != e.Checksum ||
		again.Description != "rotated" {
		t.Errorf("kept %+v, %v, %v", again, created, err)
	}
	if _, _, err := set(
		core.ElementInput{Name: "TOKEN"},
	); !reflect.DeepEqual(
		pointers(t, err),
		[]string{"/value"},
	) {
		t.Errorf("making it readable without a value = %v", err)
	}
	if _, _, err := set(core.ElementInput{Name: "A=B", Value: new("")}); !reflect.DeepEqual(
		pointers(t, err), []string{"/configId"}) {
		t.Errorf("a bad name = %v", err)
	}
	if _, _, err := set(core.ElementInput{Name: "REGION", Value: new("eu")}); err != nil {
		t.Fatal(err)
	}
	if got, err := f.svc.StackElement(
		ctx,
		f.acct,
		"web",
		"TOKEN",
	); err != nil ||
		got.Value != nil ||
		got.Checksum != sum("s3cret") {
		t.Errorf("TOKEN = %+v, %v", got, err)
	}
	if got, err := f.svc.StackElement(
		ctx,
		f.acct,
		"web",
		"REGION",
	); err != nil ||
		*got.Value != "eu" {
		t.Errorf("REGION = %+v, %v", got, err)
	}
	config, err := f.svc.Config(ctx, f.acct, []kernel.ULID{f.web.ID})
	if err != nil || len(config[f.web.ID]) != 2 || config[f.web.ID][0].ID != "REGION" ||
		config[f.web.ID][1].Value != nil || config[f.web.ID][1].Checksum != sum("s3cret") {
		t.Errorf("the stack's view reads %+v, %v", config, err)
	}
	if _, err := f.svc.StackElement(ctx, f.acct, "web", "NOPE"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("a missing element = %v", err)
	}
	for name, err := range map[string]error{
		"StackElement": func() error { _, err := f.svc.StackElement(ctx, f.acct, "gone", "A"); return err }(),
		"SetStackElement": func() error {
			_, _, err := f.svc.SetStackElement(ctx, f.admin, "gone", variable("A", "1"), "", "/id")
			return err
		}(),
		"DeleteStackElement": func() error { _, err := f.svc.DeleteStackElement(ctx, f.admin, "gone", "A"); return err }(),
		"StackAttachable": func() error {
			_, err := f.svc.StackAttachable(ctx, f.admin, "gone", search.Input{})
			return err
		}(),
	} {
		if !missing(err, "stack") {
			t.Errorf("%s of a missing stack = %v", name, err)
		}
	}

	gone, err := f.svc.DeleteStackElement(ctx, f.writer, "web", "TOKEN")
	if err != nil || gone.ID != e.ID {
		t.Errorf("deleted %+v, %v", gone, err)
	}
	if _, err := f.svc.DeleteStackElement(
		ctx,
		f.writer,
		"web",
		"TOKEN",
	); !errors.Is(
		err,
		core.ErrNotFound,
	) {
		t.Errorf("deleting it again = %v", err)
	}
	if err := f.svc.StackGuard().Deleting(ctx, f.web); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.StackElement(
		ctx,
		f.acct,
		"web",
		"REGION",
	); !errors.Is(
		err,
		core.ErrNotFound,
	) {
		t.Errorf("REGION of a deleted stack = %v", err)
	}
}

// TestAccStackElementsConcurrent sets one new element of a stack from two
// writers, the second while the first's transaction is open: it waits
// for the first to commit and replaces what it created, rather than
// failing on the element's unique name.
func TestAccStackElementsConcurrent(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	second := make(chan error, 1)
	err := tx.Do(ctx, f.db, tx.Options{}, func(ctx context.Context) error {
		if _, created, err := f.svc.SetStackElement(
			ctx,
			f.admin,
			"web",
			variable("A", "1"),
			"",
			"/id",
		); err != nil ||
			!created {
			return fmt.Errorf("first: %v, %w", created, err)
		}
		go func() { //nolint:contextcheck // the second writer runs its own transaction, not the first's
			_, created, err := f.svc.SetStackElement(
				t.Context(),
				f.admin,
				"web",
				variable("A", "2"),
				"",
				"/id",
			)
			if err == nil && created {
				err = errors.New("second created it too")
			}
			second <- err
		}()
		for {
			var waiting bool
			if err := f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_locks l
				JOIN pg_stat_activity a ON a.pid = l.pid
				WHERE NOT l.granted AND a.datname = current_database())`).Scan(&waiting); err != nil || waiting {
				return err
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Errorf("second: %v", err)
	}
	if e, err := f.svc.StackElement(ctx, f.acct, "web", "A"); err != nil || *e.Value != "2" {
		t.Errorf("A = %+v, %v", e, err)
	}
}

// TestAccStackAttachable reads the contexts a stack may attach that the
// caller may read.
func TestAccStackAttachable(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	for _, c := range []struct{ name, space string }{
		{"Top", ""}, {"Team", "team"}, {"App", "app"}, {"Leaf", "leaf"}, {"Hidden", "hidden"},
	} {
		f.create(t, c.name, c.space)
	}
	slugs := func(r search.Result[core.Context]) []string {
		out := make([]string, 0, len(r.Edges))
		for _, e := range r.Edges {
			out = append(out, e.Node.Slug)
		}
		return out
	}
	res, err := f.svc.StackAttachable(ctx, f.admin, "web", search.Input{})
	if err != nil || !reflect.DeepEqual(slugs(res), []string{"app", "team", "top"}) {
		t.Errorf("the admin's = %v, %v", slugs(res), err)
	}
	res, err = f.svc.StackAttachable(ctx, f.reader, "web", search.Input{})
	if err != nil || !reflect.DeepEqual(slugs(res), []string{"app", "team"}) {
		t.Errorf("the reader's = %v, %v", slugs(res), err)
	}
	res, err = f.svc.StackAttachable(ctx, f.admin, "db", search.Input{})
	if err != nil || !reflect.DeepEqual(slugs(res), []string{"hidden"}) {
		t.Errorf("db's, in a space that does not inherit = %v, %v", slugs(res), err)
	}
	f.spaces.err = errors.New("down")
	if _, err := f.svc.StackAttachable(ctx, f.admin, "web", search.Input{}); err == nil {
		t.Error("a read with the spaces down succeeded")
	}
}
