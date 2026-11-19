package push

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type actionSources struct {
	t              *testing.T
	write, current bool
	refresh        int
	seen           []vcsapi.Repository
	err            error
	mutate         func()
}

func (a *actionSources) PrepareCheckAction(ctx context.Context, _ kernel.AccountID, _ kernel.ULID, _ string, event vcsapi.Event) (port.ActionFacts, error) {
	if tx.In(ctx) {
		a.t.Fatal("native preparation inside transaction")
	}
	return port.ActionFacts{Commit: port.ActionCommit{Hash: event.Head}, NativeHost: "https://github.example/api/v3/app/1"}, a.err
}

func (a *actionSources) RefreshCheckAction(ctx context.Context, _ kernel.AccountID, _ kernel.ULID, _ string, event vcsapi.Event, _ port.ActionFacts) (bool, error) {
	if tx.In(ctx) {
		a.t.Fatal("permission read inside transaction")
	}
	a.refresh++
	a.seen = append(a.seen, event.Repository)
	if a.mutate != nil {
		a.mutate()
	}
	return a.write, a.err
}

func (a *actionSources) CurrentCheckAction(ctx context.Context, _ kernel.AccountID, _ kernel.ULID, _ string, _ vcsapi.Repository, _ port.ActionFacts) (bool, error) {
	if !tx.In(ctx) {
		a.t.Fatal("binding check outside completion")
	}
	return a.current, nil
}

type actionEffects struct{ done bool }

func (a *actionEffects) HasCheckAction(ctx context.Context, _ kernel.AccountID, _ kernel.ULID, _ kernel.ULID) (bool, error) {
	if !tx.In(ctx) {
		return false, ErrNoTransaction
	}
	return a.done, nil
}

func (a *actionEffects) RecordCheckAction(ctx context.Context, _ kernel.AccountID, _ kernel.ULID, _ kernel.ULID) error {
	if !tx.In(ctx) {
		return ErrNoTransaction
	}
	a.done = true
	return nil
}

type actionRuns struct{ w *evaluationWorld }

func (r actionRuns) Create(ctx context.Context, in runsapi.CreateRequest) (runsapi.CreatedRun, error) {
	r.w.step(ctx, "create")
	if in.Mode != runsapi.None || in.TriggeredBy != "github/developer" {
		r.w.t.Fatal("action lost explicit lifecycle policy", in.Mode, in.TriggeredBy)
	}
	if r.w.createErr != nil {
		return runsapi.CreatedRun{}, r.w.createErr
	}
	if err := in.BeforeCreate(ctx); err != nil {
		return runsapi.CreatedRun{}, err
	}
	r.w.requests = append(r.w.requests, in)
	return runsapi.CreatedRun{ID: kernel.NewULID(), Created: true}, nil
}

func newActionEvaluation(t *testing.T) (*evaluationWorld, *actionSources) {
	t.Helper()
	w := newEvaluation(t)
	w.stack.Account = w.input.Account
	w.stack.Provider, w.meta.Provider = "GITHUB_ENTERPRISE", "GITHUB_ENTERPRISE"
	w.stack.GithubActionDeploy = true
	w.s.d.Integrations = eventUse{use: stacksapi.Use{Integration: w.meta.Integration, Provider: w.meta.Provider}, found: true}
	a := &actionSources{t: t, write: true, current: true}
	w.s.d.Actions = a
	w.s.d.Effects = &actionEffects{}
	w.s.d.Creator = actionRuns{w}
	event := vcsapi.Event{
		Type:        "check_action",
		Action:      "requested_action",
		Head:        strings.Repeat("a", 40),
		Author:      "developer",
		Repository:  vcsapi.Repository{ID: "7", Namespace: "acme", Name: "infra"},
		CheckAction: &vcsapi.CheckAction{Action: "preview", ExternalID: w.input.Stack.String()},
	}
	facts, err := a.PrepareCheckAction(t.Context(), w.input.Account, w.meta.Integration, w.meta.Provider, event)
	if err != nil {
		t.Fatal(err)
	}
	w.events = []Prepared{{Event: event, Commit: stacksapi.Commit{Hash: event.Head}, Action: &facts}}
	return w, a
}

