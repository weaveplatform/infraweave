package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/feedback"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/pullrequest"
)

type commentReadFacts struct {
	stacksapi.Stacks
	history runsapi.RunHistory
	stack   stacksapi.Settings
	log     string
	logs    map[kernel.ULID]string
}

func (f *commentReadFacts) HistoryAt(context.Context, kernel.AccountID, kernel.ULID, int64) (runsapi.RunHistory, error) {
	return f.history, nil
}

func (f *commentReadFacts) Settings(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.Settings, error) {
	return f.stack, nil
}

func (f *commentReadFacts) Labels(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID][]string, error) {
	return map[kernel.ULID][]string{f.stack.ID: {"feature:add_plan_pr_comment"}}, nil
}

func (f *commentReadFacts) PlanningLog(_ context.Context, _ kernel.AccountID, run kernel.ULID, version int64, budget int) (runsapi.PlanningLog, error) {
	text, found := f.logs[run]
	if !found || version != 2 || budget != runsapi.MaxPlanningLogBytes {
		return runsapi.PlanningLog{}, errors.New("comment read wrong planning execution")
	}
	return runsapi.PlanningLog{Text: text}, nil
}

type commentWorld struct {
	*deploymentWorld
	facts   *commentReadFacts
	service *feedback.Service
	request pullrequest.Request
}

func newCommentWorld(t *testing.T) *commentWorld {
	t.Helper()
	w := newDeploymentWorld(t)
	facts := &commentReadFacts{history: runsapi.RunHistory{
		ID:         w.binding.Run.ID,
		Stack:      w.binding.Stack.ID,
		Type:       kernel.RunTypeTracked,
		CommitHash: w.binding.Run.Commit,
		CreatedAt:  w.binding.CreatedAt,
		Through:    3,
		Transitions: []runsapi.RunTransition{
			{Sequence: 1, State: kernel.RunStateQueued},
			{Sequence: 2, State: kernel.RunStatePlanning},
			{Sequence: 3, State: kernel.RunStateUnconfirmed},
		},
	}, stack: stacksapi.Settings{
		Stack: stacksapi.Stack{ID: w.binding.Stack.ID, Slug: "infra"}, Provider: "GITLAB", VCSIntegration: &w.binding.Integration,
		Namespace: "acme", Repository: "infra",
	}, log: "masked password=***\nplan: create one resource\n"}
	request := pullrequest.Request{
		Account: w.f.acct, ID: kernel.NewULID(), Integration: w.binding.Integration, RepositoryID: "7", Number: "7",
		Namespace: "acme", Repository: "infra", HeadSHA: w.binding.Run.Commit, State: pullrequest.Open, CreatedAt: w.binding.CreatedAt, UpdatedAt: w.binding.CreatedAt,
	}
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error { return w.f.store.Upsert(ctx, request) }); err != nil {
		t.Fatal(err)
	}
	s := feedback.New(w.jobs, func(ctx context.Context, acct kernel.AccountID) error {
		return w.f.deps.Spaces.Hold(ctx, acct, acct.ULID)
	}).
		WithComments(feedback.CommentDeps{
			DB: w.f.db, Store: w.f.store, Histories: facts, Stacks: facts, Integrations: w.f.svc,
			Providers: map[string]port.Comments{"GITLAB": w.provider.Provider}, Logs: facts, Public: "https://infra.example",
		})
	return &commentWorld{deploymentWorld: w, facts: facts, service: s, request: request}
}

