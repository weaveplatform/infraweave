package pg_test

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/configuration/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// labelled creates a context named name in space as the admin, with
// labels.
func (f *fixture) labelled(t *testing.T, name, space string, labels ...string) core.Context {
	t.Helper()
	c, err := f.svc.Create(
		t.Context(),
		f.admin,
		core.Input{Space: space, Name: name, Labels: labels},
	)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// relabel replaces the labels of the admin's context slug in space.
func (f *fixture) relabel(t *testing.T, slug, name, space string, labels ...string) {
	t.Helper()
	if _, err := f.svc.Update(t.Context(), f.admin, slug, kernel.Expect{},
		core.Input{Space: space, Name: name, Labels: labels}, false); err != nil {
		t.Fatal(err)
	}
}

// attached are the contexts attached to stack, in the stack view's
// order, an automatic one marked "auto:".
func (f *fixture) attached(t *testing.T, stack kernel.ULID) []string {
	t.Helper()
	got, err := f.svc.AttachedContexts(t.Context(), f.acct, []kernel.ULID{stack})
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, a := range got[stack] {
		if a.Autoattached {
			out = append(out, "auto:"+a.Context)
		} else {
			out = append(out, a.Context)
		}
	}
	return out
}

// TestAccLabelled reads the contexts in spaces carrying one of labels, by
// name, and attaches a context automatically once.
func TestAccLabelled(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	f.labelled(t, "Beta", "team", "autoattach:infra", "x")
	f.labelled(t, "Alpha", "", "autoattach:*")
	f.labelled(t, "Gamma", "hidden", "autoattach:infra")
	f.labelled(t, "Delta", "team", "infra")
	got, err := f.store.Labelled(ctx, f.acct, []kernel.ULID{f.sp["acme"], f.sp["team"]},
		[]string{"autoattach:infra", "autoattach:*"})
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(got))
	for _, c := range got {
		names = append(names, c.Name)
	}
	if !reflect.DeepEqual(names, []string{"Alpha", "Beta"}) {
		t.Errorf("labelled = %v", names)
	}
	if got, err := f.store.Labelled(ctx, kernel.NewAccountID(), []kernel.ULID{f.sp["team"]},
		[]string{"autoattach:infra"}); err != nil || len(got) != 0 {
		t.Errorf("another account's = %v, %v", got, err)
	}

	a := core.Attachment{
		ID: kernel.NewULID(), Account: f.acct, Context: got[0].ID, Stack: f.data.ID,
		Autoattached: true, CreatedAt: f.now,
	}
	if ok, err := f.store.AutoAttach(ctx, a); err != nil || !ok {
		t.Errorf("auto-attaching = %v, %v", ok, err)
	}
	a.ID = kernel.NewULID()
	if ok, err := f.store.AutoAttach(ctx, a); err != nil || ok {
		t.Errorf("auto-attaching again = %v, %v", ok, err)
	}
}

