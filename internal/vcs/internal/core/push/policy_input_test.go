package push

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

type inputOwners struct {
	snapshot    stacksapi.PolicySnapshot
	space       accessapi.PolicySpace
	pool        workersapi.PoolMetadata
	roles       []accessapi.PolicyRole
	runs        []runsapi.PushRunFacts
	integration policyapi.VCSIntegrationFacts
	contexts    []string
	fault       string
	cause       error
	reads       []string
	input       policyapi.PushInputFacts
}

func TestUnitPushInputPrefersCompleteRecoveredFactsWithoutChangingSignedProvenance(t *testing.T) {
	s, owners, input, event, integration := newInputOwners(t)
	item := vcsapi.PushCommit{Hash: strings.Repeat("a", 40), At: time.Now(), Author: "Native author", Message: "Native message", Files: new([]string{"root/main.tf"})}
	event.Event.Commits = &vcsapi.CommitCollection{Source: "signed array", Omission: "provider-commit-limit", Items: []vcsapi.PushCommit{item}}
	event.RecoveredCommits = &vcsapi.CommitCollection{Source: "native REST", Items: []vcsapi.PushCommit{item}}
	if _, err := s.policyInput(t.Context(), input, event, integration); err != nil {
		t.Fatal(err)
	}
	commits := owners.input.Push.Value.Commits
	if !commits.Known || commits.Value == nil || len(*commits.Value) != 1 || (*commits.Value)[0].Commit.Hash.Value == nil || *(*commits.Value)[0].Commit.Hash.Value != item.Hash ||
		event.Event.Commits.Omission != "provider-commit-limit" {
		t.Fatal("available recovered facts were omitted or signed provenance changed", commits, event.Event.Commits)
	}
	for _, mode := range []string{"missing source", "partial range", "unknown files"} {
		t.Run(mode, func(t *testing.T) {
			bad := &vcsapi.CommitCollection{Source: "native REST", Items: []vcsapi.PushCommit{item}}
			switch mode {
			case "missing source":
				bad.Source = ""
			case "partial range":
				bad.Omission = "provider limit"
			case "unknown files":
				bad.Items[0].Files = nil
			}
			event.RecoveredCommits = bad
			if _, err := s.policyInput(t.Context(), input, event, integration); !errors.Is(err, kernel.ErrUnavailable) {
				t.Fatal("corrupt stored recovered facts became omitted optional input", err)
			}
		})
	}
}

func (o *inputOwners) read(name string) error {
	o.reads = append(o.reads, name)
	if o.fault == name {
		return o.cause
	}
	return nil
}

func (o *inputOwners) PolicySnapshot(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.PolicySnapshot, error) {
	return o.snapshot, o.read("stack")
}

func (o *inputOwners) PolicySpace(context.Context, kernel.AccountID, kernel.ULID) (accessapi.PolicySpace, error) {
	return o.space, o.read("space")
}

func (o *inputOwners) PoolMetadata(context.Context, kernel.AccountID, kernel.ULID) (workersapi.PoolMetadata, error) {
	return o.pool, o.read("pool")
}

func (o *inputOwners) StackRoles(context.Context, kernel.AccountID, kernel.ULID) ([]accessapi.PolicyRole, error) {
	return o.roles, o.read("roles")
}

func (o *inputOwners) PolicyContextSlugs(context.Context, kernel.AccountID, kernel.ULID) ([]string, error) {
	return o.contexts, o.read("contexts")
}

func (o *inputOwners) PushInProgress(context.Context, kernel.AccountID, kernel.ULID) ([]runsapi.PushRunFacts, error) {
	return o.runs, o.read("runs")
}

func (o *inputOwners) PolicyIntegration(context.Context, kernel.AccountID, kernel.ULID) (policyapi.VCSIntegrationFacts, error) {
	return o.integration, o.read("integration")
}

func (o *inputOwners) BuildPushInput(_ context.Context, in policyapi.PushInputFacts) (json.RawMessage, error) {
	o.input = in
	return json.RawMessage(`{}`), o.read("builder")
}

