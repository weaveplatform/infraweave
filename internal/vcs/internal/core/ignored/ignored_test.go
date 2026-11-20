package ignored

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jackc/pgx/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

type (
	testDB struct{}
	testTx struct{ pgx.Tx }
)

func (testDB) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) { return testTx{}, nil }
func (testTx) Commit(context.Context) error                           { return nil }
func (testTx) Rollback(context.Context) error                         { return nil }

type memoryStore struct {
	rows           []Record
	scope          Scope
	input          search.Input
	prunes         []int
	accounts       []*kernel.AccountID
	cutoffs        []*time.Time
	deletedAccount kernel.AccountID
	deletedStack   kernel.ULID
	cause          error
}

func (m *memoryStore) RecordIgnored(ctx context.Context, r Record) error {
	if !tx.In(ctx) {
		return ErrNoTransaction
	}
	m.rows = append(m.rows, r)
	return m.cause
}

func (m *memoryStore) SearchIgnored(_ context.Context, scope Scope, in search.Input) (search.Result[Record], error) {
	m.scope, m.input = scope, in
	return search.Result[Record]{}, m.cause
}

func (m *memoryStore) PruneIgnored(_ context.Context, a *kernel.AccountID, b *time.Time, _ int) (int, error) {
	m.accounts = append(m.accounts, a)
	m.cutoffs = append(m.cutoffs, b)
	if m.cause != nil {
		return 0, m.cause
	}
	n := m.prunes[0]
	m.prunes = m.prunes[1:]
	return n, nil
}

func (m *memoryStore) DeleteIgnoredStack(_ context.Context, a kernel.AccountID, s kernel.ULID) error {
	m.deletedAccount, m.deletedStack = a, s
	return m.cause
}

type stacks struct {
	stacksapi.Stacks
	row     stacksapi.Stack
	cause   error
	account kernel.AccountID
	slug    string
}

func (s *stacks) BySlug(_ context.Context, a kernel.AccountID, slug string) (stacksapi.Stack, error) {
	s.account, s.slug = a, slug
	return s.row, s.cause
}

type authz struct {
	accessapi.Authorizer
	read, admin bool
	cause       error
	errAt       accessapi.Action
}

func (a authz) Authorize(_ context.Context, _ kernel.Principal, action accessapi.Action, _ kernel.ULID) (bool, error) {
	err := a.cause
	if a.errAt != "" && a.errAt != action {
		err = nil
	}
	if action == accessapi.ActionSpaceRead {
		return a.read, err
	}
	return a.admin, err
}

func TestUnitIgnoredCommitFactsAndWriteBoundary(t *testing.T) {
	t.Parallel()
	store := &memoryStore{}
	now := time.Now().UTC()
	s := New(Deps{Store: store, Now: func() time.Time { return now }})
	account, stack := kernel.NewAccountID(), kernel.NewULID()
	commit := stacksapi.Commit{
		Hash:        strings.Repeat("a", 40),
		Tag:         "v1",
		URL:         "https://example.com/commit",
		Message:     "summary\r\nprivate detail",
		AuthorName:  "Creator",
		AuthorLogin: new("creator"),
		Timestamp:   now.Add(-time.Hour),
	}
	if err := s.Record(t.Context(), account, stack, commit, TagsIgnored); !errors.Is(err, ErrNoTransaction) {
		t.Fatal("unfenced record accepted", err)
	}
	for _, tag := range []string{"v1", "v2"} {
		commit.Tag = tag
		if err := tx.Do(t.Context(), testDB{}, tx.Options{}, func(ctx context.Context) error { return s.Record(ctx, account, stack, commit, TagsIgnored) }); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.rows) != 2 {
		t.Fatal("recording unexpectedly depends on an entitlement")
	}
	for _, row := range store.rows {
		if row.Account != account || row.Stack != stack || row.Commit.Hash != commit.Hash || row.Commit.URL != commit.URL || row.Commit.AuthorName != commit.AuthorName ||
			*row.Commit.AuthorLogin != "creator" ||
			!row.Commit.Timestamp.Equal(commit.Timestamp) ||
			row.Commit.Message != "summary" ||
			!row.CreatedAt.Equal(now) ||
			row.Reason != TagsIgnored {
			t.Fatalf("API commit facts/once-per-commit key changed: %+v", row)
		}
	}
	if store.rows[0].ID == store.rows[1].ID || store.rows[0].Commit.Tag != "v1" || store.rows[1].Commit.Tag != "v2" {
		t.Fatal("record attempt identity/tag snapshot lost")
	}
	store.cause = errors.New("write refused")
	if err := tx.Do(
		t.Context(),
		testDB{},
		tx.Options{},
		func(ctx context.Context) error { return s.Record(ctx, account, stack, commit, TagsIgnored) },
	); !errors.Is(
		err,
		store.cause,
	) {
		t.Fatal("storage refusal hidden", err)
	}
	for _, tc := range []struct {
		name         string
		a            kernel.AccountID
		st           kernel.ULID
		hash, reason string
	}{
		{"account", kernel.AccountID{}, stack, commit.Hash, TagsIgnored}, {"stack", account, kernel.ULID{}, commit.Hash, TagsIgnored}, {"empty", account, stack, "", TagsIgnored}, {"zero", account, stack, strings.Repeat("0", 40), TagsIgnored}, {"nonhex", account, stack, strings.Repeat("z", 40), TagsIgnored}, {"unknown reason", account, stack, commit.Hash, "UNSUPPORTED_REASON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := commit
			c.Hash = tc.hash
			err := tx.Do(t.Context(), testDB{}, tx.Options{}, func(ctx context.Context) error { return s.Record(ctx, tc.a, tc.st, c, tc.reason) })
			if !errors.Is(err, ErrRecord) {
				t.Fatal("invalid record accepted", err)
			}
		})
	}
}

