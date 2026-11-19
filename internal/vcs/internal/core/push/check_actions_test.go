package push

import (
	"context"
	"errors"
	"strings"
	"testing"

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
	err            error
	mutate         func()
}

func (a *actionSources) PrepareCheckAction(ctx context.Context, _ kernel.AccountID, _ kernel.ULID, _ string, event vcsapi.Event) (port.ActionFacts, error) {
	if tx.In(ctx) {
		a.t.Fatal("native preparation inside transaction")
	}
	return port.ActionFacts{Commit: port.ActionCommit{Hash: event.Head}, NativeHost: "https://github.example/api/v3/app/1"}, a.err
}

func (a *actionSources) RefreshCheckAction(ctx context.Context, _ kernel.AccountID, _ kernel.ULID, _ string, _ vcsapi.Event, _ port.ActionFacts) (bool, error) {
	if tx.In(ctx) {
		a.t.Fatal("permission read inside transaction")
	}
	a.refresh++
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
