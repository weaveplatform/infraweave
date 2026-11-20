package core

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

func known[T any](v T) policyapi.Fact[T] { return policyapi.Fact[T]{Known: true, Value: &v} }

func inputFacts() policyapi.RunInputFacts {
	at := time.Unix(1795140000, 123456789).UTC()
	return policyapi.RunInputFacts{
		Account: kernel.NewAccountID(), Type: policyapi.Plan, At: at,
		Run: policyapi.RunFacts{
			ID: kernel.NewULID(), Type: kernel.RunTypeTracked, State: kernel.RunStatePlanning,
			CreatedAt: at.Add(-time.Hour), UpdatedAt: at, TriggeredBy: policyapi.Fact[string]{Known: true},
			Commit: known(policyapi.CommitFacts{
				Author: "Alice", Branch: "main", Message: "change", At: at,
				Hash: known("abc"), Tag: known(""), URL: known("https://example.test/commit/abc"),
				ExistsOnTrackedBranch: policyapi.Fact[bool]{Known: true}, PullRequestID: known(int64(0)),
			}),
			Flags: known([]string{}), Branch: known("main"), BasedOnLocalWorkspace: known(false),
			DriftDetection: known(false), ForceApplyMode: known("DISABLED"), Command: policyapi.Fact[string]{Known: true},
		},
		Stack: policyapi.StackFacts{
			ID: kernel.NewULID(), Slug: "infra", Name: "Infrastructure", Branch: "main",
			Namespace: "org", Repository: "infra", ProjectRoot: "", State: kernel.StackStateNone,
			Labels: policyapi.Fact[[]string]{Known: true}, TerraformVersion: policyapi.Fact[string]{Known: true},
			WorkflowTool: known("TERRAFORM_FOSS"), AdditionalProjectGlobs: known([]string{}),
			LockedBy: policyapi.Fact[string]{Known: true}, TrackedCommit: policyapi.Fact[policyapi.CommitFacts]{Known: true},
			Space:      known(policyapi.SpaceFacts{ID: "root", Name: "Root", Labels: known([]string{})}),
			WorkerPool: known(policyapi.WorkerPoolFacts{ID: known("public"), Name: known("Public"), Public: true, Labels: known([]string{})}),
		},
	}
}

func TestRunInputBuilderPreservesTypedFactsAndPlanProjection(t *testing.T) {
	facts := inputFacts()
	previous := facts.Run
	previous.ID = kernel.NewULID()
	previous.Flags = known([]string{"reviewed"})
	facts.PreviousRun = known(previous)
	facts.Run.UserProvidedMetadata = known([]string{"build/42"})
	facts.Run.Changes = known(
		[]policyapi.ChangeFacts{{Phase: "plan", Action: "added", Address: "local_file.main", ResourceType: known("local_file"), Vendor: "terraform", Moved: known(false)}},
	)
	hooks := known([]string{"echo hook"})
	facts.Run.RuntimeConfig = known(policyapi.RuntimeConfigFacts{
		AfterApply: hooks, AfterDestroy: hooks, AfterInit: hooks, AfterPerform: hooks, AfterPlan: hooks, AfterRun: hooks,
		BeforeApply: hooks, BeforeDestroy: hooks, BeforeInit: hooks, BeforePerform: hooks, BeforePlan: hooks,
		GitSparseCheckoutPaths: policyapi.Fact[[]string]{Known: true}, Environment: known(map[string]string{"VALUE": "actual"}),
		ProjectRoot: "src", RunnerImage: "image:tag", TerraformVersion: policyapi.Fact[string]{Known: true},
	})
	facts.Plan = &policyapi.SanitizedPlan{
		Vendor:   "terraform",
		Document: json.RawMessage(`{"format_version":"1.2","terraform_version":"1.17.2","resource_changes":[],"variables":{"not_projected":{"value":"artifact-only"}}}`),
	}
	raw, err := (&Service{}).BuildRunInput(t.Context(), facts)
	if err != nil {
		t.Fatal(err)
	}
	var input inputs.PlanInput
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	c, _ := input.Infraweave.Get()
	r, _ := c.Run.Get()
	created, _ := r.CreatedAt.Get()
	request, _ := c.Request.Get()
	ns, _ := request.TimestampNS.Get()
	if created != facts.Run.CreatedAt.UnixNano() || ns != facts.At.UnixNano() || !r.TriggeredBy.IsNull() || !r.Command.IsNull() || r.CreatorSession.Present() ||
		r.Promoted.Present() {
		t.Fatalf("timestamps/null/absence changed: %s", raw)
	}
	flags, _ := r.Flags.Get()
	old, _ := c.PreviousRun.Get()
	oldFlags, _ := old.Flags.Get()
	if flags == nil || !reflect.DeepEqual(oldFlags, []string{"reviewed"}) {
		t.Fatalf("current/previous flag snapshots changed: %s", raw)
	}
	config, _ := r.RuntimeConfig.Get()
	for _, hook := range []inputs.Field[[]string]{config.AfterApply, config.AfterDestroy, config.AfterInit, config.AfterPerform, config.AfterPlan, config.AfterRun, config.BeforeApply, config.BeforeDestroy, config.BeforeInit, config.BeforePerform, config.BeforePlan} {
		if commands, _ := hook.Get(); !reflect.DeepEqual(commands, []string{"echo hook"}) {
			t.Fatalf("runtime hook was dropped: %s", raw)
		}
	}
	env, _ := config.Environment.Get()
	if env["VALUE"] != "actual" || !config.TerraformVersion.IsNull() || !config.GitSparseCheckoutPaths.IsNull() {
		t.Fatalf("runtime values/nulls changed: %s", raw)
	}
	changes, _ := r.Changes.Get()
	entity, _ := changes[0].Entity.Get()
	address, _ := entity.Address.Get()
	if address != "local_file.main" || entity.Data.Present() || strings.Contains(string(raw), "artifact-only") {
		t.Fatalf("structural/vendor boundary changed: %s", raw)
	}
	stack, _ := c.Stack.Get()
	if !stack.Labels.IsNull() || !stack.TrackedCommit.IsNull() || stack.Contexts.Present() {
		t.Fatalf("stack absence/null changed: %s", raw)
	}
	if !input.Terraform.Present() || input.Opentofu.Present() || input.Terragrunt.Present() || input.ThirdPartyMetadata.Present() {
		t.Fatalf("vendor projection changed: %s", raw)
	}
}