func TestUnitIgnoredSearchCurrentOwnerAndRefusals(t *testing.T) {
	for _, fault := range []string{"none", "missing", "stack down", "hidden", "reader", "auth down", "admin down", "store down"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			cause := errors.New("down")
			account := kernel.NewAccountID()
			p := kernel.Principal{AccountID: account}
			now := time.Now().UTC()
			st := &stacks{row: stacksapi.Stack{ID: kernel.NewULID(), Space: kernel.NewULID()}}
			auth := authz{read: true, admin: true}
			store := &memoryStore{}
			want := error(nil)
			switch fault {
			case "missing":
				st.cause = stacksapi.ErrNotFound
				want = stacksapi.ErrNotFound
			case "stack down":
				st.cause = cause
				want = cause
			case "hidden":
				auth.read = false
				want = stacksapi.ErrNotFound
			case "reader":
				auth.admin = false
				want = ErrForbidden
			case "auth down":
				auth.cause = cause
				want = cause
			case "admin down":
				auth.cause = cause
				auth.errAt = accessapi.ActionSpaceAdmin
				want = cause
			case "store down":
				store.cause = cause
				want = cause
			}
			s := New(Deps{Store: store, Stacks: st, Authz: auth, Now: func() time.Time { return now }})
			in := search.Input{After: "cursor"}
			_, err := s.Search(t.Context(), p, "infra", in)
			if !errors.Is(err, want) {
				t.Fatal("wrong read refusal", err, want)
			}
			if st.account != account || st.slug != "infra" {
				t.Fatal("stack lookup crossed account")
			}
			if fault == "none" &&
				(store.scope.Account != account || store.scope.Stack != st.row.ID || store.scope.Space != st.row.Space || !store.scope.Since.Equal(now.Add(-Retention)) || !reflect.DeepEqual(store.input, in)) {
				t.Fatal("scope/horizon/input lost", store.scope)
			}
		})
	}
	if err := search.Check(
		Fields,
		search.Input{
			OrderBy:    []search.Order{{Field: "createdAt", Desc: true}},
			Predicates: []search.Predicate{{Field: "reasonType", Constraint: search.Constraint{EnumEquals: []string{RunLimitReached}}}},
		},
	); err != nil {
		t.Fatal("declared reference search rejected", err)
	}
	if err := search.Check(Fields, search.Input{OrderBy: []search.Order{{Field: "unknown"}}}); err == nil {
		t.Fatal("unknown search field accepted")
	}
}

func TestUnitIgnoredSevenDayRetentionAndPurge(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		store := &memoryStore{prunes: []int{Batch, 0, 2}}
		s := New(Deps{Store: store})
		time.Sleep(Retention + time.Second) //nolint:forbidigo // advances only the synctest virtual clock
		if err := s.Pruned(t.Context()); err != nil {
			t.Fatal(err)
		}
		if len(store.cutoffs) != 2 || store.accounts[0] != nil || !store.cutoffs[0].Equal(start.Add(time.Second)) {
			t.Fatal("seven-day hourly horizon drifted", store.cutoffs)
		}
		account := kernel.NewAccountID()
		if err := s.Purge(t.Context(), account); err != nil {
			t.Fatal(err)
		}
		if *store.accounts[2] != account || store.cutoffs[2] != nil {
			t.Fatal("account purge was retention-limited")
		}
		stack := stacksapi.Stack{Account: account, ID: kernel.NewULID()}
		if err := s.Deleting(t.Context(), stack); !errors.Is(err, ErrNoTransaction) {
			t.Fatal("unfenced delete accepted", err)
		}
		if err := tx.Do(
			t.Context(),
			testDB{},
			tx.Options{},
			func(ctx context.Context) error { return s.Deleting(ctx, stack) },
		); err != nil || store.deletedAccount != account ||
			store.deletedStack != stack.ID {
			t.Fatal("stack delete scope", err)
		}
		if lost, err := s.LostOnMove(t.Context(), stacksapi.StackMove{}); err != nil || len(lost) != 0 {
			t.Fatal("records blocked their owner's move", lost, err)
		}
		store.cause = errors.New("down")
		if err := s.Pruned(t.Context()); !errors.Is(err, store.cause) {
			t.Fatal("prune failure hidden", err)
		}
		if err := tx.Do(t.Context(), testDB{}, tx.Options{}, func(ctx context.Context) error { return s.Deleting(ctx, stack) }); !errors.Is(err, store.cause) {
			t.Fatal("delete failure hidden", err)
		}
	})
}