func newInputOwners(t *testing.T) (*Service, *inputOwners, Evaluation, Prepared, kernel.ULID) {
	t.Helper()
	account, id, integration, pool := kernel.NewAccountID(), kernel.NewULID(), kernel.NewULID(), kernel.NewULID()
	now := time.Now().UTC()
	owners := &inputOwners{
		cause: errors.New("owner backend failure"), snapshot: stacksapi.PolicySnapshot{
			Settings: stacksapi.Settings{
				ID: id, Slug: "infra", Name: "Infra", Space: account.ULID, Branch: "main", Namespace: "org", Repository: "repo",
				Vendor: "terraform", VendorConfig: json.RawMessage(`{"workflowTool":"OPEN_TOFU"}`), WorkerPool: &pool,
				ProjectRoot: new("root"), TerraformVersion: new(">=1.13"), Lock: &stacksapi.Lock{Holder: "policy:deploy", By: "deploy"},
				TrackedCommit: &stacksapi.Commit{Hash: "earlier", Timestamp: now, AuthorName: "Human", AuthorLogin: new("login")},
			},
			State: kernel.StackStateNone, Labels: []string{"production"},
		},
		space:    accessapi.PolicySpace{ID: account.ULID, Slug: "root", Name: "Root", Labels: []string{"space-label"}},
		pool:     workersapi.PoolMetadata{ID: pool, Name: "Private", Public: false, Labels: []string{"pool-label"}},
		roles:    []accessapi.PolicyRole{{ID: kernel.NewULID(), Slug: "operator", Name: "Operator"}},
		contexts: []string{"explicit", "automatic"}, integration: policyapi.VCSIntegrationFacts{ID: integration, Name: "Git", Provider: "GITLAB", CreatedAt: now, UpdatedAt: now},
		runs: []runsapi.PushRunFacts{{
			ID: kernel.NewULID(), Stack: id, Type: kernel.RunTypeTask, State: kernel.RunStateQueued, Version: 4,
			CreatedAt: now, UpdatedAt: now, TriggeredBy: new("person"), Branch: "main", Command: new("echo task"), BasedOnLocalWorkspace: true,
			DriftDetection: true, ForceApplyMode: "DISABLED", Flags: []string{"not-in-published-in-progress"},
			Commit: &runsapi.Commit{Hash: "task-head", Timestamp: now, AuthorName: "Human"},
		}},
	}
	s := New(Deps{
		PolicyInputs: owners, PolicyStacks: owners, PolicySpaces: owners, PolicyPools: owners, PolicyRoles: owners,
		PolicyContexts: owners, PolicyRuns: owners, PolicyIntegrations: owners,
	})
	input := Evaluation{Account: account, Stack: id}
	event := Prepared{
		Commit: stacksapi.Commit{
			Hash:        "head",
			Timestamp:   now,
			Branch:      "feature",
			AuthorName:  "Name",
			AuthorLogin: new("native-login"),
			Tag:         "v1",
			URL:         "https://git.example/commit/head",
		},
		Files:               []string{"root/main.tf"},
		AffectedStacksCount: new(int64(0)),
	}
	return s, owners, input, event, integration
}

func TestUnitPushInputMaterializesEveryAvailableOwnerFact(t *testing.T) {
	s, owners, input, event, integration := newInputOwners(t)
	if _, err := s.policyInput(t.Context(), input, event, integration); err != nil {
		t.Fatal(err)
	}
	f := owners.input
	if f.Account != input.Account || f.Stack.ID != input.Stack || f.Stack.State != kernel.StackStateNone || f.Stack.ProjectRoot != "root" ||
		f.Stack.Labels.Value == nil || !slices.Equal(*f.Stack.Contexts.Value, owners.contexts) || f.Stack.WorkerPool.Value == nil ||
		f.Stack.WorkerPool.Value.Public || f.Stack.Space.Value.ID != "root" || !slices.Equal(*f.Stack.Space.Value.Labels.Value, owners.space.Labels) ||
		*f.Stack.LockedBy.Value != "deploy" || *f.Stack.TerraformVersion.Value != ">=1.13" || *f.Stack.WorkflowTool.Value != "OPEN_TOFU" ||
		f.Stack.Roles.Value == nil || (*f.Stack.Roles.Value)[0].ID != owners.roles[0].ID || f.Stack.TrackedCommit.Value.Branch != "main" ||
		f.Stack.TrackedCommit.Value.Author != "login" || f.Push.Value.Author != "native-login" || f.Push.Value.AffectedStacksCount != 0 ||
		*f.Push.Value.Tag.Value != "v1" || f.InProgress.Value == nil || len(*f.InProgress.Value) != 1 || f.VCSIntegration.Value.ID != integration {
		t.Fatalf("owner facts were lost: %+v", f)
	}
	r := (*f.InProgress.Value)[0]
	if r.Command.Value == nil || *r.Command.Value != "echo task" || r.Commit.Value == nil || r.TriggeredBy.Value == nil ||
		!(*r.BasedOnLocalWorkspace.Value) || !(*r.DriftDetection.Value) || r.Flags.Known || r.RuntimeConfig.Known {
		t.Fatal("in-progress projection lost available facts or added fields absent from its published contract")
	}
	owners.snapshot.Settings.WorkerPool = nil
	owners.snapshot.Settings.Lock = nil
	owners.snapshot.Settings.TrackedCommit = nil
	owners.snapshot.Settings.TerraformVersion = nil
	owners.snapshot.Settings.ProjectRoot = nil
	owners.snapshot.Settings.Vendor = "ansible"
	owners.runs = []runsapi.PushRunFacts{{ID: kernel.NewULID(), CreatedAt: event.Commit.Timestamp, UpdatedAt: event.Commit.Timestamp}}
	owners.reads = nil
	if _, err := s.policyInput(t.Context(), input, event, integration); err != nil {
		t.Fatal(err)
	}
	f = owners.input
	if f.Stack.WorkerPool.Known || f.Stack.TerraformVersion.Known || f.Stack.WorkflowTool.Known || f.Stack.TrackedCommit.Value != nil || !f.Stack.TrackedCommit.Known ||
		f.Stack.LockedBy.Value != nil || !f.Stack.LockedBy.Known || slices.Contains(owners.reads, "pool") || !f.PullRequest.Known || f.PullRequest.Value != nil {
		t.Fatal("absent public-pool/future facts or known null owner facts were fabricated")
	}
}

