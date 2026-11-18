package feedback

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type deploymentFacts struct {
	stacksapi.Stacks
	history      runsapi.RunHistory
	stack        stacksapi.Settings
	labels       []string
	binding      kernel.ULID
	stored       Deployment
	failure      error
	stackError   error
	labelsError  error
	bindingError error
	storeError   error
	reports      []port.DeploymentState
	reportError  error
	useError     error
	useFound     bool
}

func (f *deploymentFacts) HistoryAt(context.Context, kernel.AccountID, kernel.ULID, int64) (runsapi.RunHistory, error) {
	return f.history, f.failure
}

func (f *deploymentFacts) Settings(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.Settings, error) {
	return f.stack, f.stackError
}

func (f *deploymentFacts) Labels(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID][]string, error) {
	return map[kernel.ULID][]string{f.stack.ID: slices.Clone(f.labels)}, f.labelsError
}

func (f *deploymentFacts) Binding(context.Context, kernel.AccountID, string, *kernel.ULID) (port.Integration, bool, error) {
	return port.Integration{ID: f.binding}, !f.binding.IsZero(), f.bindingError
}

func (f *deploymentFacts) Use(context.Context, kernel.AccountID, string, *kernel.ULID) (port.Integration, bool, error) {
	return port.Integration{}, f.useFound, f.useError
}

func (f *deploymentFacts) Report(_ context.Context, _ port.Repository, _ port.DeploymentStack, _ port.DeploymentRun, state port.DeploymentState) error {
	f.reports = append(f.reports, state)
	return f.reportError
}

func (f *deploymentFacts) Deployment(context.Context, kernel.AccountID, kernel.ULID) (Deployment, error) {
	if f.storeError != nil {
		return Deployment{}, f.storeError
	}
	if f.stored.Run.ID.IsZero() {
		return Deployment{}, ErrNoDeployment
	}
	return f.stored, nil
}

type deploymentScopedFacts struct {
	*deploymentFacts
	err error
}

func (f deploymentScopedFacts) DeploymentHost(port.Settings) (string, error) {
	return "https://gitlab.example.com/api/v4/", f.err
}

func (*deploymentFacts) BindDeployment(context.Context, Deployment) (Deployment, error) {
	panic("target selection must not write")
}

func (*deploymentFacts) RequestDeployment(context.Context, DeploymentRequest) (bool, error) {
	panic("target selection must not write")
}

func (*deploymentFacts) ReserveDeployment(context.Context, Deployment, string) error {
	panic("target selection must not write")
}

func (*deploymentFacts) HoldDeployment(context.Context, kernel.AccountID, string) (DeploymentCursor, error) {
	panic("target selection must not lock")
}

func (*deploymentFacts) ReportedDeployment(context.Context, kernel.AccountID, string, int64) error {
	panic("target selection must not write")
}

func (*deploymentFacts) PurgeDeployments(context.Context, kernel.AccountID, int) (bool, error) {
	return true, nil
}

