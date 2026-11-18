package feedback

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type checkTargetFacts struct {
	*deploymentFacts
	CheckStore
	runsapi.CheckFacts
	run                 runsapi.CheckRun
	policy              port.CheckPolicy
	kept                CheckBinding
	runError, hostError error
}

func (f *checkTargetFacts) Run(context.Context, kernel.AccountID, kernel.ULID) (runsapi.CheckRun, error) {
	return f.run, f.runError
}

func (f *checkTargetFacts) Binding(context.Context, kernel.AccountID, string, *kernel.ULID) (port.Integration, bool, error) {
	return port.Integration{
		ID:         f.binding,
		Provider:   "GITHUB_ENTERPRISE",
		Settings:   port.Settings{AppID: "1"},
		Checks:     f.policy,
		Credential: []byte("must-not-reach-check-payload"),
	}, true, f.bindingError
}

func (f *checkTargetFacts) EventUse(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.Use, bool, error) {
	return stacksapi.Use{}, false, nil
}

func (f *checkTargetFacts) CheckHost(port.Settings) (string, error) {
	return "https://github.example/api/v3/app/1", f.hostError
}

func (*checkTargetFacts) Set(context.Context, port.Repository, string, port.CheckReport) (string, error) {
	panic("target selection made an outbound effect")
}

func (f *checkTargetFacts) CheckBinding(context.Context, kernel.AccountID, string) (CheckBinding, error) {
	if f.kept.Key == "" {
		return CheckBinding{}, ErrNoCheck
	}
	return f.kept, nil
}

func (*checkTargetFacts) ResolveCheckRepository(_ context.Context, b CheckBinding) (CheckBinding, error) {
	return b, nil
}

func checkTargetWorld(t *testing.T) (*Service, *checkTargetFacts, outbox.Event) {
	t.Helper()
	_, base, event := deploymentTargetWorld(t)
	f := &checkTargetFacts{deploymentFacts: base, policy: port.CheckPolicy{Mode: "INDIVIDUAL"}, run: runsapi.CheckRun{
		ID:        base.history.ID,
		Stack:     base.history.Stack,
		Type:      base.history.Type,
		State:     kernel.RunStateUnconfirmed,
		Sequence:  3,
		CommitSHA: base.history.CommitHash,
		Branch:    "main",
		CreatedAt: base.history.CreatedAt,
	}}
	s := New(queue.New(nil), nil).WithChecks(CheckDeps{Store: f, Facts: f, Histories: f, Stacks: f, Integrations: f, Providers: map[string]CheckProviders{"GITHUB_ENTERPRISE": f}})
	return s, f, event
}

func TestUnitCommitCheckTargetsAndModes(t *testing.T) {
	for _, tc := range []struct {
		mode string
		kind kernel.RunType
		want []string
	}{
		{"INDIVIDUAL", kernel.RunTypeTracked, []string{"infraweave/infra/tracked"}},
		{"INDIVIDUAL", kernel.RunTypeProposed, []string{"infraweave/infra"}},
		{"AGGREGATED", kernel.RunTypeProposed, []string{"infraweave/tracked", "infraweave/proposed", "infraweave/modules"}},
		{"ALL", kernel.RunTypeTracked, []string{"infraweave/infra/tracked", "infraweave/tracked", "infraweave/proposed", "infraweave/modules"}},
	} {
		t.Run(tc.mode+string(tc.kind), func(t *testing.T) {
			s, f, event := checkTargetWorld(t)
			f.policy.Mode = tc.mode
			f.run.Type = tc.kind
			f.history.Type = tc.kind
			var update runsapi.Updated
			if err := json.Unmarshal(event.Document, &update); err != nil {
				t.Fatal(err)
			}
			update.Run.Type = tc.kind
			event.Document, _ = json.Marshal(update)
			targets, err := s.Targets(t.Context(), event)
			if err != nil || len(targets) != len(tc.want) {
				t.Fatal(targets, err)
			}
			for i, target := range targets {
				var request CheckRequest
				if json.Unmarshal(target.Payload, &request) != nil || request.Name != tc.want[i] || request.Binding.Run != f.run.ID || request.Binding.Stack != f.run.Stack ||
					request.Binding.Commit != f.run.CommitSHA ||
					request.Binding.Integration != f.binding ||
					request.Binding.Policy.Mode != tc.mode ||
					request.Sequence != event.Sequence ||
					target.Account != event.Account ||
					target.EventID != event.ID ||
					target.Job != ReportCheck.Name ||
					strings.Contains(string(target.Payload), "must-not-reach") {
					t.Fatal("check target lost identity/mode or retained credential", target, request)
				}
			}
		})
	}
}