func TestUnitPushInputPreservesOperationalOwnerFailures(t *testing.T) {
	for _, boundary := range []string{"stack", "space", "contexts", "roles", "pool", "integration", "runs", "builder"} {
		t.Run(boundary, func(t *testing.T) {
			s, owners, input, event, integration := newInputOwners(t)
			owners.fault = boundary
			if _, err := s.policyInput(t.Context(), input, event, integration); !errors.Is(err, owners.cause) || errors.Is(err, policyapi.ErrInput) {
				t.Fatalf("owner failure=%v", err)
			}
		})
	}
	for _, boundary := range []string{"inputs", "stack", "space", "contexts", "roles", "pool", "integration", "runs"} {
		t.Run("unbound "+boundary, func(t *testing.T) {
			s, _, input, event, integration := newInputOwners(t)
			switch boundary {
			case "inputs":
				s.d.PolicyInputs = nil
			case "stack":
				s.d.PolicyStacks = nil
			case "space":
				s.d.PolicySpaces = nil
			case "contexts":
				s.d.PolicyContexts = nil
			case "roles":
				s.d.PolicyRoles = nil
			case "pool":
				s.d.PolicyPools = nil
			case "integration":
				s.d.PolicyIntegrations = nil
			case "runs":
				s.d.PolicyRuns = nil
			}
			if _, err := s.policyInput(t.Context(), input, event, integration); !errors.Is(err, kernel.ErrUnavailable) {
				t.Fatalf("unbound owner failure=%v", err)
			}
		})
	}
	s, owners, input, event, integration := newInputOwners(t)
	event.AffectedStacksCount = nil
	if _, err := s.policyInput(t.Context(), input, event, integration); !errors.Is(err, policyapi.ErrInput) || len(owners.reads) != 0 {
		t.Fatal("older uncaptured count became a fabricated zero", err)
	}
	owners.snapshot.Settings.VendorConfig = json.RawMessage(`{"workflowTool":false}`)
	event.AffectedStacksCount = new(int64(1))
	if _, err := s.policyInput(t.Context(), input, event, integration); !errors.Is(err, policyapi.ErrInput) {
		t.Fatalf("corrupt materialized settings=%v", err)
	}
}

func TestUnitPushWorkflowToolFacts(t *testing.T) {
	for _, tc := range []struct{ vendor, settings, want string }{
		{"terraform", `{}`, "TERRAFORM_FOSS"},
		{"terraform", `{"workflowTool":"CUSTOM"}`, "CUSTOM"},
		{"opentofu", `{}`, "OPEN_TOFU"},
		{"opentofu", `{"workflowTool":"CUSTOM"}`, "CUSTOM"},
		{"terragrunt", `{}`, "TERRAFORM_FOSS"},
		{"terragrunt", `{"tool":"OPEN_TOFU"}`, "OPEN_TOFU"},
		{"ansible", `{}`, ""},
	} {
		t.Run(tc.vendor+tc.settings, func(t *testing.T) {
			got, err := workflowTool(stacksapi.Settings{Vendor: tc.vendor, VendorConfig: json.RawMessage(tc.settings)})
			if err != nil || got != tc.want {
				t.Fatalf("tool=%q error=%v", got, err)
			}
		})
	}
}