func (w *commentWorld) targets(t *testing.T) []vcsapi.FeedbackTarget {
	t.Helper()
	h := w.facts.history
	if w.facts.logs == nil {
		w.facts.logs = map[kernel.ULID]string{}
	}
	w.facts.logs[h.ID] = w.facts.log
	u := runsapi.Updated{
		State: kernel.RunStateUnconfirmed, Run: runsapi.UpdatedRun{ID: h.ID, Type: h.Type, State: kernel.RunStateUnconfirmed, Commit: runsapi.UpdatedCommit{Hash: h.CommitHash}},
		Stack: runsapi.UpdatedStack{ID: "infra", ULID: h.Stack},
	}
	body, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	out, err := w.service.Targets(t.Context(), outbox.Event{
		Account: w.f.acct.ULID, ID: kernel.NewULID(), Kind: runsapi.UpdatedKind,
		SchemaVersion: runsapi.UpdatedVersion, Subject: outbox.Subject{Kind: "run", ID: h.ID.String()}, Sequence: 3, Document: body,
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (w *commentWorld) enqueue(t *testing.T, targets []vcsapi.FeedbackTarget) {
	t.Helper()
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		for _, target := range targets {
			if err := w.service.Enqueue(ctx, target); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func (w *commentWorld) commentJob(t *testing.T) queue.Job {
	t.Helper()
	jobs, err := w.jobs.Claim(t.Context(), queue.Deliveries, []string{feedback.ReportComment.Name}, 1, time.Minute, "comments")
	if err != nil || len(jobs) != 1 {
		t.Fatalf("comment claim %+v: %v", jobs, err)
	}
	return jobs[0]
}

func TestAccCommentsRecoverAndPersistWithCompletion(t *testing.T) {
	w := newCommentWorld(t)
	targets := w.targets(t)
	if len(targets) != 1 {
		t.Fatal("missing eligible comment", targets)
	}
	w.enqueue(t, targets)
	w.fake.FailNextCommentCreate()
	j := w.commentJob(t)
	if err := w.service.Commented(t.Context(), j); !errors.Is(err, feedback.ErrCommentReport) {
		t.Fatal("lost-create response not observed", err)
	}
	if got := w.fake.Comments("acme", "infra", "7"); len(got) != 1 {
		t.Fatal("fake did not preserve uncertain native effect", got)
	}
	var records int
	if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM vcs_pr_comments WHERE reference->>'id' IS NOT NULL`).Scan(&records); err != nil || records != 0 {
		t.Fatal("failed effect acknowledged local identity", records, err)
	}
	// The same still-held claim can retry its native lookup after rollback.
	if err := w.service.Commented(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	if err := w.jobs.Complete(t.Context(), j); !errors.Is(err, queue.ErrLeaseLost) {
		t.Fatal("comment reference and queue completion did not commit together", err)
	}
	got := w.fake.Comments("acme", "infra", "7")
	if len(got) != 1 || !strings.Contains(got[0].Body, "<details>") || !strings.Contains(got[0].Body, w.facts.log) ||
		!port.HasCommentMarker(got[0].Body, "plan:"+w.facts.history.ID.String()) {
		t.Fatal("recovery duplicated or altered the planning body", got)
	}
	// Queue uniqueness alone is insufficient: a completed event replay creates
	// a new claim, but its durable mapping prevents another provider write.
	w.enqueue(t, targets)
	if err := w.service.Commented(t.Context(), w.commentJob(t)); err != nil {
		t.Fatal(err)
	}
	if writes := w.fake.CommentWrites("acme", "infra", "7"); len(writes) != 1 {
		t.Fatal("completed event replay repeated the native effect", writes)
	}
}

func TestAccCommentsCurrentSharedHeadsAndAccountIsolation(t *testing.T) {
	w := newCommentWorld(t)
	second := w.request
	second.ID, second.Number = kernel.NewULID(), "8"
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error { return w.f.store.Upsert(ctx, second) }); err != nil {
		t.Fatal(err)
	}
	targets := w.targets(t)
	if len(targets) != 2 {
		t.Fatal("shared heads did not select both requests", targets)
	}
	if found, err := w.f.store.CommentPullRequests(t.Context(), kernel.NewAccountID(), w.request.Integration, "acme", "infra", w.request.HeadSHA); err != nil || len(found) != 0 {
		t.Fatal("foreign account read requests", found, err)
	}
	if _, err := w.f.store.CommentPullRequest(t.Context(), kernel.NewAccountID(), w.request.ID); !errors.Is(err, feedback.ErrNoComment) {
		t.Fatal("foreign local request identity", err)
	}
	w.enqueue(t, targets)
	if _, err := w.f.db.Exec(t.Context(), `UPDATE pull_requests SET head_sha=$2 WHERE id=$1`, second.ID, strings.Repeat("b", 40)); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := w.service.Commented(t.Context(), w.commentJob(t)); err != nil {
			t.Fatal(err)
		}
	}
	if len(w.fake.Comments("acme", "infra", "7")) != 1 || len(w.fake.Comments("acme", "infra", "8")) != 0 {
		t.Fatal("a moved current head received a queued plan")
	}
	read, err := w.f.store.CommentPullRequests(t.Context(), w.f.acct, w.request.Integration, "acme", "infra", w.request.HeadSHA)
	if err != nil || len(read) != 1 || read[0].ID != w.request.ID {
		t.Fatal("historical head re-entered current-head results", read, err)
	}
}

func TestAccCommentPolicyUpsertAndPurge(t *testing.T) {
	w := newCommentWorld(t)
	input := vcsapi.PullRequestComment{PullRequest: w.request.ID, DedupKey: "policy:notice", Body: "initial notice"}
	update := func(acct kernel.AccountID) error {
		return tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error { return w.service.Upsert(ctx, acct, input) })
	}
	if err := w.service.Upsert(t.Context(), w.f.acct, input); !errors.Is(err, queue.ErrNoTransaction) {
		t.Fatal("untransactional policy enqueue", err)
	}
	if err := update(w.f.acct); err != nil {
		t.Fatal(err)
	}
	if err := w.service.Commented(t.Context(), w.commentJob(t)); err != nil {
		t.Fatal(err)
	}
	input.Body = "updated notice"
	if err := update(w.f.acct); err != nil {
		t.Fatal(err)
	}
	if err := w.service.Commented(t.Context(), w.commentJob(t)); err != nil {
		t.Fatal(err)
	}
	got := w.fake.Comments("acme", "infra", "7")
	if len(got) != 1 || !strings.Contains(got[0].Body, "updated notice") || got[0].Minimized {
		t.Fatal("policy update created another comment or minimized it", got)
	}
	input.DedupKey = "plan:" + kernel.NewULID().String()
	if err := update(w.f.acct); !errors.Is(err, vcsapi.ErrCommentInput) {
		t.Fatal("policy overwrote reserved plan namespace", err)
	}
	if done, err := w.service.PurgeComments(t.Context(), kernel.NewAccountID(), 1); err != nil || !done {
		t.Fatal("foreign purge", done, err)
	}
	if done, err := w.service.PurgeComments(t.Context(), w.f.acct, 1); err != nil || done {
		t.Fatal("first bounded purge", done, err)
	}
	if done, err := w.service.PurgeComments(t.Context(), w.f.acct, 1); err != nil || !done {
		t.Fatal("idempotent purge", done, err)
	}
	if writes := w.fake.CommentWrites("acme", "infra", "7"); len(writes) != 2 || writes[1].Action != "update" {
		t.Fatal("native edit route was not exercised")
	}
}

func TestAccCommentsRollbackAndSupersededPlanOrdering(t *testing.T) {
	w := newCommentWorld(t)
	targets := w.targets(t)
	stop := errors.New("caller refused event completion")
	err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		if err := w.service.Enqueue(ctx, targets[0]); err != nil {
			return err
		}
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}
	for _, table := range []string{"vcs_pr_comments", "jobs"} {
		var count int
		if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatal("rolled back enqueue left metadata or job", table, count, err)
		}
	}
	first := w.facts.history.ID
	w.enqueue(t, targets)
	if err := w.service.Commented(t.Context(), w.commentJob(t)); err != nil {
		t.Fatal(err)
	}
	// A newer run posts another native comment. GitLab keeps the prior native
	// comment visible; the local handled bit records its successful no-op.
	w.facts.history.ID = kernel.NewULID()
	w.facts.history.CreatedAt = w.facts.history.CreatedAt.Add(time.Minute)
	newest := w.facts.history.ID
	w.enqueue(t, w.targets(t))
	if err := w.service.Commented(t.Context(), w.commentJob(t)); err != nil {
		t.Fatal(err)
	}
	// A delayed older run must supersede itself rather than minimize the newer
	// run that already reported. Chronology is run creation, not job arrival.
	w.facts.history.ID = kernel.NewULID()
	w.facts.history.CreatedAt = w.facts.history.CreatedAt.Add(-30 * time.Second)
	delayed := w.facts.history.ID
	w.enqueue(t, w.targets(t))
	if err := w.service.Commented(t.Context(), w.commentJob(t)); err != nil {
		t.Fatal(err)
	}
	for _, run := range []kernel.ULID{first, newest, delayed} {
		var minimized bool
		var nativeID string
		if err := w.f.db.QueryRow(t.Context(), `SELECT minimized,reference->>'id' FROM vcs_pr_comments WHERE account_id=$1 AND run_id=$2`, w.f.acct, run).
			Scan(&minimized, &nativeID); err != nil || nativeID == "" || minimized != (run != newest) {
			t.Fatal("incorrect supersession or lost native reference", run, minimized, nativeID, err)
		}
	}
	if got := w.fake.Comments("acme", "infra", "7"); len(got) != 3 || got[0].Minimized || got[1].Minimized || got[2].Minimized {
		t.Fatal("unsupported provider minimization changed native history", got)
	}
}
