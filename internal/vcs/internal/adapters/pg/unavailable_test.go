package pg_test

import (
	"context"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
)

// TestAccUnavailable checks that every read and write on a closed pool
// fails, and not as a missing row, through the store and the service.
func TestAccUnavailable(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	in, err := f.svc.Create(ctx, f.admin, gitlab("Main", "", true))
	if err != nil {
		t.Fatal(err)
	}
	f.db.Close()
	id, acct := in.ID.String(), f.acct
	s := pg.New(f.db, nil)
	for name, err := range map[string]error{
		"Create": s.Create(ctx, in, integration.Secrets{}),
		"Write":  s.Write(ctx, in, nil),
		"Get":    func() error { _, err := s.Get(ctx, acct, in.ID, true); return err }(),
		"Secrets": func() error {
			_, err := s.Secrets(ctx, acct, in.ID)
			return err
		}(),
		"Delete":  s.Delete(ctx, acct, in.ID),
		"InSpace": func() error { _, err := s.InSpace(ctx, acct, in.ID); return err }(),
		"Purge":   func() error { _, err := s.Purge(ctx, acct, 1); return err }(),
		"Search": func() error {
			_, err := s.Search(ctx, acct, search.Input{}, integration.Scope{Provider: "GITLAB"})
			return err
		}(),
		"Suggest": func() error {
			_, err := s.Suggest(ctx, acct, search.SuggestionsInput{}, integration.Scope{})
			return err
		}(),
		"Service.Search": func() error {
			_, err := f.svc.Search(ctx, f.admin, search.Input{})
			return err
		}(),
		"Service.Suggest": func() error {
			_, err := f.svc.Suggest(ctx, f.admin, search.SuggestionsInput{})
			return err
		}(),
		"Service.Attachable": func() error {
			_, err := f.svc.Attachable(ctx, f.admin, "team", "GITLAB", search.Input{})
			return err
		}(),
		"Service.Test": func() error { _, err := f.svc.Test(ctx, f.admin, id, "GITLAB"); return err }(),
		"Service.Use": func() error {
			_, _, err := f.svc.Use(ctx, acct, "GITLAB", nil)
			return err
		}(),
		"Service.Bindable": f.svc.Bindable(ctx, acct, acct.ULID, "GITLAB", in.ID),
		"Service.SpaceOf": func() error {
			_, err := f.svc.SpaceOf(ctx, acct, id)
			return err
		}(),
	} {
		if err == nil || errors.Is(err, integration.ErrNotFound) {
			t.Errorf("%s on a closed pool = %v", name, err)
		}
	}
	if len(pg.Held()) != 5 || len(pg.SealedColumns()) != 3 {
		t.Errorf("held %v, sealed %v", pg.Held(), pg.SealedColumns())
	}
	if (&integration.Missing{Kind: "space", ID: "x"}).Error() == "" {
		t.Error("a Missing names nothing")
	}
}

// TestAccWriteMissing checks that writing an integration the account lacks
// is integration.ErrNotFound.
func TestAccWriteMissing(t *testing.T) {
	f := newFixture(t)
	s := pg.New(f.db, nil)
	err := s.Write(t.Context(), integration.Integration{
		Account: f.acct, ID: kernel.NewULID(), Labels: []string{},
	}, nil)
	if !errors.Is(err, integration.ErrNotFound) {
		t.Errorf("Write of a missing integration = %v", err)
	}
}