func TestUnitCheckActionCreationAndCurrentPermission(t *testing.T) {
	for _, action := range []string{"preview", "deploy"} {
		t.Run(action, func(t *testing.T) {
			w, a := newActionEvaluation(t)
			w.events[0].Event.CheckAction.Action = action
			if err := w.s.Evaluated(t.Context(), w.job); err != nil {
				t.Fatal(err)
			}
			want := kernel.RunTypeProposed
			if action == "deploy" {
				want = kernel.RunTypeTracked
			}
			if len(w.requests) != 1 || w.requests[0].Type != want || w.requests[0].Commit.Hash != w.events[0].Event.Head || a.refresh != 1 || w.complete != 1 || w.used != 1 ||
				len(w.heads) != 0 ||
				len(w.tracked) != 0 {
				t.Fatal("action did not preserve commit/pointers/admission", w.requests, a.refresh, w.heads, w.tracked, w.used)
			}
			for _, step := range w.order {
				if step == "supersede" || step == "projection" {
					t.Fatal("action borrowed push side effects", w.order)
				}
			}
		})
	}
	for _, scenario := range []string{"off", "off before lookup", "write revoked after dispatch", "source changed", "prepared replaced", "provider unavailable", "cap", "stack refusal"} {
		t.Run(scenario, func(t *testing.T) {
			w, a := newActionEvaluation(t)
			w.events[0].Event.CheckAction.Action = "deploy"
			var refused []string
			w.s.d.ActionRefused = func(_ context.Context, reason string) { refused = append(refused, reason) }
			switch scenario {
			case "off before lookup":
				w.stack.GithubActionDeploy = false
				a.err = errors.New("provider failure must stay unread")
			case "off":
				a.mutate = func() { w.stack.GithubActionDeploy = false }
			case "write revoked after dispatch":
				a.write = false
			case "source changed":
				a.current = false
			case "prepared replaced":
				a.mutate = func() { w.events[0].Event.Author = "other-sender" }
			case "provider unavailable":
				a.err = errors.New("private native failure")
			case "cap":
				w.used = w.cap
			case "stack refusal":
				w.createErr = runsapi.ErrCreationRefused
			}
			err := w.s.Evaluated(t.Context(), w.job)
			if scenario == "provider unavailable" {
				if err == nil || err.Error() != "vcs: check action preparation failed" || w.complete != 0 || len(refused) != 0 {
					t.Fatal("transient native failure leaked/settled/refused", err, w.complete, refused)
				}
			} else if err != nil || w.complete != 1 || len(refused) != 1 {
				t.Fatal("action refusal did not complete/count once", err, w.complete, refused)
			}
			if scenario == "off before lookup" && a.refresh != 0 {
				t.Fatal("disabled action read provider", a.refresh)
			}
			if len(w.requests) != 0 || len(w.heads) != 0 || len(w.tracked) != 0 {
				t.Fatal("refused press created/changed pointers", w.requests, w.heads, w.tracked)
			}
		})
	}
}

func TestUnitCheckActionTargetIsolation(t *testing.T) {
	for _, scenario := range []string{"valid", "foreign account", "foreign external ID", "slug", "other repository", "other namespace", "other integration", "nondefault", "detached", "other provider"} {
		t.Run(scenario, func(t *testing.T) {
			w, _ := newActionEvaluation(t)
			event := w.events[0].Event
			use := stacksapi.Use{Integration: w.meta.Integration, Provider: w.meta.Provider}
			switch scenario {
			case "foreign account":
				w.stack.Account = kernel.NewAccountID()
			case "foreign external ID":
				event.CheckAction.ExternalID = kernel.NewULID().String()
			case "slug":
				event.CheckAction.ExternalID = "infra"
			case "other repository":
				event.Repository.Name = "other"
			case "other namespace":
				event.Repository.Namespace = "other"
			case "other integration":
				id := kernel.NewULID()
				w.stack.VCSIntegration = &id
			case "nondefault":
				w.stack.VCSIntegration = nil
			case "detached":
				w.stack.VCSDetached = true
			case "other provider":
				w.stack.Provider = "GITLAB"
			}
			_, reason, err := w.s.actionTarget(t.Context(), intake.Delivery{Account: w.input.Account, Integration: w.meta.Integration, Provider: w.meta.Provider}, event, use)
			if err != nil || (reason == "") != (scenario == "valid") {
				t.Fatal("external ID scope", reason, err)
			}
		})
	}
}

