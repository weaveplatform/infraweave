package push

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
)

type eventUse struct {
	use   stacksapi.Use
	found bool
	err   error
}

func (u eventUse) EventUse(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.Use, bool, error) {
	return u.use, u.found, u.err
}

type repositoryStacks struct {
	stacksapi.Stacks
	rows  []stacksapi.Stack
	calls int
	err   error
}

func (s *repositoryStacks) VCSRepositories(context.Context, kernel.AccountID, stacksapi.Use) ([]stacksapi.VCSRepository, error) {
	if s.err != nil || len(s.rows) == 0 {
		return nil, s.err
	}
	return []stacksapi.VCSRepository{{Namespace: "acme", Name: "infra"}}, nil
}

func (s *repositoryStacks) ByVCSRepository(context.Context, kernel.AccountID, stacksapi.Use, string, string) ([]stacksapi.Stack, error) {
	s.calls++
	return s.rows, s.err
}

type pushSource struct {
	t         *testing.T
	calls     []string
	tags      []string
	files     []string
	err       error
	wrongHead bool
}

func (s *pushSource) PrepareTag(ctx context.Context, _ kernel.AccountID, _ kernel.ULID, _ string, event vcsapi.Event) (stacksapi.Commit, error) {
	if tx.In(ctx) {
		s.t.Fatal("tag lookup held a transaction")
	}
	if _, ok := ctx.Deadline(); !ok {
		s.t.Fatal("tag lookup has no delivery budget")
	}
	s.tags = append(s.tags, event.Head)
	return stacksapi.Commit{Hash: event.Head, Tag: event.Tag}, s.err
}

func (s *pushSource) PreparePush(ctx context.Context, _ kernel.AccountID, _ kernel.ULID, _ string, event vcsapi.Event) (stacksapi.Commit, []string, error) {
	s.t.Helper()
	if tx.In(ctx) {
		s.t.Fatal("provider work held a transaction")
	}
	if _, ok := ctx.Deadline(); !ok {
		s.t.Fatal("provider work has no delivery budget")
	}
	s.calls = append(s.calls, event.Branch)
	head := event.Head
	if s.wrongHead {
		head = "different"
	}
	return stacksapi.Commit{Hash: head}, s.files, s.err
}

func TestUnitPreparedMultiRefFanout(t *testing.T) {
	acct, integration := kernel.NewAccountID(), kernel.NewULID()
	stacks := &repositoryStacks{rows: []stacksapi.Stack{{ID: kernel.NewULID()}, {ID: kernel.NewULID()}}}
	source := &pushSource{t: t, files: []string{"terraform/main.tf"}}
	s := New(Deps{Stacks: stacks, Sources: source, Integrations: eventUse{use: stacksapi.Use{Integration: integration, Provider: "GITLAB", Default: true}, found: true}})
	delivery := intake.Delivery{Account: acct, ID: kernel.NewULID(), Integration: integration, Provider: "GITLAB", Deadline: time.Now().Add(time.Minute)}
	repository := vcsapi.Repository{ID: "one", Namespace: "acme", Name: "infra"}
	events := []vcsapi.Event{
		{Type: "push", Repository: repository, Branch: "main", Head: strings.Repeat("1", 40)},
		{Type: "push", Repository: repository, Branch: "feature", Head: strings.Repeat("2", 40)},
		{Type: "tag", Repository: repository, Tag: "v1", Head: strings.Repeat("1", 40)},
		{Type: "comment", Repository: repository},
	}
	prepared, err := s.Prepare(t.Context(), delivery, events)
	if err != nil {
		t.Fatal(err)
	}
	plan := prepared.(*plan) //nolint:forcetypeassert // this package's concrete dispatcher result
	if stacks.calls != 0 || !slices.Equal(source.calls, []string{"main", "feature"}) || len(plan.events) != 3 {
		t.Fatal("refs were dropped, source work repeated per stack, or targets selected before commit")
	}
	if plan.events[0].Commit.Branch != "main" || plan.events[1].Commit.Branch != "feature" || plan.events[2].Event.Tag != "v1" || plan.events[2].Commit.Hash != events[2].Head ||
		plan.events[2].Commit.Tag != "v1" ||
		len(source.tags) != 1 {
		t.Fatal("prepared event order or source branches changed")
	}
	source.files[0] = "mutated"
	events[0].Branch = "mutated"
	if plan.events[0].Files[0] != "terraform/main.tf" || plan.events[0].Event.Branch != "main" {
		t.Fatal("prepared facts retained caller-owned mutable data")
	}
	jobs := &preparedJobs{}
	s.d.Jobs, s.d.Store = jobs, preparedStore{}
	if err := tx.Do(t.Context(), evaluationDB{}, tx.Options{}, plan.Commit); err != nil {
		t.Fatal(err)
	}
	if stacks.calls != 1 || len(jobs.inputs) != 2 {
		t.Fatal("committed fanout lost/duplicated targets", stacks.calls, jobs.inputs)
	}
	if err := plan.Commit(t.Context()); !errors.Is(err, queue.ErrNoTransaction) {
		t.Fatal("prepared work committed outside the processor transaction", err)
	}
}