// TestAccAutoattach attaches contexts automatically to the stacks in
// their reach that their autoattach labels name, follows label and space
// changes on the context's side and on the stack's, and leaves a
// manual attachment alone.
func TestAccAutoattach(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	f.stacks.labels["web"] = []string{"infra"}
	f.stacks.labels["db"] = []string{"infra"}

	top := f.labelled(t, "Top", "", "autoattach:infra")
	f.labelled(t, "Every", "team", "autoattach:*")
	f.labelled(t, "Leafy", "leaf", "autoattach:*")
	f.create(t, "Hand", "team")
	if _, _, err := f.svc.Attach(ctx, f.admin, "hand", "web", 5); err != nil {
		t.Fatal(err)
	}
	if got := f.attached(t, f.web.ID); !reflect.DeepEqual(got,
		[]string{"hand", "auto:every", "auto:top"}) {
		t.Errorf("web attaches %v", got)
	}
	if got := f.attached(t, f.data.ID); len(got) != 0 {
		t.Errorf("db, in a space that inherits nothing, attaches %v", got)
	}
	if got := f.changes(t, top.ID); !reflect.DeepEqual(got, []string{"created"}) {
		t.Errorf("top's changes = %v", got)
	}

	as, err := f.store.Attachments(ctx, f.acct, []kernel.ULID{top.ID}, false)
	if err != nil || len(as[top.ID]) != 1 {
		t.Fatalf("top's attachments = %v, %v", as, err)
	}
	if _, _, err := f.svc.Detach(ctx, f.admin, as[top.ID][0].ID.String()); !reflect.DeepEqual(
		pointers(t, err), []string{"/attachmentId"}) {
		t.Errorf("detaching an automatic attachment = %v", err)
	}

	f.relabel(t, "top", "Top", "")
	if got := f.attached(t, f.web.ID); !reflect.DeepEqual(got, []string{"hand", "auto:every"}) {
		t.Errorf("web, top's label removed, attaches %v", got)
	}
	f.relabel(t, "top", "Top", "hidden", "autoattach:infra")
	if got := f.attached(t, f.data.ID); !reflect.DeepEqual(got, []string{"auto:top"}) {
		t.Errorf("db, top moved beside it, attaches %v", got)
	}
	f.relabel(t, "every", "Every", "team", "autoattach:infra")
	if _, _, err := f.svc.Attach(ctx, f.admin, "every", "web", 2); err != nil {
		t.Fatal(err)
	}
	f.relabel(t, "every", "Every", "team")
	if got := f.attached(t, f.web.ID); !reflect.DeepEqual(got, []string{"every", "hand"}) {
		t.Errorf("web, every attached by hand, attaches %v", got)
	}
	if lost, err := f.svc.StackGuard().LostOnMove(ctx, stacksapi.StackMove{
		Stack: f.web, To: f.sp["hidden"],
	}); err != nil || !reflect.DeepEqual(lost, []string{"context every", "context hand"}) {
		t.Errorf("moving web to hidden loses %v, %v", lost, err)
	}
	if lost, err := f.svc.StackGuard().LostOnMove(ctx, stacksapi.StackMove{
		Stack: f.data, To: f.sp["acme"],
	}); err != nil || len(lost) != 0 {
		t.Errorf("moving db, attached automatically alone, loses %v, %v", lost, err)
	}

	api := stacksapi.Stack{
		ID:      kernel.NewULID(),
		Account: f.acct,
		Slug:    "api",
		Name:    "API",
		Space:   f.sp["team"],
	}
	f.stacks.all["api"] = api
	other := f.labelled(t, "Other", "", "autoattach:infra")
	if err := f.svc.StackChanged(ctx, f.acct, api.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.attached(t, api.ID); len(got) != 0 {
		t.Errorf("api, unlabelled, attaches %v", got)
	}
	if got := f.changes(t, other.ID); !reflect.DeepEqual(got, []string{"created"}) {
		t.Errorf("other's changes, nothing attached = %v", got)
	}
	f.stacks.labels["api"] = []string{"infra"}
	if err := f.svc.StackChanged(ctx, f.acct, api.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.attached(t, api.ID); !reflect.DeepEqual(got, []string{"auto:other"}) {
		t.Errorf("api, labelled, attaches %v", got)
	}
	if err := f.svc.StackChanged(ctx, f.acct, api.ID); err != nil {
		t.Fatal(err)
	}
	f.stacks.labels["api"] = nil
	if err := f.svc.StackChanged(ctx, f.acct, api.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.attached(t, api.ID); len(got) != 0 {
		t.Errorf("api, unlabelled again, attaches %v", got)
	}
	if got := f.changes(t, other.ID); !reflect.DeepEqual(got,
		[]string{"created", "attached", "detached"}) {
		t.Errorf("other's changes = %v", got)
	}
	if err := f.svc.StackChanged(ctx, f.acct, f.web.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.attached(t, f.web.ID); !reflect.DeepEqual(got,
		[]string{"every", "hand", "auto:other"}) {
		t.Errorf("web, recomputed, attaches %v", got)
	}
	if err := f.svc.StackChanged(ctx, f.acct, kernel.NewULID()); err != nil {
		t.Errorf("a deleted stack = %v", err)
	}
}

// TestAccAutoattachConcurrent labels a stack while a context that names
// the label is created: the stack's change waits for the context to
// commit, then attaches it, rather than each missing the other.
func TestAccAutoattachConcurrent(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	changed := make(chan error, 1)
	err := tx.Do(ctx, f.db, tx.Options{}, func(ctx context.Context) error {
		if _, err := f.svc.Create(ctx, f.admin, core.Input{
			Space: "team", Name: "Late", Labels: []string{"autoattach:infra"},
		}); err != nil {
			return err
		}
		f.stacks.mu.Lock()
		f.stacks.labels["web"] = []string{"infra"}
		f.stacks.mu.Unlock()
		go func() { //nolint:contextcheck // the stack's change runs its own transaction, not the create's
			changed <- f.svc.StackChanged(t.Context(), f.acct, f.web.ID)
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
	if err := <-changed; err != nil {
		t.Fatal(err)
	}
	if got := f.attached(t, f.web.ID); !slices.Equal(got, []string{"auto:late"}) {
		t.Errorf("web attaches %v", got)
	}
	if err := f.svc.StackChanged(ctx, f.acct, f.web.ID); err != nil {
		t.Errorf("again = %v", err)
	}
	if got := f.attached(t, f.web.ID); !slices.Equal(got, []string{"auto:late"}) {
		t.Errorf("web, again, attaches %v", got)
	}
}
