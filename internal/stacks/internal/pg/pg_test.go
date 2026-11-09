package pg_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/core"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/pg"
)

// TestMain migrates the platform stream and the stacks stream alone: no
// stacks table references another domain's.
func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, schema.Stream(), pg.Stream())) }

func pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(t.Context(), sqltest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// TestAccStore reads a stack and the stacks in a space of one account,
// and none of another's.
func TestAccStore(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	acct, other := kernel.NewAccountID(), kernel.NewAccountID()
	space, id := kernel.NewULID(), kernel.NewULID()
	if _, err := db.Exec(ctx, `INSERT INTO stacks (account_id, id, slug, space_id, name,
			repository, branch, provider, vendor)
		VALUES ($1, $2, 'infra', $3, 'Infra', 'infra', 'main', 'GITHUB', 'terraform')`,
		acct, id, space); err != nil {
		t.Fatal(err)
	}
	want := stacksapi.Stack{ID: id, Account: acct, Slug: "infra", Name: "Infra", Space: space}
	if got, err := s.Get(ctx, acct, id); err != nil || got != want {
		t.Errorf("Get = %+v, %v; want %+v", got, err, want)
	}
	got, err := s.ByIDs(ctx, acct, []kernel.ULID{id, kernel.NewULID()})
	if err != nil || len(got) != 1 || got[id] != want {
		t.Errorf("ByIDs = %+v, %v; want only %+v", got, err, want)
	}
	if got, err := s.ByIDs(ctx, other, []kernel.ULID{id}); err != nil || len(got) != 0 {
		t.Errorf("ByIDs of another account = %+v, %v", got, err)
	}
	err = tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
		_, err := s.Get(ctx, acct, id)
		return err
	})
	if err != nil {
		t.Errorf("Get in a transaction = %v", err)
	}
	for name, err := range map[string]error{
		"a missing stack":       func() error { _, err := s.Get(ctx, acct, kernel.NewULID()); return err }(),
		"another account's one": func() error { _, err := s.Get(ctx, other, id); return err }(),
	} {
		if !errors.Is(err, stacksapi.ErrNotFound) {
			t.Errorf("Get of %s = %v, want ErrNotFound", name, err)
		}
	}
	for _, c := range []struct {
		name  string
		acct  kernel.AccountID
		space kernel.ULID
		want  bool
	}{
		{"the stack's space", acct, space, true},
		{"another space", acct, kernel.NewULID(), false},
		{"another account's space of that ID", other, space, false},
	} {
		if got, err := s.SpaceInUse(ctx, c.acct, c.space); err != nil || got != c.want {
			t.Errorf("SpaceInUse of %s = %v, %v; want %v", c.name, got, err, c.want)
		}
	}
}

