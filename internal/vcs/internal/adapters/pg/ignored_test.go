package pg_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/ignored"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/push"
)

type ignoredStack struct {
	stacksapi.Stacks
	settings stacksapi.Settings
	deleted  bool
}

type noPushPolicies struct{}

func (noPushPolicies) Evaluate(context.Context, policyapi.EvaluationRequest) (policyapi.Evaluation, error) {
	return policyapi.Evaluation{Type: policyapi.Push, Outcome: policyapi.NoPolicies}, nil
}

func (s *ignoredStack) PolicySnapshot(ctx context.Context, account kernel.AccountID, id kernel.ULID) (stacksapi.PolicySnapshot, error) {
	settings, err := s.Settings(ctx, account, id)
	return stacksapi.PolicySnapshot{Settings: settings}, err
}

func (s *ignoredStack) Settings(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.Settings, error) {
	if s.deleted {
		return stacksapi.Settings{}, stacksapi.ErrNotFound
	}
	return s.settings, nil
}

func (s *ignoredStack) LockForRun(ctx context.Context, a kernel.AccountID, id kernel.ULID) (stacksapi.Settings, error) {
	if !tx.In(ctx) {
		return stacksapi.Settings{}, errors.New("stack hold escaped evaluation transaction")
	}
	return s.Settings(ctx, a, id)
}

func (*ignoredStack) SetTracked(context.Context, kernel.AccountID, kernel.ULID, *stacksapi.Commit, *stacksapi.Commit, string) error {
	return nil
}

type ignoredSupersede struct{}

func (ignoredSupersede) Supersede(context.Context, kernel.AccountID, kernel.ULID, string, string) (int, error) {
	return 0, nil
}

type rollbackIgnoredCompletion struct {
	*queue.Queue
	cause error
}

func (q rollbackIgnoredCompletion) CompleteTx(ctx context.Context, j queue.Job) error {
	if err := q.Queue.CompleteTx(ctx, j); err != nil {
		return err
	}
	return q.cause
}

func ignoredCommit(hash string) stacksapi.Commit {
	return stacksapi.Commit{
		Hash:        hash,
		Branch:      "main",
		Message:     "Infrastructure change\nprivate body",
		AuthorName:  "Original author",
		AuthorLogin: new("author"),
		URL:         "https://example.com/acme/infra/commit/" + hash,
		Timestamp:   time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

func ignoredEvents(hash string) []push.Prepared {
	c := ignoredCommit(hash)
	repo := vcsapi.Repository{Namespace: "acme", Name: "infra"}
	tag := c
	tag.Tag = "release-one"
	tag.Branch = ""
	otherTag := tag
	otherTag.Tag = "release-two"
	return []push.Prepared{
		{Event: vcsapi.Event{Type: "push", Branch: "main", Head: hash, Repository: repo}, Commit: c, Files: []string{"README.md"}},
		{Event: vcsapi.Event{Type: "tag", Tag: tag.Tag, Head: hash, Repository: repo}, Commit: tag},
		{Event: vcsapi.Event{Type: "tag", Tag: otherTag.Tag, Head: hash, Repository: repo}, Commit: otherTag},
	}
}

// The fixture supplies the declared owner ports; delivery, prepared facts,
// claim fencing, recording and completion all use their real PostgreSQL stores.
func ignoredChild(t *testing.T, w *webhookWorld, name string, stack kernel.ULID, events []push.Prepared) (*push.Service, *ignoredStack, queue.Job) {
	t.Helper()
	w.receive(t, name, webhookBody(128))
	d := w.delivery(t, name)
	parent := w.claim(t, w.queue, "parent")
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		if err := w.queue.HoldTx(ctx, parent); err != nil {
			return err
		}
		if err := w.f.store.PutPrepared(ctx, w.f.acct, d.ID, events); err != nil {
			return err
		}
		if err := w.store.State(ctx, w.f.acct, d.ID, 0, intake.Dispatched, ""); err != nil {
			return err
		}
		if _, err := w.queue.EnqueueTx(
			ctx,
			push.Evaluate,
			push.Evaluation{Account: w.f.acct, Delivery: d.ID, Stack: stack, Deadline: d.Deadline},
			queue.Account(w.f.acct.ULID),
			queue.UniqueKey(intake.EvaluationKey(d.ID, 0, stack)),
		); err != nil {
			return err
		}
		return w.queue.CompleteTx(ctx, parent)
	}); err != nil {
		t.Fatal(err)
	}
	jobs, err := w.queue.Claim(t.Context(), queue.VCSInbox, []string{push.Evaluate.Name}, 1, time.Minute, "ignored-child")
	if err != nil || len(jobs) != 1 {
		t.Fatal("claim ignored child", jobs, err)
	}
	root := "project"
	st := &ignoredStack{
		settings: stacksapi.Settings{
			ID:             stack,
			Space:          w.f.acct.ULID,
			Namespace:      "acme",
			Repository:     "infra",
			Provider:       "GITHUB_ENTERPRISE",
			VCSIntegration: &w.id,
			Branch:         "main",
			ProjectRoot:    &root,
		},
	}
	service := push.New(
		push.Deps{
			DB:           w.f.db,
			Store:        w.f.store,
			Stacks:       st,
			Spaces:       w.f.deps.Spaces,
			Integrations: dispatchIntegration{w.id},
			Jobs:         w.queue,
			Supersede:    ignoredSupersede{},
			Ignored:      ignored.New(ignored.Deps{Store: w.f.store}),
			Policies:     noPushPolicies{}, PolicyStacks: st, PolicyAllowed: func(context.Context, kernel.AccountID) (bool, error) { return true, nil },
		},
	)
	return service, st, jobs[0]
}

