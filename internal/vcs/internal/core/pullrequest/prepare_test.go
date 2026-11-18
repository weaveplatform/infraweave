package pullrequest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type (
	planDB struct{}
	planTx struct{ pgx.Tx }
)

func (planDB) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) { return planTx{}, nil }
func (planTx) Commit(context.Context) error                           { return nil }
func (planTx) Rollback(context.Context) error                         { return nil }

type authors struct {
	login string
	err   error
	calls int
	id    string
}

func (a *authors) AuthorLogin(ctx context.Context, _ port.Integration, id string) (string, error) {
	if tx.In(ctx) {
		return "", errors.New("remote read under transaction")
	}
	if _, ok := ctx.Deadline(); !ok {
		return "", errors.New("unbounded remote read")
	}
	a.calls++
	a.id = id
	return a.login, a.err
}

type pushPlan struct {
	prepared, committed   int
	prepareErr, commitErr error
}

func (p *pushPlan) Prepare(context.Context, intake.Delivery, []vcsapi.Event) (intake.DispatchPlan, error) {
	p.prepared++
	return p, p.prepareErr
}

func (p *pushPlan) Commit(ctx context.Context) error {
	if !tx.In(ctx) {
		return ErrNoTransaction
	}
	p.committed++
	return p.commitErr
}

func requestDelivery() intake.Delivery {
	return intake.Delivery{Account: kernel.NewAccountID(), Integration: kernel.NewULID(), Provider: "GITLAB", ReceivedAt: time.Now().UTC(), Deadline: time.Now().Add(time.Minute)}
}

func requestEvent() vcsapi.Event {
	return vcsapi.Event{
		Type:       "pull_request",
		Action:     "opened",
		Head:       "head",
		Repository: vcsapi.Repository{ID: "repo-id", Namespace: "acme", Name: "infra"},
		PullRequest: &vcsapi.PullRequest{
			Number:        17,
			HeadBranch:    "feature",
			BaseBranch:    "main",
			AuthorID:      "42",
			Title:         "changes",
			URL:           "https://example.com/17",
			HeadBranchURL: "https://example.com/feature",
			Draft:         true,
		},
	}
}

func TestUnitRequestPreparationKeepsCreatorAndFence(t *testing.T) {
	for _, known := range []string{"payload", "stored", "lookup", "deleted integration"} {
		t.Run(known, func(t *testing.T) {
			t.Parallel()
			store := &memoryStore{}
			binding := &bindings{found: known != "deleted integration"}
			lookup := &authors{login: "creator"}
			pushes := &pushPlan{}
			d, event := requestDelivery(), requestEvent()
			if known == "payload" {
				event.PullRequest.AuthorLogin = "creator"
			}
			if known == "stored" {
				store.author = "creator"
			}
			s := New(Deps{Store: store, Integrations: binding, Authors: map[string]Authors{"GITLAB": lookup}, Pushes: pushes})
			plan, err := s.Prepare(t.Context(), d, []vcsapi.Event{{Type: "push"}, {Type: "pull_request"}, event})
			if err != nil {
				t.Fatal(err)
			}
			if len(store.rows) != 0 || pushes.committed != 0 {
				t.Fatal("preparation wrote outside fence")
			}
			if err := plan.Commit(t.Context()); !errors.Is(err, ErrNoTransaction) {
				t.Fatal("unfenced commit accepted", err)
			}
			if err := tx.Do(t.Context(), planDB{}, tx.Options{}, plan.Commit); err != nil {
				t.Fatal(err)
			}
			if len(store.rows) != 1 || pushes.prepared != 1 || pushes.committed != 1 {
				t.Fatal("compound plan lost work", store.rows, pushes)
			}
			r := store.rows[0]
			want := "creator"
			if known == "deleted integration" {
				want = ""
			}
			if r.Account != d.Account || r.Integration != d.Integration || r.RepositoryID != "repo-id" || r.Number != "17" || r.AuthorLogin != want || r.State != Open ||
				!r.Draft ||
				!r.CreatedAt.Equal(d.ReceivedAt) ||
				!r.UpdatedAt.Equal(d.ReceivedAt) ||
				r.HeadSHA != "head" {
				t.Fatalf("lost immutable snapshot: %+v", r)
			}
			if lookup.calls != map[string]int{"lookup": 1}[known] || lookup.calls > 0 && lookup.id != "42" {
				t.Fatal("unnecessary or wrong creator lookup", lookup)
			}
			if binding.used != map[string]int{"lookup": 1, "deleted integration": 1}[known] {
				t.Fatal("unnecessary credential read", binding.used)
			}
		})
	}
}