func TestUnitCheckActionCompoundTargetsAndRetry(t *testing.T) {
	w, a := newActionEvaluation(t)
	foreign := w.events[0]
	other := *foreign.Event.CheckAction
	other.ExternalID = kernel.NewULID().String()
	foreign.Event.CheckAction = &other
	w.events = append(w.events, foreign)
	if err := w.s.Evaluated(t.Context(), w.job); err != nil || len(w.requests) != 1 || a.refresh != 1 {
		t.Fatal("one child acted on another stack's event", err, w.requests, a.refresh)
	}
}

func TestUnitCheckActionCurrentRepositoryProjection(t *testing.T) {
	for _, name := range []string{"case alias", "native rename"} {
		t.Run(name, func(t *testing.T) {
			w, a := newActionEvaluation(t)
			original := actionFingerprint(w.events[0])
			repo := w.events[0].Event.Repository
			if name == "case alias" {
				repo.Namespace = "ACME"
				repo.Name = "INFRA"
			} else {
				repo.Namespace = "new-owner"
				repo.Name = "new-name"
				w.stack.Namespace = repo.Namespace
				w.stack.Repository = repo.Name
			}
			w.s.d.Lifecycle = &reviewCurrentRepository{current: repo}
			if err := w.s.Evaluated(t.Context(), w.job); err != nil {
				t.Fatal(err)
			}
			if len(w.requests) != 1 || w.complete != 1 || len(a.seen) != 1 || a.seen[0] != repo || actionFingerprint(w.events[0]) != original {
				t.Fatal("native identity projection invalidated original authorization", w.requests, a.seen, w.complete)
			}
		})
	}
}

func TestUnitCheckActionNativeRetryAndDeadline(t *testing.T) {
	for _, name := range []string{"cancelled", "deadline retry", "deadline expires", "rate at final attempt", "rate after expiration"} {
		t.Run(name, func(t *testing.T) {
			w, a := newActionEvaluation(t)
			now := w.input.Deadline.Add(-10 * time.Second)
			w.s.d.Now = func() time.Time { return now }
			w.job.Attempt, w.job.MaxAttempts = 3, 3
			switch name {
			case "cancelled":
				a.err = fmt.Errorf("private native cancellation: %w", context.Canceled)
			case "deadline retry":
				a.err = fmt.Errorf("private native timeout: %w", context.DeadlineExceeded)
			case "deadline expires":
				a.err = context.DeadlineExceeded
				a.mutate = func() { now = w.input.Deadline }
			case "rate at final attempt":
				a.err = &port.RateLimited{Reset: now.Add(5 * time.Second)}
			case "rate after expiration":
				a.err = &port.RateLimited{Reset: now.Add(time.Minute)}
				a.mutate = func() { now = w.input.Deadline }
			}
			err := w.s.Evaluated(t.Context(), w.job)
			switch name {
			case "cancelled":
				if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "private") {
					t.Fatal("cancellation lost safe classification", err)
				}
			case "deadline retry":
				if !errors.Is(err, context.DeadlineExceeded) || w.complete != 0 {
					t.Fatal("native deadline acknowledged claim", err, w.complete)
				}
			case "deadline expires", "rate after expiration":
				if err != nil || w.complete != 1 {
					t.Fatal("expired work was rescheduled", err, w.complete)
				}
			case "rate at final attempt":
				if err == nil || err.Error() != "snoozed for 5s" || w.complete != 0 || w.failureMarkers != 0 {
					t.Fatal("rate limit consumed final attempt or expired delay", err, w.complete, w.failureMarkers)
				}
			}
			if len(w.requests) != 0 || w.used != 0 || len(w.heads) != 0 || len(w.tracked) != 0 {
				t.Fatal("native failure changed run/cap/pointers")
			}
		})
	}
}

func TestUnitCheckActionSuccessfulReplaySkipsNativeAuthorization(t *testing.T) {
	w, a := newActionEvaluation(t)
	w.s.d.Effects = &actionEffects{done: true}
	a.err = errors.New("native unavailable")
	w.stack.GithubActionDeploy = false
	if err := w.s.Evaluated(t.Context(), w.job); err != nil {
		t.Fatal(err)
	}
	if a.refresh != 0 || w.complete != 1 || len(w.requests) != 0 || w.used != 0 {
		t.Fatal("successful original intent repeated effects/authorization", a.refresh, w.complete, w.requests, w.used)
	}
}