func deploymentTargetWorld(t *testing.T) (*Service, *deploymentFacts, outbox.Event) {
	t.Helper()
	acct, run, stack := kernel.NewULID(), kernel.NewULID(), kernel.NewULID()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	f := &deploymentFacts{
		history: runsapi.RunHistory{
			ID: run, Stack: stack, Type: kernel.RunTypeTracked, CommitHash: strings.Repeat("a", 40), Branch: "main", CreatedAt: now, Through: 3,
			Transitions: []runsapi.RunTransition{{Sequence: 1, State: kernel.RunStateQueued, At: now}, {Sequence: 3, State: kernel.RunStateUnconfirmed, At: now.Add(time.Minute)}},
		},
		stack: stacksapi.Settings{
			Stack:      stacksapi.Stack{ID: stack, Account: kernel.AccountID{ULID: acct}, Slug: "infra"},
			Provider:   "GITHUB",
			Namespace:  "acme",
			Repository: "infra",
			Branch:     "main",
		},
		binding:  kernel.NewULID(),
		useFound: true,
	}
	u := runsapi.Updated{
		State: kernel.RunStateUnconfirmed,
		Run:   runsapi.UpdatedRun{ID: run, Type: kernel.RunTypeTracked, State: kernel.RunStateUnconfirmed, Commit: runsapi.UpdatedCommit{Hash: f.history.CommitHash}},
		Stack: runsapi.UpdatedStack{ID: "infra", ULID: stack},
	}
	body, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	e := outbox.Event{
		Account:       acct,
		ID:            kernel.NewULID(),
		Kind:          runsapi.UpdatedKind,
		SchemaVersion: runsapi.UpdatedVersion,
		Subject:       outbox.Subject{Kind: "run", ID: run.String()},
		Sequence:      3,
		Document:      body,
	}
	s := NewDeployments(queue.New(nil), nil, DeploymentDeps{Histories: f, Stacks: f, Integrations: f, Store: f, Providers: map[string]port.Deployments{"GITHUB": f, "GITLAB": f}})
	return s, f, e
}

func TestUnitDeploymentTargets(t *testing.T) {
	t.Parallel()
	s, f, event := deploymentTargetWorld(t)
	targets, err := s.Targets(t.Context(), event)
	if err != nil || len(targets) != 1 {
		t.Fatalf("targets=%+v err=%v", targets, err)
	}
	var request DeploymentRequest
	if err := json.Unmarshal(targets[0].Payload, &request); err != nil {
		t.Fatal(err)
	}
	if !validDeployment(request) || targets[0].Job != ReportDeployment.Name || request.Sequence != 3 || request.Deployment.Run.ID != f.history.ID ||
		request.Deployment.Stack.Environment != "infraweave/infra" || !request.Deployment.Epoch.Equal(f.history.Transitions[1].At) || len(f.reports) != 0 {
		t.Fatalf("target %+v reports %v", request, f.reports)
	}
	f.labels = []string{"ghenv:production"}
	f.history.Tag = "v1"
	targets, err = s.Targets(t.Context(), event)
	if err != nil || len(targets) != 1 {
		t.Fatalf("tag target %v %v", targets, err)
	}
	if err := json.Unmarshal(targets[0].Payload, &request); err != nil {
		t.Fatal(err)
	}
	if !request.Deployment.Run.Tag || request.Deployment.Run.Ref != "v1" || request.Deployment.Stack.Environment != "production" {
		t.Fatalf("tag override %+v", request)
	}
	f.stored = request.Deployment
	f.stack.Provider = "GIT"
	f.labels = []string{"ghenv:-"}
	targets, err = s.Targets(t.Context(), event)
	if err != nil || len(targets) != 1 {
		t.Fatalf("recorded binding must survive later stack changes: %v %v", targets, err)
	}
	if err := json.Unmarshal(targets[0].Payload, &request); err != nil {
		t.Fatal(err)
	}
	if request.Deployment != f.stored {
		t.Fatalf("recorded binding changed: %+v", request.Deployment)
	}
}