func TestUnitCommitCheckTargetEligibility(t *testing.T) {
	for name, change := range map[string]func(*checkTargetFacts, *outbox.Event){
		"raw Git":                func(f *checkTargetFacts, _ *outbox.Event) { f.stack.Provider = "GIT" },
		"detached":               func(f *checkTargetFacts, _ *outbox.Event) { f.stack.VCSDetached = true },
		"local workspace":        func(f *checkTargetFacts, _ *outbox.Event) { f.run.Local = true },
		"drift":                  func(f *checkTargetFacts, _ *outbox.Event) { f.run.DriftDetection = true },
		"task":                   func(f *checkTargetFacts, _ *outbox.Event) { f.run.Type = kernel.RunTypeTask },
		"destroy":                func(f *checkTargetFacts, _ *outbox.Event) { f.run.Type = kernel.RunTypeDestroy },
		"noncommit":              func(f *checkTargetFacts, _ *outbox.Event) { f.run.CommitSHA = "" },
		"missing run":            func(f *checkTargetFacts, _ *outbox.Event) { f.runError = runsapi.ErrCheckRunNotFound },
		"different stack":        func(f *checkTargetFacts, _ *outbox.Event) { f.run.Stack = kernel.NewULID() },
		"future event":           func(f *checkTargetFacts, _ *outbox.Event) { f.run.Sequence = 2 },
		"wrong historical phase": func(f *checkTargetFacts, _ *outbox.Event) { f.history.Transitions[1].State = kernel.RunStateFinished },
		"unknown phase":          func(f *checkTargetFacts, _ *outbox.Event) { f.run.State = kernel.RunStateUnknown },
		"foreign subject":        func(_ *checkTargetFacts, e *outbox.Event) { e.Subject.ID = kernel.NewULID().String() },
		"wrong kind":             func(_ *checkTargetFacts, e *outbox.Event) { e.Kind = "other" },
		"wrong schema":           func(_ *checkTargetFacts, e *outbox.Event) { e.SchemaVersion++ },
		"malformed body":         func(_ *checkTargetFacts, e *outbox.Event) { e.Document = []byte("{") },
	} {
		t.Run(name, func(t *testing.T) {
			s, f, event := checkTargetWorld(t)
			change(f, &event)
			targets, err := s.Targets(t.Context(), event)
			if err != nil || len(targets) != 0 {
				t.Fatal("ineligible identity produced checks", targets, err)
			}
		})
	}
	for _, phase := range []string{"run facts", "history", "stack", "binding", "host"} {
		t.Run(phase, func(t *testing.T) {
			s, f, event := checkTargetWorld(t)
			cause := errors.New("dependency unavailable")
			want := cause
			switch phase {
			case "run facts":
				f.runError = cause
			case "history":
				f.failure = cause
			case "stack":
				f.stackError = cause
			case "binding":
				f.bindingError = cause
			case "host":
				f.hostError = errors.New("private-host-marker")
				want = ErrCheckReport
			}
			_, err := s.Targets(t.Context(), event)
			if !errors.Is(err, want) || strings.Contains(err.Error(), "private-host-marker") {
				t.Fatal("dependency was hidden or provider input leaked", err)
			}
		})
	}
}

func TestUnitNativeCheckResourceOwnership(t *testing.T) {
	b := CheckBinding{
		Key:        checkBindingKey(kernel.NewULID()),
		Provider:   "GITHUB_ENTERPRISE",
		NativeHost: "https://github.example/app/1",
		Namespace:  "acme",
		Repository: "infra",
		Commit:     strings.Repeat("a", 40),
		Slug:       "infra",
		Type:       kernel.RunTypeTracked,
	}
	other := b
	other.Key = checkBindingKey(kernel.NewULID())
	other.Integration = kernel.NewULID()
	if checkResourceKey(b, individualName(b), false) == checkResourceKey(other, individualName(other), false) {
		t.Fatal("GitHub runs share an individual native resource")
	}
	if checkResourceKey(b, "infraweave/tracked", true) != checkResourceKey(other, "infraweave/tracked", true) {
		t.Fatal("GitHub aggregate split by run")
	}
	for _, provider := range []string{"GITLAB", "BITBUCKET_CLOUD", "BITBUCKET_DATACENTER", "AZURE_DEVOPS"} {
		b.Provider = provider
		other.Provider = provider
		if checkResourceKey(b, individualName(b), false) != checkResourceKey(other, individualName(other), false) {
			t.Fatal("native shared status split across runs/integrations", provider)
		}
	}
	other.Commit = strings.Repeat("b", 40)
	if checkResourceKey(b, individualName(b), false) == checkResourceKey(other, individualName(other), false) {
		t.Fatal("different commits share a resource")
	}
}

