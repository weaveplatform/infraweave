package pg_test

import (
	"context"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/source"
)

// TestAccTokens checks that a run's tokens are kept once, listed by the
// run alone, forgotten once and again, purged with their account, and
// that each call on a closed pool fails.
func TestAccTokens(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s := pg.NewTokens(f.db)
	run, other := kernel.NewULID(), kernel.NewULID()
	tok := source.Token{
		Account: f.acct, Run: run, ID: "7", Stack: kernel.NewULID(), Integration: kernel.NewULID(),
		Provider: "GITLAB", Namespace: "acme", Repository: "infra",
		ExpiresAt: time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC),
	}
	second, elsewhere := tok, tok
	second.ID = "8"
	elsewhere.Run = other
	for _, k := range []source.Token{tok, tok, second, elsewhere} {
		if err := s.Add(ctx, k); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.List(ctx, f.acct, run)
	if err != nil || len(got) != 2 || got[0] != tok || got[1] != second {
		t.Fatalf("List = %+v, %v", got, err)
	}
	for range 2 {
		if err := s.Remove(ctx, f.acct, run, "7"); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := s.List(ctx, f.acct, run); err != nil || len(got) != 1 || got[0].ID != "8" {
		t.Errorf("List after Remove = %+v, %v", got, err)
	}

	store := pg.New(f.db, nil)
	if done, err := store.Purge(ctx, f.acct, 1); err != nil || done {
		t.Errorf("Purge = %t, %v", done, err)
	}
	if done, err := store.Purge(ctx, f.acct, 1); err != nil || done {
		t.Errorf("second Purge = %t, %v", done, err)
	}
	if done, err := store.Purge(ctx, f.acct, 1); err != nil || !done {
		t.Errorf("Purge of an empty account = %t, %v", done, err)
	}

	f.db.Close()
	for name, err := range map[string]error{
		"Add": s.Add(ctx, tok),
		"List": func() error {
			_, err := s.List(ctx, f.acct, run)
			return err
		}(),
		"Remove": s.Remove(ctx, f.acct, run, "8"),
	} {
		if err == nil {
			t.Errorf("%s on a closed pool = %v", name, err)
		}
	}
}