func TestRunInputBuilderRejectsInvalidOrUnavailableFacts(t *testing.T) {
	for name, change := range map[string]func(*policyapi.RunInputFacts){
		"account":              func(f *policyapi.RunInputFacts) { f.Account = kernel.AccountID{} },
		"timestamp":            func(f *policyapi.RunInputFacts) { f.At = time.Time{} },
		"run id":               func(f *policyapi.RunInputFacts) { f.Run.ID = kernel.ULID{} },
		"run timestamp":        func(f *policyapi.RunInputFacts) { f.Run.CreatedAt = time.Time{} },
		"required trigger":     func(f *policyapi.RunInputFacts) { f.Run.TriggeredBy = policyapi.Fact[string]{} },
		"required plan commit": func(f *policyapi.RunInputFacts) { f.Run.Commit = policyapi.Fact[policyapi.CommitFacts]{} },
		"unknown with value":   func(f *policyapi.RunInputFacts) { f.Run.Branch = policyapi.Fact[string]{Value: new("private-value")} },
		"nested invalid": func(f *policyapi.RunInputFacts) {
			f.Stack.WorkerPool.Value.ID = policyapi.Fact[string]{Value: new("private-value")}
		},
		"commit time":      func(f *policyapi.RunInputFacts) { f.Run.Commit.Value.At = time.Time{} },
		"unsupported kind": func(f *policyapi.RunInputFacts) { f.Type = policyapi.Task },
		"unsupported vendor": func(f *policyapi.RunInputFacts) {
			f.Plan = &policyapi.SanitizedPlan{Vendor: "unknown", Document: json.RawMessage(`{}`)}
		},
		"runtime key-only values": func(f *policyapi.RunInputFacts) {
			f.Run.RuntimeConfig = known(policyapi.RuntimeConfigFacts{TerraformVersion: policyapi.Fact[string]{Known: true}})
		},
		"unsupported action": func(f *policyapi.RunInputFacts) {
			f.Run.Changes = known([]policyapi.ChangeFacts{{Action: "unknown", Phase: "plan"}})
		},
	} {
		t.Run(name, func(t *testing.T) {
			facts := inputFacts()
			change(&facts)
			_, err := (&Service{}).BuildRunInput(t.Context(), facts)
			if !errors.Is(err, policyapi.ErrInput) || strings.Contains(err.Error(), "private-value") {
				t.Fatalf("invalid facts did not fail safely: %v", err)
			}
		})
	}
}

func TestRunInputBuilderApprovalAndVendorVariants(t *testing.T) {
	for _, vendor := range []string{"terraform", "opentofu", "terragrunt"} {
		t.Run(vendor, func(t *testing.T) {
			facts := inputFacts()
			document := `{"format_version":"1.2","resource_changes":null}`
			if vendor == "terragrunt" {
				document = `{"units":[{"path":"selected","plan":` + document + `}]}`
			}
			facts.Plan = &policyapi.SanitizedPlan{Vendor: vendor, Document: json.RawMessage(document)}
			if _, err := (&Service{}).BuildRunInput(t.Context(), facts); err != nil {
				t.Fatal(err)
			}
		})
	}
	planFacts := inputFacts()
	planFacts.Run.Changes = known(
		[]policyapi.ChangeFacts{
			{
				Phase:        "plan",
				Action:       "changed",
				Address:      "module.a.local_file.main",
				Vendor:       "terraform",
				Module:       known("module.a"),
				Name:         known("main"),
				ResourceType: known("local_file"),
			},
		},
	)
	plan, err := (&Service{}).BuildRunInput(t.Context(), planFacts)
	if err != nil || !strings.Contains(string(plan), `"module":"module.a"`) || !strings.Contains(string(plan), `"name":"main"`) {
		t.Fatalf("available address metadata was omitted: %s %v", plan, err)
	}
	facts := inputFacts()
	facts.Type = policyapi.Approval
	raw, err := (&Service{}).BuildRunInput(t.Context(), facts)
	if err != nil || strings.Contains(string(raw), `"reviews"`) || strings.Contains(string(raw), `"previous_run"`) {
		t.Fatalf("approval invented unavailable review or previous facts: %s %v", raw, err)
	}
	facts.PreviousRun = policyapi.Fact[policyapi.RunFacts]{Known: true}
	if _, err := (&Service{}).BuildRunInput(t.Context(), facts); !errors.Is(err, policyapi.ErrInput) {
		t.Fatalf("approval silently discarded unsupported facts: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := (&Service{}).BuildRunInput(ctx, inputFacts()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
