package push

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
)

type reviewCurrentRepository struct{ current vcsapi.Repository }

func (l *reviewCurrentRepository) EventRepository(context.Context, kernel.AccountID, kernel.ULID, time.Time, vcsapi.Repository) (vcsapi.Repository, error) {
	return l.current, nil
}

func (*reviewCurrentRepository) Apply(context.Context, kernel.AccountID, kernel.ULID, stacksapi.Use, time.Time, []vcsapi.Event) error {
	return nil
}

type reviewRenamedStacks struct {
	stacksapi.Stacks
	current *reviewCurrentRepository
	stack   kernel.ULID
}

func (s reviewRenamedStacks) Settings(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.Settings, error) {
	return stacksapi.Settings{ID: s.stack, Namespace: s.current.current.Namespace, Repository: s.current.current.Name}, nil
}

func (s reviewRenamedStacks) VCSRepositories(context.Context, kernel.AccountID, stacksapi.Use) ([]stacksapi.VCSRepository, error) {
	return []stacksapi.VCSRepository{{Namespace: s.current.current.Namespace, Name: s.current.current.Name}}, nil
}

func (s reviewRenamedStacks) ByVCSRepository(_ context.Context, _ kernel.AccountID, _ stacksapi.Use, namespace, name string) ([]stacksapi.Stack, error) {
	if namespace == s.current.current.Namespace && name == s.current.current.Name {
		return []stacksapi.Stack{{ID: s.stack}}, nil
	}
	return nil, nil
}

func TestUnitReviewRenameBetweenPrepareAndCommitStillFansOut(t *testing.T) {
	acct, integration, stack := kernel.NewAccountID(), kernel.NewULID(), kernel.NewULID()
	old := vcsapi.Repository{ID: "7", Namespace: "acme", Name: "before"}
	lifecycle := &reviewCurrentRepository{current: old}
	jobs := &preparedJobs{}
	source := &pushSource{t: t}
	s := New(Deps{
		Lifecycle: lifecycle, Stacks: reviewRenamedStacks{current: lifecycle, stack: stack},
		Sources: source, Jobs: jobs, Store: preparedStore{},
		Integrations: eventUse{use: stacksapi.Use{Integration: integration, Provider: "GITLAB", Default: true}, found: true},
	})
	delivery := intake.Delivery{Account: acct, ID: kernel.NewULID(), Integration: integration, Provider: "GITLAB", ReceivedAt: time.Now(), Deadline: time.Now().Add(time.Minute)}
	prepared, err := s.Prepare(t.Context(), delivery, []vcsapi.Event{{Type: "push", Repository: old, Branch: "main", Head: strings.Repeat("1", 40)}})
	if err != nil {
		t.Fatal(err)
	}
	// Another delivery completes a newer rename after provider preparation
	// but before this delivery acquires the integration/completion fence.
	lifecycle.current.Name = "after"
	if err := tx.Do(t.Context(), evaluationDB{}, tx.Options{}, prepared.Commit); err != nil {
		t.Fatal(err)
	}
	if len(jobs.inputs) != 1 || jobs.inputs[0].Stack != stack {
		t.Fatalf("push missed the concurrently renamed stack: children=%v", jobs.inputs)
	}
	if len(source.calls) != 1 {
		t.Fatal("commit repeated provider preparation", source.calls)
	}
}

func (l *reviewCurrentRepository) FanoutRepository(_ context.Context, _ kernel.AccountID, _ kernel.ULID, _ time.Time, repo vcsapi.Repository) (vcsapi.Repository, bool, error) {
	return l.current, true, nil
}