func ignoredCount(t *testing.T, w *webhookWorld) int {
	t.Helper()
	var n int
	if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM vcs_ignored_triggers WHERE account_id=$1`, w.f.acct).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAccIgnoredTriggersFencedEvaluation(t *testing.T) {
	for _, fault := range []string{"none", "completion", "storage", "stale lease", "stale replay", "deleted stack"} {
		t.Run(fault, func(t *testing.T) {
			w := newWebhookWorld(t)
			stack := kernel.NewULID()
			events := ignoredEvents(strings.Repeat("a", 40))
			s, st, job := ignoredChild(t, w, "ignored", stack, events)
			cause := errors.New("acknowledgement unavailable")
			if fault == "completion" {
				s = push.New(
					push.Deps{
						DB:           w.f.db,
						Store:        w.f.store,
						Stacks:       st,
						Spaces:       w.f.deps.Spaces,
						Integrations: dispatchIntegration{w.id},
						Jobs:         rollbackIgnoredCompletion{w.queue, cause},
						Supersede:    ignoredSupersede{},
						Ignored:      ignored.New(ignored.Deps{Store: w.f.store}),
						Policies:     noPushPolicies{}, PolicyStacks: st, PolicyAllowed: func(context.Context, kernel.AccountID) (bool, error) { return true, nil },
					},
				)
			}
			if fault == "storage" {
				if _, err := w.f.db.Exec(
					t.Context(),
					`CREATE FUNCTION refuse_ignored_trigger() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'record unavailable' USING ERRCODE='P0001'; END $$;
		CREATE TRIGGER refuse_ignored_trigger BEFORE INSERT ON vcs_ignored_triggers FOR EACH ROW EXECUTE FUNCTION refuse_ignored_trigger()`,
				); err != nil {
					t.Fatal(err)
				}
			}
			if fault == "stale lease" {
				if _, err := w.f.db.Exec(t.Context(), `UPDATE jobs SET lease_until=now()-interval '1 minute' WHERE id=$1`, job.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := w.queue.Recover(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			if fault == "stale replay" {
				if _, err := w.f.db.Exec(t.Context(), `UPDATE vcs_deliveries SET replays=1 WHERE account_id=$1`, w.f.acct); err != nil {
					t.Fatal(err)
				}
			}
			st.deleted = fault == "deleted stack"
			err := s.Evaluated(t.Context(), job)
			switch fault {
			case "completion":
				if !errors.Is(err, cause) {
					t.Fatal(err)
				}
			case "storage":
				if p, ok := errors.AsType[*pgconn.PgError](err); !ok || p.Code != "P0001" {
					t.Fatal("storage refusal became ignored success", err)
				}
			case "stale lease":
				if !errors.Is(err, queue.ErrLeaseLost) {
					t.Fatal(err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
			want := 0
			if fault == "none" {
				want = 2
			}
			if n := ignoredCount(t, w); n != want {
				t.Fatal("ghost or missing ignored rows", n, want)
			}
			var state string
			if err := w.f.db.QueryRow(t.Context(), `SELECT state FROM jobs WHERE id=$1`, job.ID).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if (fault == "completion" || fault == "storage") && state != "running" {
				t.Fatal("failed row write committed acknowledgement", state)
			}
			if fault != "none" {
				return
			}
			if state != "succeeded" {
				t.Fatal("ignored evaluator not acknowledged", state)
			}
			// A distinct delivery and tag name do not create another record for
			// either reason; path and tag remain independently visible.
			s, _, job = ignoredChild(t, w, "duplicate", stack, events)
			if err := s.Evaluated(t.Context(), job); err != nil {
				t.Fatal(err)
			}
			if n := ignoredCount(t, w); n != 2 {
				t.Fatal("delivery dedup erased reason or duplicated history", n)
			}
			rows, err := w.f.store.SearchIgnored(t.Context(), ignored.Scope{Account: w.f.acct, Stack: stack, Space: w.f.acct.ULID}, search.Input{})
			if err != nil || len(rows.Edges) != 2 {
				t.Fatal(rows, err)
			}
			for _, edge := range rows.Edges {
				c := edge.Node.Commit
				if c.Hash != events[0].Commit.Hash || c.Message != "Infrastructure change" || c.AuthorName != "Original author" ||
					(c.AuthorLogin == nil || *c.AuthorLogin != "author") ||
					c.URL != events[0].Commit.URL ||
					!c.Timestamp.Equal(events[0].Commit.Timestamp) {
					t.Fatal("record lost immutable commit metadata", c)
				}
				if edge.Node.Reason == ignored.TagsIgnored && c.Tag != "release-one" {
					t.Fatal("tag did not retain first delivered identity", c.Tag)
				}
			}
			// Administrative replay creates a fresh delivery generation and
			// child claim, while the account/stack/hash/reason key remains stable.
			w.deps.RelatedKinds = []queue.Kind{push.Evaluate}
			w.svc = intake.New(w.deps)
			d := w.delivery(t, "ignored")
			if _, err := w.svc.Replay(t.Context(), w.f.admin, w.id, d.ID, "192.0.2.44"); err != nil {
				t.Fatal(err)
			}
			parent := w.claim(t, w.queue, "replay-parent")
			if err := w.svc.Processed(t.Context(), parent); err != nil {
				t.Fatal(err)
			}
			d = w.delivery(t, "ignored")
			if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
				if err := w.f.store.PutPrepared(ctx, w.f.acct, d.ID, events); err != nil {
					return err
				}
				_, err := w.queue.EnqueueTx(
					ctx,
					push.Evaluate,
					push.Evaluation{Account: w.f.acct, Delivery: d.ID, Stack: stack, Deadline: d.Deadline, Replays: 1},
					queue.Account(w.f.acct.ULID),
					queue.UniqueKey(intake.EvaluationKey(d.ID, 1, stack)),
				)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			replayed, err := w.queue.Claim(t.Context(), queue.VCSInbox, []string{push.Evaluate.Name}, 1, time.Minute, "replay-child")
			if err != nil || len(replayed) != 1 {
				t.Fatal(replayed, err)
			}
			if err := s.Evaluated(t.Context(), replayed[0]); err != nil {
				t.Fatal(err)
			}
			if n := ignoredCount(t, w); n != 2 {
				t.Fatal("replay duplicated ignored history", n)
			}
		})
	}
}

func TestAccIgnoredTriggerSearchAndRetention(t *testing.T) {
	w := newWebhookWorld(t)
	stack, other := kernel.NewULID(), kernel.NewULID()
	foreign := kernel.NewAccountID()
	at := time.Now().UTC().Truncate(time.Second)
	rows := []ignored.Record{
		{Account: w.f.acct, ID: kernel.NewULID(), Stack: stack, Commit: ignoredCommit(strings.Repeat("a", 40)), Reason: ignored.NoAffectedFiles, CreatedAt: at},
		{Account: w.f.acct, ID: kernel.NewULID(), Stack: stack, Commit: ignoredCommit(strings.Repeat("b", 40)), Reason: ignored.TagsIgnored, CreatedAt: at},
		{Account: w.f.acct, ID: kernel.NewULID(), Stack: stack, Commit: ignoredCommit(strings.Repeat("c", 40)), Reason: ignored.RunLimitReached, CreatedAt: at.Add(-time.Minute)},
		{Account: w.f.acct, ID: kernel.NewULID(), Stack: other, Commit: ignoredCommit(strings.Repeat("a", 40)), Reason: ignored.TagsIgnored, CreatedAt: at},
		{Account: foreign, ID: kernel.NewULID(), Stack: stack, Commit: ignoredCommit(strings.Repeat("a", 40)), Reason: ignored.TagsIgnored, CreatedAt: at},
	}
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		for _, r := range rows {
			if err := w.f.store.RecordIgnored(ctx, r); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	scope := ignored.Scope{Account: w.f.acct, Stack: stack, Space: w.f.acct.ULID, Since: at.Add(-ignored.Retention)}
	first, err := w.f.store.SearchIgnored(t.Context(), scope, search.Input{First: new(int32(1))})
	if err != nil || len(first.Edges) != 1 || first.Edges[0].Node.ID != rows[0].ID || !first.HasNext {
		t.Fatal("newest/ID order or scope incorrect", first, err)
	}
	second, err := w.f.store.SearchIgnored(t.Context(), scope, search.Input{First: new(int32(1)), After: first.End})
	if err != nil || len(second.Edges) != 1 || second.Edges[0].Node.ID != rows[1].ID {
		t.Fatal("cursor page lost deterministic tie", second, err)
	}
	back, err := w.f.store.SearchIgnored(t.Context(), scope, search.Input{First: new(int32(1)), After: second.Start, Page: search.Previous})
	if err != nil || len(back.Edges) != 1 || back.Edges[0].Node.ID != rows[0].ID {
		t.Fatal("reverse cursor incorrect", back, err)
	}
	for _, changed := range []ignored.Scope{{Account: foreign, Stack: stack, Space: foreign.ULID}, {Account: w.f.acct, Stack: other, Space: w.f.acct.ULID}} {
		if _, err := w.f.store.SearchIgnored(t.Context(), changed, search.Input{After: first.End}); err == nil {
			t.Fatal("cursor crossed account/stack")
		}
	}
	if _, err := w.f.store.SearchIgnored(t.Context(), scope, search.Input{After: "broken"}); err == nil {
		t.Fatal("bad cursor accepted")
	}
	filtered, err := w.f.store.SearchIgnored(
		t.Context(),
		scope,
		search.Input{Predicates: []search.Predicate{{Field: "reasonType", Constraint: search.Constraint{EnumEquals: []string{ignored.RunLimitReached}}}}},
	)
	if err != nil || len(filtered.Edges) != 1 || filtered.Edges[0].Node.ID != rows[2].ID {
		t.Fatal("reason filter incorrect", filtered, err)
	}
	filtered, err = w.f.store.SearchIgnored(
		t.Context(),
		scope,
		search.Input{
			Predicates: []search.Predicate{{Field: "createdAt", Constraint: search.Constraint{TimeInRange: &search.TimeRange{Start: new(int32(at.Unix()))}}}},
			OrderBy:    []search.Order{{Field: "createdAt"}},
		},
	)
	if err != nil || len(filtered.Edges) != 2 || filtered.Edges[0].Node.ID != rows[0].ID {
		t.Fatal("createdAt filter/order incorrect", filtered, err)
	}
	// Fill more than one prune batch, with an old foreign row in the same
	// database. Account purge must not merely appear isolated by fixtures.
	old := at.Add(-ignored.Retention - time.Second)
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		for i := range ignored.Batch + 1 {
			r := ignored.Record{
				Account:   w.f.acct,
				ID:        kernel.NewULID(),
				Stack:     stack,
				Commit:    ignoredCommit(fmt.Sprintf("%040x", i+100)),
				Reason:    ignored.TagsIgnored,
				CreatedAt: old,
			}
			if err := w.f.store.RecordIgnored(ctx, r); err != nil {
				return err
			}
		}
		r := rows[4]
		r.ID = kernel.NewULID()
		r.Commit.Hash = strings.Repeat("d", 40)
		r.CreatedAt = old
		return w.f.store.RecordIgnored(ctx, r)
	}); err != nil {
		t.Fatal(err)
	}
	svc := ignored.New(ignored.Deps{Store: w.f.store, Now: func() time.Time { return at }})
	visible, err := w.f.store.SearchIgnored(t.Context(), scope, search.Input{})
	if err != nil || len(visible.Edges) != 3 {
		t.Fatal("expired row visible before asynchronous prune", len(visible.Edges), err)
	}
	if err := svc.Pruned(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := ignoredCount(t, w); n != 4 {
		t.Fatal("fixed retention pruned recent rows or stopped at one batch", n)
	}
	rollback := errors.New("stack deletion rolled back")
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		if err := svc.Deleting(ctx, stacksapi.Stack{Account: w.f.acct, ID: stack}); err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) || ignoredCount(t, w) != 4 {
		t.Fatal("stack guard did not join deletion transaction", err)
	}
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		return svc.Deleting(ctx, stacksapi.Stack{Account: w.f.acct, ID: stack})
	}); err != nil {
		t.Fatal(err)
	}
	if n := ignoredCount(t, w); n != 1 {
		t.Fatal("stack guard crossed sibling boundary", n)
	}
	if err := svc.Purge(t.Context(), w.f.acct); err != nil {
		t.Fatal(err)
	}
	if n := ignoredCount(t, w); n != 0 {
		t.Fatal("account purge retained ignored rows", n)
	}
	var preserved int
	if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM vcs_ignored_triggers WHERE account_id=$1`, foreign).Scan(&preserved); err != nil || preserved != 1 {
		t.Fatal("purge crossed account", preserved, err)
	}
}