func TestUnitCommitCheckReservedNames(t *testing.T) {
	for _, slug := range []string{"tracked", "proposed", "modules"} {
		for _, provider := range []string{"GITHUB_ENTERPRISE", "GITLAB", "BITBUCKET_CLOUD", "BITBUCKET_DATACENTER", "AZURE_DEVOPS"} {
			for _, mode := range []string{"INDIVIDUAL", "ALL"} {
				t.Run(slug+"/"+provider+"/"+mode, func(t *testing.T) {
					s, f, event := checkTargetWorld(t)
					f.run.Type, f.history.Type = kernel.RunTypeProposed, kernel.RunTypeProposed
					f.run.State = kernel.RunStateFailed
					var update runsapi.Updated
					if err := json.Unmarshal(event.Document, &update); err != nil {
						t.Fatal(err)
					}
					update.Run.Type = kernel.RunTypeProposed
					event.Document, _ = json.Marshal(update)
					b, err := s.selectCheck(t.Context(), kernel.AccountID{ULID: event.Account}, f.run)
					if err != nil {
						t.Fatal(err)
					}
					b.Slug, b.Provider, b.Policy.Mode = slug, provider, mode
					f.kept = b
					targets, err := s.Targets(t.Context(), event)
					want := 1
					if mode == "ALL" {
						want = 3
						if provider == "GITHUB_ENTERPRISE" {
							want = 4
						}
					}
					if err != nil || len(targets) != want {
						t.Fatal(targets, err)
					}
					classes := map[string]bool{}
					for _, target := range targets {
						var r CheckRequest
						if json.Unmarshal(target.Payload, &r) != nil {
							t.Fatal("invalid request")
						}
						if r.Name == "infraweave/"+slug {
							classes[checkClass(r.Aggregate)] = true
						}
					}
					if mode == "ALL" && (!classes["aggregate"] || classes["individual"] != (provider == "GITHUB_ENTERPRISE")) {
						t.Fatal("native fixed-name collision lost aggregate precedence", classes)
					}
					if mode == "INDIVIDUAL" && (!classes["individual"] || classes["aggregate"]) {
						t.Fatal("individual mode lost its mandated name", classes)
					}
				})
			}
		}
	}
}

func TestUnitGitHubNativeResourceCaseAliases(t *testing.T) {
	b := CheckBinding{
		Provider:   "GITHUB_ENTERPRISE",
		NativeHost: "https://github.example/api/v3/app/42",
		Namespace:  "acme",
		Repository: "infra",
		Commit:     strings.Repeat("a", 40),
		Key:        "run:first",
	}
	alias := b
	alias.Namespace = "Acme"
	alias.Repository = "Infra"
	if checkResourceKey(b, "infraweave/proposed", true) != checkResourceKey(alias, "infraweave/proposed", true) {
		t.Fatal("GitHub path casing split one native aggregate fence")
	}
	for _, provider := range []string{"GITLAB", "BITBUCKET_CLOUD", "BITBUCKET_DATACENTER", "AZURE_DEVOPS"} {
		b.Provider = provider
		alias.Provider = provider
		if checkResourceKey(b, "infraweave/proposed", true) == checkResourceKey(alias, "infraweave/proposed", true) {
			t.Fatal("provider path case was incorrectly discarded", provider)
		}
	}
}

func TestUnitCommitCheckNativeIdentitySurvivesRepositoryRename(t *testing.T) {
	_, f, _ := checkTargetWorld(t)
	b := CheckBinding{Provider: "GITLAB", NativeHost: "https://gitlab.example", RepositoryID: "7", Namespace: "acme", Repository: "infra", Commit: f.run.CommitSHA}
	renamed := b
	renamed.Namespace, renamed.Repository = "new-owner", "renamed"
	if checkResourceKey(b, "infraweave/proposed", true) != checkResourceKey(renamed, "infraweave/proposed", true) {
		t.Fatal("rename split native resource ownership")
	}
	renamed.RepositoryID = "8"
	if checkResourceKey(b, "infraweave/proposed", true) == checkResourceKey(renamed, "infraweave/proposed", true) {
		t.Fatal("different native repositories shared an ownership fence")
	}
}
