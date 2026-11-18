package push

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jackc/pgx/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
)

type evaluationDB struct{}

func (evaluationDB) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	return evaluationTx{}, nil
}

type evaluationTx struct{ pgx.Tx }

func (evaluationTx) Commit(context.Context) error   { return nil }
func (evaluationTx) Rollback(context.Context) error { return nil }

type evaluationWorld struct {
	t         *testing.T
	s         *Service
	input     Evaluation
	job       queue.Job
	stack     stacksapi.Settings
	meta      EventContext
	events    []Prepared
	order     []string
	created   map[string]bool
	requests  []runsapi.CreateRequest
	heads     []string
	tracked   []string
	cap       int
	used      int
	complete  int
	holdErr   error
	createErr error
	found     bool
	delay     time.Duration
}

type evaluationStore struct{ w *evaluationWorld }

func (s evaluationStore) PutPrepared(context.Context, kernel.AccountID, kernel.ULID, []Prepared) error {
	return nil
}

func (s evaluationStore) Prepared(context.Context, kernel.AccountID, kernel.ULID) ([]Prepared, error) {
	return s.w.events, nil
}

func (s evaluationStore) EventContext(context.Context, kernel.AccountID, kernel.ULID) (EventContext, bool, error) {
	return s.w.meta, s.w.found, nil
}

type evaluationLimit struct {
	LimitStore
	w *evaluationWorld
}

func (s evaluationLimit) RunLimit(context.Context, kernel.AccountID) (*int, error) {
	return &s.w.cap, nil
}

func (s evaluationLimit) ReserveRun(ctx context.Context, _ kernel.AccountID, _ kernel.ULID, limit int) (bool, error) {
	s.w.step(ctx, "admit")
	if s.w.used == limit {
		return false, nil
	}
	s.w.used++
	return true, nil
}

type evaluationStacks struct {
	stacksapi.Stacks
	w *evaluationWorld
}

func (s evaluationStacks) Settings(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.Settings, error) {
	return s.w.stack, nil
}

func (s evaluationStacks) LockForRun(ctx context.Context, _ kernel.AccountID, _ kernel.ULID) (stacksapi.Settings, error) {
	s.w.step(ctx, "stack lock")
	return s.w.stack, nil
}

func (s evaluationStacks) SetTracked(ctx context.Context, _ kernel.AccountID, _ kernel.ULID, head, tracked *stacksapi.Commit, setter string) error {
	s.w.step(ctx, "projection")
	if head != nil {
		s.w.heads = append(s.w.heads, head.Hash)
	}
	if tracked != nil {
		if setter != "infraweave" {
			s.w.t.Fatal("tracked commit lost its source")
		}
		s.w.tracked = append(s.w.tracked, tracked.Hash)
	}
	return nil
}

type evaluationSpaces struct {
	accessapi.Spaces
	w *evaluationWorld
}

func (s evaluationSpaces) Hold(ctx context.Context, _ kernel.AccountID, _ kernel.ULID) error {
	s.w.step(ctx, "owner hold")
	return nil
}

type evaluationJobs struct {
	intake.Jobs
	w *evaluationWorld
}

func (j evaluationJobs) HoldTx(ctx context.Context, _ queue.Job) error {
	j.w.step(ctx, "fence")
	return j.w.holdErr
}

func (j evaluationJobs) CompleteTx(ctx context.Context, _ queue.Job) error {
	j.w.step(ctx, "complete")
	j.w.complete++
	return j.w.holdErr
}

type evaluationRuns struct{ w *evaluationWorld }

func (r evaluationRuns) Supersede(ctx context.Context, _ kernel.AccountID, _ kernel.ULID, _, _ string) (int, error) {
	r.w.step(ctx, "supersede")
	if !slices.Contains(r.w.order, "stack lock") {
		r.w.t.Fatal("supersede acquired run locks before stack creation lock")
	}
	return 0, nil
}