func TestUnitPreparedSourceGuards(t *testing.T) {
	for _, name := range []string{"no users", "deleted integration", "stack failure", "integration failure", "source failure", "wrong head", "wrong provider"} {
		t.Run(name, func(t *testing.T) {
			cause := errors.New("provider failed")
			stacks := &repositoryStacks{rows: []stacksapi.Stack{{ID: kernel.NewULID()}}}
			source := &pushSource{t: t}
			use := eventUse{use: stacksapi.Use{Provider: "GITLAB"}, found: true}
			switch name {
			case "no users":
				stacks.rows = nil
			case "deleted integration":
				use.found = false
			case "stack failure":
				stacks.err = cause
			case "integration failure":
				use.err = cause
			case "source failure":
				source.err = cause
			case "wrong head":
				source.wrongHead = true
			case "wrong provider":
				use.use.Provider = "OTHER"
			}
			s := New(Deps{Stacks: stacks, Sources: source, Integrations: use})
			plan, err := s.Prepare(
				t.Context(),
				intake.Delivery{Provider: "GITLAB", Deadline: time.Now().Add(time.Minute)},
				[]vcsapi.Event{{Type: "push", Head: strings.Repeat("1", 40)}},
			)
			if err == nil && plan != nil {
				s.d.Store, s.d.Jobs = preparedStore{}, &preparedJobs{}
				err = tx.Do(t.Context(), evaluationDB{}, tx.Options{}, plan.Commit)
			}
			wantError := name != "no users" && name != "deleted integration"
			if (err != nil) != wantError {
				t.Fatal("source guard hid or fabricated failure", err)
			}
			if (name == "deleted integration" || name == "no users") && len(source.calls) != 0 {
				t.Fatal("unneeded provider request")
			}
		})
	}
}

func TestUnitPreparationNoop(t *testing.T) {
	s := New(Deps{})
	for _, event := range []vcsapi.Event{{Type: "comment"}, {Type: "pull_request"}, {Type: "push", Head: strings.Repeat("0", 40)}, {Type: "tag", Head: strings.Repeat("0", 40)}} {
		if plan, err := s.Prepare(t.Context(), intake.Delivery{Deadline: time.Now().Add(time.Minute)}, []vcsapi.Event{event}); err != nil || plan != nil {
			t.Fatal("non-actionable event performed work", plan, err)
		}
	}
	if plan, err := s.Prepare(t.Context(), intake.Delivery{Deadline: time.Now().Add(-time.Minute)}, []vcsapi.Event{{Type: "push"}}); err != nil || plan != nil {
		t.Fatal("expired preparation read a dependency", plan, err)
	}
}

// Preparation performs provider reads only; commit owns the affected-stack
// snapshot, after the native identity map has repointed any renamed users.
type preparedJobs struct {
	intake.Jobs
	inputs []Evaluation
}

func (j *preparedJobs) EnqueueTx(ctx context.Context, _ queue.Kind, payload any, _ ...queue.Option) (int64, error) {
	if !tx.In(ctx) {
		return 0, queue.ErrNoTransaction
	}
	j.inputs = append(j.inputs, payload.(Evaluation))
	return int64(len(j.inputs)), nil
}

type preparedStore struct{ Store }

func (preparedStore) PutPrepared(context.Context, kernel.AccountID, kernel.ULID, []Prepared) error {
	return nil
}

func (u eventUse) HeldEventUse(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (stacksapi.Use, bool, error) {
	if !tx.In(ctx) {
		return stacksapi.Use{}, false, queue.ErrNoTransaction
	}
	return u.EventUse(ctx, acct, id)
}