func TestUnitRequestPreparationFailures(t *testing.T) {
	for _, fault := range []string{"author", "integration", "lookup", "blank author", "no author provider", "push prepare", "upsert", "push commit"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			cause := errors.New("dependency unavailable")
			store := &memoryStore{cause: cause, errAt: fault}
			binding := &bindings{found: true}
			lookup := &authors{login: "creator"}
			pushes := &pushPlan{}
			if fault == "integration" {
				binding.err = cause
			}
			if fault == "lookup" {
				lookup.err = cause
			}
			if fault == "blank author" {
				lookup.login = ""
				cause = kernel.ErrUnavailable
			}
			providers := map[string]Authors{"GITLAB": lookup}
			if fault == "no author provider" {
				providers = nil
				cause = kernel.ErrUnavailable
			}
			if fault == "push prepare" {
				pushes.prepareErr = cause
			}
			if fault == "push commit" {
				pushes.commitErr = cause
			}
			s := New(Deps{Store: store, Integrations: binding, Authors: providers, Pushes: pushes})
			plan, err := s.Prepare(t.Context(), requestDelivery(), []vcsapi.Event{requestEvent()})
			if fault == "upsert" || fault == "push commit" {
				if err != nil {
					t.Fatal(err)
				}
				err = tx.Do(t.Context(), planDB{}, tx.Options{}, plan.Commit)
			}
			if !errors.Is(err, cause) {
				t.Fatal("dependency failure hidden", err)
			}
			if fault == "upsert" && pushes.committed != 0 {
				t.Fatal("push committed after request write failed")
			}
		})
	}
}

func TestUnitRequestStateAndRetention(t *testing.T) {
	t.Parallel()
	store := &memoryStore{prunes: []int{Batch, 0, Batch, 2}}
	now := time.Now().UTC()
	s := New(Deps{Store: store, Now: func() time.Time { return now }, Retention: time.Hour})
	for _, action := range []string{"closed", "merged", "edited"} {
		e := requestEvent()
		e.Action = action
		e.PullRequest.AuthorID = ""
		e.PullRequest.CreatedAt = now.Add(-time.Hour)
		e.PullRequest.UpdatedAt = now
		plan, err := s.Prepare(t.Context(), requestDelivery(), []vcsapi.Event{e})
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Do(t.Context(), planDB{}, tx.Options{}, plan.Commit); err != nil {
			t.Fatal(err)
		}
	}
	if store.rows[0].State != Closed || store.rows[1].State != Merged || store.rows[2].State != Open || !store.rows[0].CreatedAt.Equal(now.Add(-time.Hour)) {
		t.Fatal("native state/time lost", store.rows)
	}
	if err := s.Pruned(t.Context()); err != nil {
		t.Fatal(err)
	}
	acct := kernel.NewAccountID()
	if err := s.Purge(t.Context(), acct); err != nil {
		t.Fatal(err)
	}
	if len(store.pruneBefore) != 4 || store.pruneAccounts[0] != nil || !store.pruneBefore[0].Equal(now.Add(-time.Hour)) || *store.pruneAccounts[2] != acct ||
		store.pruneBefore[2] != nil {
		t.Fatal("retention cutoff/account batch changed")
	}
	store.errAt = "prune"
	store.cause = errors.New("store unavailable")
	if err := s.Pruned(t.Context()); !errors.Is(err, store.cause) {
		t.Fatal("prune failure hidden", err)
	}
}