func (r evaluationRuns) Create(ctx context.Context, in runsapi.CreateRequest) (runsapi.CreatedRun, error) {
	r.w.step(ctx, "create")
	r.w.requests = append(r.w.requests, in)
	if in.Mode != runsapi.OncePerCommit || in.TriggeredBy != "vcs/commit" || in.Commit.Branch == "" {
		r.w.t.Fatal("event creation lost lifecycle policy or branch")
	}
	if r.w.createErr != nil {
		return runsapi.CreatedRun{}, r.w.createErr
	}
	if r.w.created[in.Commit.Hash] {
		return runsapi.CreatedRun{ID: kernel.NewULID()}, nil
	}
	time.Sleep(r.w.delay) //nolint:forbidigo // nonzero only inside the virtual-time deadline test
	if err := in.BeforeCreate(ctx); err != nil {
		return runsapi.CreatedRun{}, err
	}
	r.w.created[in.Commit.Hash] = true
	return runsapi.CreatedRun{ID: kernel.NewULID(), Created: true}, nil
}

func (w *evaluationWorld) step(ctx context.Context, name string) {
	w.t.Helper()
	if !tx.In(ctx) {
		w.t.Fatal("evaluation escaped its transaction", name)
	}
	w.order = append(w.order, name)
}

func newEvaluation(t *testing.T) *evaluationWorld {
	t.Helper()
	w := &evaluationWorld{t: t, created: map[string]bool{}, cap: 1, found: true}
	w.input = Evaluation{Account: kernel.NewAccountID(), Stack: kernel.NewULID(), Delivery: kernel.NewULID(), Deadline: time.Now().Add(time.Minute)}
	id := kernel.NewULID()
	w.stack = stacksapi.Settings{ID: w.input.Stack, Space: w.input.Account.ULID, Namespace: "acme", Repository: "infra", Provider: "GITLAB", Branch: "main", VCSIntegration: &id}
	w.meta = EventContext{Integration: id, Provider: "GITLAB", State: intake.Dispatched, Deadline: w.input.Deadline}
	payload, err := json.Marshal(w.input)
	if err != nil {
		t.Fatal(err)
	}
	w.job = queue.Job{Payload: payload, Account: w.input.Account.ULID}
	w.s = New(
		Deps{
			DB:           evaluationDB{},
			Store:        evaluationStore{w},
			Stacks:       evaluationStacks{w: w},
			Spaces:       evaluationSpaces{w: w},
			Integrations: eventUse{use: stacksapi.Use{Integration: id, Provider: "GITLAB"}, found: true},
			Creator:      evaluationRuns{w},
			Supersede:    evaluationRuns{w},
			Jobs:         evaluationJobs{w: w},
			Limits:       &Limits{Store: evaluationLimit{w: w}, Ceiling: 500},
		},
	)
	return w
}

func (w *evaluationWorld) push(branch, hash string, files ...string) Prepared {
	return Prepared{
		Event:  vcsapi.Event{Type: "push", Branch: branch, Head: hash, Repository: vcsapi.Repository{Namespace: "acme", Name: "infra"}},
		Commit: stacksapi.Commit{Hash: hash, Branch: branch},
		Files:  files,
	}
}

func TestUnitEvaluationBatchAdmission(t *testing.T) {
	w := newEvaluation(t)
	one, two := strings.Repeat("1", 40), strings.Repeat("2", 40)
	w.events = []Prepared{w.push("main", one, "main.tf"), w.push("main", two, "main.tf"), w.push("main", one, "main.tf")}
	if err := w.s.Evaluated(t.Context(), w.job); err != nil {
		t.Fatal(err)
	}
	if len(w.created) != 1 || !w.created[one] || w.used != 1 || w.complete != 1 || len(w.requests) != 3 {
		t.Fatal("later cap/duplicate discarded earlier admission", w.created, w.used, w.complete)
	}
	if !slices.Equal(w.heads, []string{one, two, one}) || !slices.Equal(w.tracked, []string{one, one}) {
		t.Fatal("cap changed tracked commit or lost branch head", w.heads, w.tracked)
	}
	if w.order[0] != "fence" || w.order[len(w.order)-1] != "complete" {
		t.Fatal("evaluation effects escaped queue fence", w.order)
	}
}