func TestUnitDeploymentTargetExclusions(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]func(*deploymentFacts, *outbox.Event){
		"other kind":           func(_ *deploymentFacts, e *outbox.Event) { e.Kind = "other" },
		"other schema":         func(_ *deploymentFacts, e *outbox.Event) { e.SchemaVersion++ },
		"no account":           func(_ *deploymentFacts, e *outbox.Event) { e.Account = kernel.ULID{} },
		"no event":             func(_ *deploymentFacts, e *outbox.Event) { e.ID = kernel.ULID{} },
		"no sequence":          func(_ *deploymentFacts, e *outbox.Event) { e.Sequence = 0 },
		"bad json":             func(_ *deploymentFacts, e *outbox.Event) { e.Document = []byte("{") },
		"other subject":        func(_ *deploymentFacts, e *outbox.Event) { e.Subject.ID = kernel.NewULID().String() },
		"proposed":             func(f *deploymentFacts, _ *outbox.Event) { f.history.Type = kernel.RunTypeProposed },
		"local":                func(f *deploymentFacts, _ *outbox.Event) { f.history.Local = true },
		"other stack":          func(f *deploymentFacts, _ *outbox.Event) { f.history.Stack = kernel.NewULID() },
		"other commit":         func(f *deploymentFacts, _ *outbox.Event) { f.history.CommitHash = "other" },
		"future history":       func(f *deploymentFacts, _ *outbox.Event) { f.history.Through++ },
		"no reached phase":     func(f *deploymentFacts, _ *outbox.Event) { f.history.Transitions = nil },
		"wrong state":          func(f *deploymentFacts, _ *outbox.Event) { f.history.Transitions[1].State = kernel.RunStateApplying },
		"unsupported provider": func(f *deploymentFacts, _ *outbox.Event) { f.stack.Provider = "BITBUCKET_CLOUD" },
		"detached":             func(f *deploymentFacts, _ *outbox.Event) { f.stack.VCSDetached = true },
		"disabled label":       func(f *deploymentFacts, _ *outbox.Event) { f.labels = []string{"ghenv:-"} },
		"no binding":           func(f *deploymentFacts, _ *outbox.Event) { f.binding = kernel.ULID{} },
		"missing history":      func(f *deploymentFacts, _ *outbox.Event) { f.failure = runsapi.ErrHistoryNotFound },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s, f, e := deploymentTargetWorld(t)
			change(f, &e)
			got, err := s.Targets(t.Context(), e)
			if err != nil || len(got) != 0 || len(f.reports) != 0 {
				t.Fatalf("targets=%+v err=%v reports=%v", got, err, f.reports)
			}
		})
	}
	s, f, e := deploymentTargetWorld(t)
	f.failure = errors.New("database offline")
	if _, err := s.Targets(t.Context(), e); !errors.Is(err, f.failure) {
		t.Fatalf("database error=%v", err)
	}
}

func TestUnitDeploymentResourceIdentity(t *testing.T) {
	t.Parallel()
	d := Deployment{
		Integration: kernel.NewULID(),
		Provider:    "GITLAB",
		NativeHost:  "https://gitlab.example.com/api/v4/",
		Namespace:   "acme",
		Repository:  "infra",
		Stack:       port.DeploymentStack{Environment: "production"},
		Run:         port.DeploymentRun{ID: kernel.NewULID(), Ref: "main", Commit: "sha"},
	}
	other := d
	other.Run.ID = kernel.NewULID()
	if resourceKey(d) != resourceKey(other) {
		t.Fatal("GitLab native identity invented run field")
	}
	other.Integration = kernel.NewULID()
	if resourceKey(d) != resourceKey(other) {
		t.Fatal("credential bindings split the same native resource")
	}
	other.NativeHost = "https://other.example.com/api/v4/"
	if resourceKey(d) == resourceKey(other) {
		t.Fatal("distinct GitLab hosts share resource")
	}
	other = d
	other.NativeHost = "https://gitlab.example.com/nested/api/v4/"
	if resourceKey(d) == resourceKey(other) {
		t.Fatal("distinct GitLab installation paths share resource")
	}
	other = d
	other.Run.Ref = "other"
	if resourceKey(d) == resourceKey(other) {
		t.Fatal("GitLab ref omitted")
	}
	d.Provider = "GITHUB"
	other = d
	other.Run.ID = kernel.NewULID()
	if resourceKey(d) == resourceKey(other) {
		t.Fatal("GitHub per-run identity omitted")
	}
}