// TestAccPurge deletes a deleted account's stacks a batch at a time, with
// their hooks, labels and stars, and none of another account's: a purge
// stopped after its first batch keeps what that batch deleted, and running
// it again finishes.
func TestAccPurge(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	gone, kept := kernel.NewAccountID(), kernel.NewAccountID()
	for i, acct := range []kernel.AccountID{gone, gone, gone, kept} {
		id, n := kernel.NewULID(), fmt.Sprint(i)
		for _, stmt := range []struct {
			sql  string
			args []any
		}{
			{
				`INSERT INTO stacks (account_id, id, slug, space_id, name, repository, branch,
				provider, vendor)
			VALUES ($1, $2, 'infra-' || $3, $2, 'Infra', 'infra', 'main', 'GITHUB', 'terraform')`,
				[]any{acct, id, n},
			},
			{`INSERT INTO stack_hooks (account_id, stack_id, phase, position, command)
			VALUES ($1, $2, 'before_init', 0, 'echo')`, []any{acct, id}},
			{
				`INSERT INTO stack_labels (account_id, stack_id, label) VALUES ($1, $2, 'infra')`,
				[]any{acct, id},
			},
			{`INSERT INTO stack_stars (account_id, stack_id, actor_kind, actor_id)
			VALUES ($1, $2, 'api_key', $2)`, []any{acct, id}},
		} {
			if _, err := db.Exec(ctx, stmt.sql, stmt.args...); err != nil {
				t.Fatalf("%s: %v", stmt.sql, err)
			}
		}
	}
	rows := func(acct kernel.AccountID) int {
		var n int
		if err := db.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM stacks WHERE account_id = $1)
			+ (SELECT count(*) FROM stack_hooks WHERE account_id = $1)
			+ (SELECT count(*) FROM stack_labels WHERE account_id = $1)
			+ (SELECT count(*) FROM stack_stars WHERE account_id = $1)`, acct).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if done, err := s.Purge(ctx, gone, 2); err != nil || done || rows(gone) != 4 {
		t.Fatalf("the first batch: done %t, %v, %d rows left", done, err, rows(gone))
	}
	for _, want := range []bool{true, true} {
		if done, err := s.Purge(ctx, gone, 2); err != nil || done != want {
			t.Fatalf("a later batch: done %t, %v", done, err)
		}
	}
	if n := rows(gone); n != 0 {
		t.Errorf("%d rows of the deleted account left", n)
	}
	if n := rows(kept); n != 4 {
		t.Errorf("another account keeps %d rows, want 4", n)
	}
}

// insertStack writes a stack of acct named slug in space with labels,
// and returns its ID.
func insertStack(
	t *testing.T,
	db *pgxpool.Pool,
	acct kernel.AccountID,
	slug string,
	space kernel.ULID,
	labels ...string,
) kernel.ULID {
	t.Helper()
	ctx := t.Context()
	id := kernel.NewULID()
	if _, err := db.Exec(ctx, `INSERT INTO stacks (account_id, id, slug, space_id, name,
		repository, branch, provider, vendor)
		VALUES ($1, $2, $3, $4, 'Infra', 'infra', 'main', 'GITHUB', 'terraform')`,
		acct, id, slug, space); err != nil {
		t.Fatal(err)
	}
	for _, l := range labels {
		if _, err := db.Exec(ctx, `INSERT INTO stack_labels (account_id, stack_id, label)
			VALUES ($1, $2, $3)`, acct, id, l); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

// TestAccHold holds a stack FOR KEY SHARE until the holder's transaction
// ends, which an owner's FOR UPDATE waits for, and refuses another
// account's stack and a missing one as not found.
func TestAccHold(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	acct := kernel.NewAccountID()
	id := insertStack(t, db, acct, "infra", kernel.NewULID())
	lock := func() error {
		_, err := db.Exec(ctx, `SELECT id FROM stacks WHERE id = $1 FOR UPDATE NOWAIT`, id)
		return err
	}
	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
			if err := s.Hold(ctx, acct, id); err != nil {
				return err
			}
			close(held)
			<-release
			return nil
		})
	}()
	select {
	case <-held:
	case err := <-done:
		t.Fatalf("Hold = %v", err)
	}
	if err := lock(); err == nil {
		t.Error("a FOR UPDATE passed a held stack")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := lock(); err != nil {
		t.Errorf("FOR UPDATE after the holder committed = %v", err)
	}
	for name, err := range map[string]error{
		"another account's": s.Hold(ctx, kernel.NewAccountID(), id),
		"a missing":         s.Hold(ctx, acct, kernel.NewULID()),
	} {
		if !errors.Is(err, stacksapi.ErrNotFound) {
			t.Errorf("Hold of %s stack = %v, want ErrNotFound", name, err)
		}
	}
}

// TestAccFind finds an account's stacks in the given spaces that carry
// any of the given labels, by slug, and no other account's; lists those
// in a space; and reads their labels.
func TestAccFind(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	acct, other := kernel.NewAccountID(), kernel.NewAccountID()
	team, ops := kernel.NewULID(), kernel.NewULID()
	insertStack(t, db, acct, "beta", team, "autoattach:prod", "web")
	insertStack(t, db, acct, "alpha", team, "db")
	insertStack(t, db, acct, "gamma", ops, "autoattach:prod")
	insertStack(t, db, acct, "delta", team)
	insertStack(t, db, other, "beta", team, "autoattach:prod")
	for _, tc := range []struct {
		spaces []kernel.ULID
		labels []string
		want   []string
	}{
		{[]kernel.ULID{team}, []string{"autoattach:prod", "db"}, []string{"alpha", "beta"}},
		{[]kernel.ULID{team, ops}, []string{"autoattach:prod"}, []string{"beta", "gamma"}},
		{[]kernel.ULID{ops}, []string{"web"}, nil},
		{[]kernel.ULID{team}, []string{"nothing"}, nil},
	} {
		got, err := s.Find(ctx, acct, tc.spaces, tc.labels)
		if err != nil {
			t.Fatal(err)
		}
		slugs := make([]string, 0, len(got))
		for _, st := range got {
			if st.Account != acct || st.ID.IsZero() || st.Space.IsZero() {
				t.Errorf("Find answered %+v", st)
			}
			slugs = append(slugs, st.Slug)
		}
		if fmt.Sprint(slugs) != fmt.Sprint(tc.want) {
			t.Errorf("Find(%v) = %v, want %v", tc.labels, slugs, tc.want)
		}
	}
	all, err := s.InSpaces(ctx, acct, []kernel.ULID{team})
	if err != nil {
		t.Fatal(err)
	}
	slugs := map[kernel.ULID]string{}
	ids := make([]kernel.ULID, 0, len(all))
	for _, st := range all {
		slugs[st.ID] = st.Slug
		ids = append(ids, st.ID)
	}
	if got := slices.Sorted(
		maps.Values(slugs),
	); !reflect.DeepEqual(
		got,
		[]string{"alpha", "beta", "delta"},
	) ||
		all[0].Slug != "alpha" {
		t.Errorf("InSpaces = %v", all)
	}
	labels, err := s.Labels(ctx, acct, ids)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for id, ls := range labels {
		got[slugs[id]] = ls
	}
	if want := map[string][]string{
		"alpha": {"db"},
		"beta":  {"autoattach:prod", "web"},
	}; !reflect.DeepEqual(
		got,
		want,
	) {
		t.Errorf("Labels = %v", got)
	}
}

// TestAccStoreUnavailable checks that a read on a closed pool fails, and
// not as a missing stack.
func TestAccStoreUnavailable(t *testing.T) {
	db := pool(t)
	s := pg.New(db)
	db.Close()
	ctx := context.Background()
	acct, id := kernel.NewAccountID(), kernel.NewULID()
	if _, err := s.Get(ctx, acct, id); err == nil || errors.Is(err, stacksapi.ErrNotFound) {
		t.Errorf("Get on a closed pool = %v", err)
	}
	if _, err := s.SpaceInUse(ctx, acct, id); err == nil {
		t.Error("SpaceInUse on a closed pool succeeded")
	}
	st := core.Stack{}
	st.Account, st.ID = acct, id
	actor := core.Actor{Kind: kernel.PrincipalAPIKey, ID: id}
	for name, err := range map[string]error{
		"Read":         func() error { _, err := s.Read(ctx, acct, id, false); return err }(),
		"ByRepository": func() error { _, err := s.ByRepository(ctx, acct, nil, "r", "b"); return err }(),
		"Create":       s.Create(ctx, st),
		"Write":        s.Write(ctx, st),
		"Delete":       s.Delete(ctx, acct, id),
		"Purge":        func() error { _, err := s.Purge(ctx, acct, 1); return err }(),
		"Hold":         s.Hold(ctx, acct, id),
		"Find": func() error {
			_, err := s.Find(ctx, acct, []kernel.ULID{id}, []string{"l"})
			return err
		}(),
		"InSpaces": func() error { _, err := s.InSpaces(ctx, acct, []kernel.ULID{id}); return err }(),
		"Labels":   func() error { _, err := s.Labels(ctx, acct, []kernel.ULID{id}); return err }(),
		"SetStar":  s.SetStar(ctx, acct, id, actor, true),
		"Starred":  func() error { _, err := s.Starred(ctx, acct, []kernel.ULID{id}, actor); return err }(),
		"Search": func() error {
			_, err := s.Search(ctx, acct, search.Input{}, core.Scope{Reachable: []kernel.ULID{id}})
			return err
		}(),
		"Suggest": func() error {
			_, err := s.Suggest(ctx, acct, search.SuggestionsInput{}, core.Scope{Actor: actor})
			return err
		}(),
	} {
		if err == nil || errors.Is(err, stacksapi.ErrNotFound) {
			t.Errorf("%s on a closed pool = %v", name, err)
		}
	}
}