func TestUnitEvaluationDecisions(t *testing.T) {
	for _, name := range []string{"other branch", "unaffected", "tag", "refused", "unexpected failure", "stale fence", "purged", "detached"} {
		t.Run(name, func(t *testing.T) {
			w := newEvaluation(t)
			root := "root"
			w.stack.ProjectRoot = &root
			w.events = []Prepared{w.push("main", strings.Repeat("1", 40), "root/main.tf")}
			switch name {
			case "other branch":
				w.events[0].Event.Branch = "feature"
				w.events[0].Commit.Branch = "feature"
			case "unaffected":
				w.events[0].Files = []string{"outside.txt"}
			case "tag":
				w.events[0].Event.Type = "tag"
			case "refused":
				w.createErr = runsapi.ErrCreationRefused
			case "unexpected failure":
				w.createErr = errors.New("insert failed")
			case "stale fence":
				w.holdErr = queue.ErrLeaseLost
			case "purged":
				w.found = false
			case "detached":
				w.stack.VCSDetached = true
			}
			err := w.s.Evaluated(t.Context(), w.job)
			fails := name == "unexpected failure" || name == "stale fence"
			if (err != nil) != fails {
				t.Fatal("wrong settlement", err)
			}
			if name == "other branch" && (len(w.requests) != 1 || w.requests[0].Type != kernel.RunTypeProposed || len(w.tracked) != 0 || len(w.heads) != 0) {
				t.Fatal("untracked push did not propose independently")
			}
			if name != "other branch" && (len(w.tracked) != 0 || w.used != 0) {
				t.Fatal("nonadmitted push changed tracked commit or cap")
			}
			if name == "unaffected" && len(w.heads) != 1 {
				t.Fatal("unaffected tracked push did not advance branch head")
			}
			if name == "stale fence" && len(w.order) != 1 {
				t.Fatal("stale claim performed business work", w.order)
			}
		})
	}
}

func TestUnitEvaluationDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newEvaluation(t)
		w.events = []Prepared{w.push("main", strings.Repeat("1", 40), "main.tf")}
		w.delay = 2 * time.Minute
		if err := w.s.Evaluated(t.Context(), w.job); err != nil || w.complete != 1 || w.used != 0 || len(w.created) != 0 {
			t.Fatal("deadline allowed a new run or kept retrying", err)
		}
	})
}

func TestUnitEvaluationPayloadAndExpiry(t *testing.T) {
	for _, name := range []string{"invalid JSON", "wrong account", "already expired", "received delivery", "lost binding", "foreign repository"} {
		t.Run(name, func(t *testing.T) {
			w := newEvaluation(t)
			w.events = []Prepared{w.push("main", strings.Repeat("1", 40), "main.tf")}
			switch name {
			case "invalid JSON":
				w.job.Payload = []byte("{")
			case "wrong account":
				w.job.Account = kernel.NewULID()
			case "already expired":
				w.s.d.Now = func() time.Time { return w.input.Deadline }
				w.s.d.Store = nil
				w.s.d.Stacks = nil
			case "received delivery":
				w.meta.State = intake.Received
			case "lost binding":
				w.s.d.Integrations = eventUse{found: false}
			case "foreign repository":
				w.events[0].Event.Repository.Namespace = "another-account"
			}
			err := w.s.Evaluated(t.Context(), w.job)
			invalid := name == "invalid JSON" || name == "wrong account"
			if (err != nil) != invalid || w.used != 0 || len(w.requests) != 0 || len(w.heads) != 0 {
				t.Fatal("invalid/expired work reached creation or projection", err)
			}
			if !invalid && w.complete != 1 {
				t.Fatal("expired work kept retrying")
			}
		})
	}
}
