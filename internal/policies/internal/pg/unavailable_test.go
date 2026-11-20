package pg_test

import (
	"context"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
)

func TestAccPolicyStoreUnavailable(t *testing.T) {
	f := newFixture(t)
	p := f.create(t, "Guard")
	f.db.Close()
	ctx := t.Context()
	scope := core.Scope{Reachable: []kernel.ULID{p.Space}}
	for name, err := range map[string]error{
		"create":    f.store.Create(ctx, p),
		"get":       func() error { _, err := f.store.Get(ctx, p.Account, p.ID, false); return err }(),
		"update":    f.store.Update(ctx, p),
		"delete":    f.store.Delete(ctx, p.Account, p.ID),
		"in-space":  func() error { _, err := f.store.InSpace(ctx, p.Account, p.Space); return err }(),
		"count":     func() error { _, err := f.store.AttachmentCount(ctx, p.Account, p.ID); return err }(),
		"lock-auto": f.store.LockAutoattach(ctx, p.Account),
		"attach": func() error {
			_, err := f.store.Attach(ctx, core.Attachment{Account: p.Account, Policy: p.ID})
			return err
		}(),
		"auto-attach": func() error {
			_, err := f.store.AutoAttach(ctx, core.Attachment{Account: p.Account, Policy: p.ID})
			return err
		}(),
		"attachment":          func() error { _, err := f.store.Attachment(ctx, p.Account, kernel.NewULID()); return err }(),
		"attachments":         func() error { _, err := f.store.Attachments(ctx, p.Account, []kernel.ULID{p.ID}, false); return err }(),
		"account-attachments": func() error { _, err := f.store.AccountAttachments(ctx, p.Account); return err }(),
		"automatic-policies":  func() error { _, err := f.store.AutomaticPolicies(ctx, p.Account); return err }(),
		"labelled": func() error {
			_, err := f.store.Labelled(ctx, p.Account, []kernel.ULID{p.Space}, []string{"autoattach:*"})
			return err
		}(),
		"by-ids":       func() error { _, err := f.store.ByIDs(ctx, p.Account, []kernel.ULID{p.ID}); return err }(),
		"detach":       f.store.Detach(ctx, p.Account, kernel.NewULID()),
		"delete-stack": f.store.DeleteStack(ctx, p.Account, kernel.NewULID()),
		"purge":        func() error { _, err := f.store.Purge(ctx, p.Account, 10); return err }(),
		"search":       func() error { _, err := f.store.Search(ctx, p.Account, search.Input{}, scope); return err }(),
		"suggest":      func() error { _, err := f.store.Suggest(ctx, p.Account, search.SuggestionsInput{}, scope); return err }(),
	} {
		if err == nil || errors.Is(err, core.ErrNotFound) {
			t.Errorf("%s hid database failure: %v", name, err)
		}
	}
}

func TestAccPolicyStorageFailuresRollback(t *testing.T) {
	f := newFixture(t)
	p := f.create(t, "Stable")
	_, err := f.db.Exec(t.Context(), `CREATE FUNCTION refuse_policy_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected policy write failure'; END $$;
CREATE TRIGGER refuse_policy_write BEFORE INSERT OR UPDATE OR DELETE ON policies FOR EACH ROW EXECUTE FUNCTION refuse_policy_write()`)
	if err != nil {
		t.Fatal(err)
	}
	in := core.Input{Name: "Refused", Body: p.Body, Type: p.Type}
	for name, call := range map[string]func() error{
		"create": func() error { _, err := f.svc.Create(t.Context(), f.principal, in); return err },
		"update": func() error {
			_, err := f.svc.Update(t.Context(), f.principal, p.ID.String(), kernel.Expect{}, in)
			return err
		},
		"delete": func() error {
			_, err := f.svc.Delete(t.Context(), f.principal, p.ID.String(), kernel.Expect{}, true)
			return err
		},
	} {
		err := call()
		var missing *core.Missing
		if err == nil || errors.As(err, &missing) {
			t.Fatalf("%s storage refusal disappeared: %v", name, err)
		}
		stored, err := f.store.Get(t.Context(), p.Account, p.ID, false)
		if err != nil || stored.Name != p.Name || stored.Version != p.Version || f.count(t, "events") != 1 || f.count(t, "policies") != 1 {
			t.Fatalf("%s partially committed: %+v %v", name, stored, err)
		}
	}
	if _, err := f.db.Exec(t.Context(), "DROP TABLE policy_stack_attachments"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Delete(t.Context(), f.principal, p.ID.String(), kernel.Expect{}, true); err == nil {
		t.Fatal("deletion ignored unreadable attachments")
	}
	if _, err := f.db.Exec(t.Context(), "DROP TABLE policies"); err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"get": func() error { _, err := f.svc.Get(t.Context(), p.Account, p.ID.String()); return err },
		"update": func() error {
			_, err := f.svc.Update(t.Context(), f.principal, p.ID.String(), kernel.Expect{}, in)
			return err
		},
		"delete": func() error {
			_, err := f.svc.Delete(t.Context(), f.principal, p.ID.String(), kernel.Expect{}, true)
			return err
		},
	} {
		err := call()
		var missing *core.Missing
		if err == nil || errors.As(err, &missing) {
			t.Errorf("%s misclassified missing table: %v", name, err)
		}
	}
}

func TestAccPolicyMissingRowsAndCancellation(t *testing.T) {
	f := newFixture(t)
	p := f.create(t, "Guard")
	missingID := kernel.NewULID()
	ghost := p
	ghost.ID = missingID
	if err := f.store.Update(t.Context(), ghost); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("updating absent row: %v", err)
	}
	for _, id := range []string{"invalid", missingID.String()} {
		for name, call := range map[string]func() error{
			"get": func() error { _, err := f.svc.Get(t.Context(), p.Account, id); return err },
			"update": func() error {
				_, err := f.svc.Update(t.Context(), f.principal, id, kernel.Expect{}, core.Input{Name: "Guard", Body: p.Body})
				return err
			},
			"delete": func() error { _, err := f.svc.Delete(t.Context(), f.principal, id, kernel.Expect{}, false); return err },
		} {
			err := call()
			var missing *core.Missing
			if !errors.As(err, &missing) || missing.Error() == "" {
				t.Errorf("%s(%s): %v", name, id, err)
			}
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := f.svc.Create(ctx, f.principal, core.Input{Name: "Cancelled", Body: p.Body, Type: p.Type})
	if !errors.Is(err, context.Canceled) || f.count(t, "policies") != 1 {
		t.Fatalf("cancelled compile persisted a policy: %v", err)
	}
}
