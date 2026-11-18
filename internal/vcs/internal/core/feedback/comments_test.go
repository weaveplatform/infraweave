package feedback

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type commentFacts struct {
	*deploymentFacts
	CommentStore
	requests []CommentPullRequest
	readErr  error
}

func (f *commentFacts) CommentPullRequests(_ context.Context, acct kernel.AccountID, binding kernel.ULID, namespace, repository, sha string) ([]CommentPullRequest, error) {
	if acct.IsZero() || binding != f.binding || namespace != f.stack.Namespace || repository != f.stack.Repository || sha != f.history.CommitHash {
		return nil, errors.New("comment selection escaped its binding")
	}
	return slices.Clone(f.requests), f.readErr
}

type unusedComments struct{ port.Comments }

func commentTargetWorld(t *testing.T) (*Service, *commentFacts, outbox.Event) {
	t.Helper()
	_, base, event := deploymentTargetWorld(t)
	base.history.Transitions = []runsapi.RunTransition{
		{Sequence: 1, State: kernel.RunStateQueued}, {Sequence: 2, State: kernel.RunStatePlanning}, {Sequence: 3, State: kernel.RunStateUnconfirmed},
	}
	base.labels = []string{"feature:add_plan_pr_comment"}
	f := &commentFacts{deploymentFacts: base, requests: []CommentPullRequest{
		{
			ID: kernel.NewULID(), Integration: base.binding, RepositoryID: "one", Number: "7", Namespace: base.stack.Namespace,
			Repository: base.stack.Repository, Head: base.history.CommitHash, Open: true, Provider: base.stack.Provider,
		},
		{
			ID: kernel.NewULID(), Integration: base.binding, RepositoryID: "one", Number: "8", Namespace: base.stack.Namespace,
			Repository: base.stack.Repository, Head: base.history.CommitHash, Open: true, Provider: base.stack.Provider,
		},
	}}
	s := New(nil, nil).WithComments(CommentDeps{
		Store: f, Histories: f, Stacks: f, Integrations: f,
		Providers: map[string]port.Comments{base.stack.Provider: unusedComments{}},
	})
	return s, f, event
}

func TestUnitPlanCommentTargets(t *testing.T) {
	s, f, event := commentTargetWorld(t)
	for _, kind := range []kernel.RunType{kernel.RunTypeTracked, kernel.RunTypeProposed} {
		for _, state := range []kernel.RunState{kernel.RunStateUnconfirmed, kernel.RunStateFinished, kernel.RunStateFailed} {
			f.history.Type = kind
			f.history.Transitions[2].State = state
			var update runsapi.Updated
			if err := json.Unmarshal(event.Document, &update); err != nil {
				t.Fatal(err)
			}
			update.State, update.Run.State, update.Run.Type = state, state, kind
			event.Document, _ = json.Marshal(update) //nolint:errcheck // fixed serializable fixture
			targets, err := s.Targets(t.Context(), event)
			if err != nil || len(targets) != 2 {
				t.Fatalf("%s/%s: %d targets, %v", kind, state, len(targets), err)
			}
			for i, target := range targets {
				var c Comment
				if err := json.Unmarshal(target.Payload, &c); err != nil || !validComment(c) || c.PlanVersion != 2 ||
					c.Run != f.history.ID || c.Target.ID != f.requests[i].ID || c.State != state || c.Body != "" ||
					target.Job != ReportComment.Name || target.Account != event.Account || target.EventID != event.ID {
					t.Fatalf("invalid target %+v / %+v: %v", target, c, err)
				}
			}
		}
	}
}

func TestUnitPlanCommentTargetRefusals(t *testing.T) {
	for name, alter := range map[string]func(*commentFacts, *outbox.Event){
		"no label":          func(f *commentFacts, _ *outbox.Event) { f.labels = nil },
		"partial label":     func(f *commentFacts, _ *outbox.Event) { f.labels = []string{"feature:add_plan_pr_comment-extra"} },
		"local workspace":   func(f *commentFacts, _ *outbox.Event) { f.history.Local = true },
		"missing run":       func(f *commentFacts, _ *outbox.Event) { f.failure = runsapi.ErrHistoryNotFound },
		"detached":          func(f *commentFacts, _ *outbox.Event) { f.stack.VCSDetached = true },
		"missing binding":   func(f *commentFacts, _ *outbox.Event) { f.binding = kernel.ULID{} },
		"not planning":      func(f *commentFacts, _ *outbox.Event) { f.history.Transitions[1].State = kernel.RunStateInitializing },
		"wrong sequence":    func(f *commentFacts, _ *outbox.Event) { f.history.Through++ },
		"different commit":  func(f *commentFacts, _ *outbox.Event) { f.history.CommitHash = "" },
		"no open heads":     func(f *commentFacts, _ *outbox.Event) { f.requests = nil },
		"different subject": func(_ *commentFacts, e *outbox.Event) { e.Subject.ID = kernel.NewULID().String() },
		"schema":            func(_ *commentFacts, e *outbox.Event) { e.SchemaVersion++ },
		"event kind":        func(_ *commentFacts, e *outbox.Event) { e.Kind = "other" },
		"malformed":         func(_ *commentFacts, e *outbox.Event) { e.Document = []byte(`{`) },
	} {
		t.Run(name, func(t *testing.T) {
			s, f, event := commentTargetWorld(t)
			alter(f, &event)
			if targets, err := s.Targets(t.Context(), event); err != nil || len(targets) != 0 {
				t.Fatalf("refused target = %+v, %v", targets, err)
			}
		})
	}
}

func TestUnitPlanCommentTargetErrors(t *testing.T) {
	failure := errors.New("owned fact read failed")
	for _, alter := range []func(*commentFacts){
		func(f *commentFacts) { f.failure = failure }, func(f *commentFacts) { f.stackError = failure },
		func(f *commentFacts) { f.labelsError = failure }, func(f *commentFacts) { f.bindingError = failure },
		func(f *commentFacts) { f.readErr = failure },
	} {
		s, f, event := commentTargetWorld(t)
		alter(f)
		if targets, err := s.Targets(t.Context(), event); !errors.Is(err, failure) || len(targets) != 0 {
			t.Fatalf("lost fact error: %+v, %v", targets, err)
		}
	}
}
