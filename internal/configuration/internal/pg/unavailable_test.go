package pg_test

import (
	"context"
	"errors"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/configuration/internal/core"
	"github.com/weaveplatform/infraweave/internal/configuration/internal/pg"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// TestAccStoreUnavailable checks that every read and write on a closed
// pool fails, and not as a missing row.
func TestAccStoreUnavailable(t *testing.T) {
	db := pool(t)
	s := pg.New(db, keyring(t, db))
	db.Close()
	ctx := context.Background()
	acct, id := kernel.NewAccountID(), kernel.NewULID()
	ids := []kernel.ULID{id}
	owner := core.Owner{Context: id}
	scope := core.Scope{Reachable: ids}
	for name, err := range map[string]error{
		"Create": s.Create(ctx, core.Context{ID: id, Account: acct, Slug: "s"}),
		"Write":  s.Write(ctx, core.Context{ID: id, Account: acct}),
		"BySlug": func() error { _, err := s.BySlug(ctx, acct, "s", core.Update); return err }(),
		"ByIDs":  func() error { _, err := s.ByIDs(ctx, acct, ids); return err }(),
		"Delete": s.Delete(ctx, acct, id),
		"InSpace": func() error {
			_, err := s.InSpace(ctx, acct, id)
			return err
		}(),
		"Search": func() error { _, err := s.Search(ctx, acct, search.Input{}, scope); return err }(),
		"Suggest": func() error {
			_, err := s.Suggest(ctx, acct, search.SuggestionsInput{}, scope)
			return err
		}(),
		"Elements": func() error { _, err := s.Elements(ctx, acct, []core.Owner{owner}); return err }(),
		"Element":  func() error { _, err := s.Element(ctx, acct, owner, "A"); return err }(),
		"PutElement": s.PutElement(ctx, core.Element{
			ID: id, Account: acct, Owner: owner, Name: "A", Value: new("v"),
		}),
		"DeleteElements": s.DeleteElements(ctx, acct, owner, nil),
		"Attach": func() error {
			_, err := s.Attach(ctx, core.Attachment{ID: id, Account: acct, Context: id, Stack: id})
			return err
		}(),
		"Attachment":     func() error { _, err := s.Attachment(ctx, acct, id); return err }(),
		"Detach":         s.Detach(ctx, acct, id),
		"Attachments":    func() error { _, err := s.Attachments(ctx, acct, ids, true); return err }(),
		"AttachedStacks": func() error { _, err := s.AttachedStacks(ctx, acct); return err }(),
		"DeleteStack":    s.DeleteStack(ctx, acct, id),
		"Labelled": func() error {
			_, err := s.Labelled(ctx, acct, ids, []string{"autoattach:*"})
			return err
		}(),
		"AutoAttach": func() error {
			_, err := s.AutoAttach(ctx, core.Attachment{ID: id, Account: acct, Context: id, Stack: id})
			return err
		}(),
		"LockAutoattach": s.LockAutoattach(ctx, acct),
		"Opened":         func() error { _, err := s.Opened(ctx, acct, []core.Owner{owner}); return err }(),
	} {
		if err == nil || errors.Is(err, core.ErrNotFound) {
			t.Errorf("%s on a closed pool = %v", name, err)
		}
	}
}

// TestAccServiceUnavailable checks that every operation fails when the
// database, the spaces or the stacks it reads are down, and that none
// fails as a missing entity.
func TestAccServiceUnavailable(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.create(t, "Shared", "team", variable("A", "1"))
	a, _, err := f.svc.Attach(ctx, f.admin, "shared", "web", 0)
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.svc.Context(ctx, f.acct, "shared")
	if err != nil {
		t.Fatal(err)
	}
	in := core.Input{Space: "team", Name: "Shared"}
	ops := map[string]func() error{
		"Context":           func() error { _, err := f.svc.Context(ctx, f.acct, "shared"); return err },
		"SpaceOf":           func() error { _, err := f.svc.SpaceOf(ctx, f.acct, "shared"); return err },
		"AttachmentSpaceOf": func() error { _, err := f.svc.AttachmentSpaceOf(ctx, f.acct, a.ID.String()); return err },
		"Search":            func() error { _, err := f.svc.Search(ctx, f.admin, search.Input{}); return err },
		"Suggest": func() error {
			_, err := f.svc.Suggest(ctx, f.admin, search.SuggestionsInput{})
			return err
		},
		"Attachable": func() error {
			_, _, err := f.svc.Attachable(ctx, f.admin, "app", search.Input{})
			return err
		},
		"AttachableSuggest": func() error {
			_, err := f.svc.AttachableSuggest(ctx, f.admin, "app", search.SuggestionsInput{})
			return err
		},
		"Details": func() error { _, err := f.svc.Details(ctx, f.acct, c); return err },
		"Element": func() error { _, err := f.svc.Element(ctx, f.acct, "shared", "A"); return err },
		"AttachedStack": func() error {
			_, _, err := f.svc.AttachedStack(ctx, f.acct, "shared", a.ID.String())
			return err
		},
		"Create": func() error {
			_, err := f.svc.Create(ctx, f.admin, core.Input{
				Space: "team", Name: "Other",
				Stacks: []core.StackInput{{Stack: "web"}},
			})
			return err
		},
		"Update": func() error { _, err := f.svc.Update(ctx, f.admin, "shared", kernel.Expect{}, in, true); return err },
		"Delete": func() error { _, err := f.svc.Delete(ctx, f.admin, "shared", kernel.Expect{}, true); return err },
		"SetElement": func() error {
			_, _, err := f.svc.SetElement(ctx, f.admin, "shared", variable("B", "2"), "", "/id")
			return err
		},
		"DeleteElement": func() error { _, err := f.svc.DeleteElement(ctx, f.admin, "shared", "A"); return err },
		"Attach":        func() error { _, _, err := f.svc.Attach(ctx, f.admin, "shared", "web", 1); return err },
		"Detach":        func() error { _, _, err := f.svc.Detach(ctx, f.admin, a.ID.String()); return err },
		"SpaceInUse": func() error {
			_, err := f.svc.SpaceGuard().SpaceInUse(ctx, f.acct, f.sp["team"])
			return err
		},
		"SpaceLostOnMove": func() error {
			_, err := f.svc.SpaceGuard().LostOnMove(ctx, accessapi.SpaceMove{
				Account: f.acct, Space: f.sp["app"], To: []kernel.ULID{f.sp["hidden"]},
			})
			return err
		},
		"StackLostOnMove": func() error {
			_, err := f.svc.StackGuard().
				LostOnMove(ctx, stacksapi.StackMove{Stack: f.web, To: f.sp["team"]})
			return err
		},
		"Deleting": func() error { return f.svc.StackGuard().Deleting(ctx, f.web) },
		"AttachedContexts": func() error {
			_, err := f.svc.AttachedContexts(ctx, f.acct, []kernel.ULID{f.web.ID})
			return err
		},
		"Config":       func() error { _, err := f.svc.Config(ctx, f.acct, []kernel.ULID{f.web.ID}); return err },
		"StackChanged": func() error { return f.svc.StackChanged(ctx, f.acct, f.web.ID) },
		"Resolve":      func() error { _, err := f.svc.Resolve(ctx, f.acct, f.web.ID); return err },
	}

	down := errors.New("down")
	f.spaces.err = down
	for _, name := range []string{
		"Search", "Suggest", "Attachable", "AttachableSuggest", "Create",
		"Update", "Attach", "SpaceLostOnMove", "StackLostOnMove", "StackChanged",
	} {
		if err := ops[name](); !errors.Is(err, down) {
			t.Errorf("%s with the spaces down = %v", name, err)
		}
	}
	f.spaces.err = nil
	f.stacks.err = down
	for _, name := range []string{
		"AttachmentSpaceOf", "Details", "AttachedStack", "Create", "Attach",
		"Detach", "SpaceLostOnMove", "StackChanged", "Resolve",
	} {
		if err := ops[name](); !errors.Is(err, down) {
			t.Errorf("%s with the stacks down = %v", name, err)
		}
	}
	f.stacks.err = nil

	f.db.Close()
	for name, op := range ops {
		err := op()
		var m *core.Missing
		if err == nil || errors.Is(err, core.ErrNotFound) || errors.As(err, &m) {
			t.Errorf("%s on a closed pool = %v", name, err)
		}
	}
}

// TestUnitErrors reads each error as the API's detail quotes it.
func TestUnitErrors(t *testing.T) {
	for err, want := range map[error]string{
		&core.Denied{Action: accessapi.ActionContextCreate}: "you may not perform CONTEXT_CREATE here",
		&core.Missing{Kind: "stack", ID: "web"}:             `configuration: no stack "web"`,
		&core.SlugTaken{Slug: "prod"}:                       `configuration: slug "prod" is taken`,
	} {
		if err.Error() != want {
			t.Errorf("%T = %q", err, err.Error())
		}
	}
	if len(pg.Held()) != 4 || pg.SealedColumns()[0].Table != "config_elements" {
		t.Errorf("Held %v, SealedColumns %v", pg.Held(), pg.SealedColumns())
	}
	if s := core.New(core.Deps{}); s == nil {
		t.Error("New with no clock")
	}
}