func TestUnitDeploymentProviderBoundary(t *testing.T) {
	t.Parallel()
	s, f, event := deploymentTargetWorld(t)
	targets, err := s.Targets(t.Context(), event)
	if err != nil {
		t.Fatal(err)
	}
	var r DeploymentRequest
	if err := json.Unmarshal(targets[0].Payload, &r); err != nil {
		t.Fatal(err)
	}
	f.reportError = errors.New("provider leaked token-secret")
	if err := s.reportDeployment(t.Context(), r.Deployment, r.State); !errors.Is(err, ErrDeploymentReport) || strings.Contains(err.Error(), "token-secret") {
		t.Fatalf("unsafe diagnostic %v", err)
	}
	f.reportError = &port.RateLimited{Reset: time.Now().Add(time.Minute)}
	if err := s.reportDeployment(t.Context(), r.Deployment, r.State); err == nil || errors.Is(err, ErrDeploymentReport) {
		t.Fatalf("rate limit not snoozed: %v", err)
	}
	f.reportError = nil
	if err := s.reportDeployment(t.Context(), r.Deployment, kernel.RunStateApplying); err != nil || f.reports[len(f.reports)-1] != port.DeploymentInProgress {
		t.Fatalf("report=%v states=%v", err, f.reports)
	}
	f.useFound = false
	before := len(f.reports)
	if err := s.reportDeployment(t.Context(), r.Deployment, r.State); err != nil || len(f.reports) != before {
		t.Fatalf("deleted binding fallback: %v %v", err, f.reports)
	}
	f.useError = errors.New("credential-secret")
	if err := s.reportDeployment(t.Context(), r.Deployment, r.State); !errors.Is(err, ErrDeploymentReport) {
		t.Fatalf("credential error=%v", err)
	}
	if err := s.Reported(t.Context(), queue.Job{}); err == nil {
		t.Fatal("invalid job accepted")
	}
}

func TestUnitDeploymentFactFailures(t *testing.T) {
	t.Parallel()
	down := errors.New("fact reader unavailable")
	for name, change := range map[string]func(*Service, *deploymentFacts){
		"stored binding":      func(_ *Service, f *deploymentFacts) { f.storeError = down },
		"stack":               func(_ *Service, f *deploymentFacts) { f.stackError = down },
		"labels":              func(_ *Service, f *deploymentFacts) { f.labelsError = down },
		"integration binding": func(_ *Service, f *deploymentFacts) { f.bindingError = down },
		"native host": func(s *Service, f *deploymentFacts) {
			f.stack.Provider = "GITLAB"
			s.deployments.Providers["GITLAB"] = deploymentScopedFacts{deploymentFacts: f, err: down}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s, f, event := deploymentTargetWorld(t)
			change(s, f)
			targets, err := s.Targets(t.Context(), event)
			if !errors.Is(err, down) || len(targets) != 0 || len(f.reports) != 0 {
				t.Fatalf("unavailable facts silently skipped or emitted effect: targets=%v error=%v reports=%v", targets, err, f.reports)
			}
		})
	}
}

func TestUnitDeploymentNativeScopeAndMissingStack(t *testing.T) {
	t.Parallel()
	s, f, event := deploymentTargetWorld(t)
	f.stack.Provider = "GITLAB"
	f.history.Branch = ""
	s.deployments.Providers["GITLAB"] = deploymentScopedFacts{deploymentFacts: f}
	targets, err := s.Targets(t.Context(), event)
	if err != nil || len(targets) != 1 {
		t.Fatalf("native target %+v %v", targets, err)
	}
	var r DeploymentRequest
	if err := json.Unmarshal(targets[0].Payload, &r); err != nil {
		t.Fatal(err)
	}
	if r.Deployment.NativeHost != "https://gitlab.example.com/api/v4/" || r.Deployment.Run.Ref != f.stack.Branch || !validDeployment(r) {
		t.Fatalf("native scope/ref %+v", r)
	}
	f.stackError = stacksapi.ErrNotFound
	if targets, err := s.Targets(t.Context(), event); err != nil || len(targets) != 0 {
		t.Fatalf("deleted stack target %+v %v", targets, err)
	}
}