func TestAccIgnoredTriggerStoreRefusals(t *testing.T) {
	f := newFixture(t)
	r := ignored.Record{
		Account:   f.acct,
		ID:        kernel.NewULID(),
		Stack:     kernel.NewULID(),
		Commit:    ignoredCommit(strings.Repeat("a", 40)),
		Reason:    ignored.TagsIgnored,
		CreatedAt: time.Now(),
	}
	if err := f.store.RecordIgnored(t.Context(), r); !errors.Is(err, ignored.ErrNoTransaction) {
		t.Fatal("unfenced write", err)
	}
	if err := f.store.DeleteIgnoredStack(t.Context(), f.acct, r.Stack); !errors.Is(err, ignored.ErrNoTransaction) {
		t.Fatal("unfenced deletion", err)
	}
	f.db.Close()
	s := pg.New(f.db, f.keys)
	if err := tx.Do(t.Context(), f.db, tx.Options{}, func(ctx context.Context) error { return s.RecordIgnored(ctx, r) }); err == nil {
		t.Fatal("closed transaction accepted")
	}
	if _, err := s.SearchIgnored(t.Context(), ignored.Scope{Account: f.acct, Stack: r.Stack, Space: f.acct.ULID}, search.Input{}); err == nil {
		t.Fatal("search storage error hidden")
	}
	if _, err := s.PruneIgnored(t.Context(), &f.acct, nil, 1); err == nil {
		t.Fatal("prune storage error hidden")
	}
}
