package core

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

func pushInputFacts() policyapi.PushInputFacts {
	run := inputFacts()
	run.Run.State = kernel.RunStateQueued
	run.Stack.WorkerPool = policyapi.Fact[policyapi.WorkerPoolFacts]{}
	return policyapi.PushInputFacts{
		Account: run.Account, Stack: run.Stack, InProgress: known([]policyapi.RunFacts{run.Run}),
		Push: known(policyapi.PushFacts{
			Author: "Alice", Branch: "main", Hash: "head", Message: "Change", At: run.At,
			AffectedFiles: []string{"root/main.tf"}, AffectedStacksCount: 2, Tag: known(""),
			Commits: known([]policyapi.PushCommitFacts{{Commit: *run.Run.Commit.Value, AffectedFiles: []string{"root/main.tf"}}}),
		}),
		PullRequest: policyapi.Fact[policyapi.PullRequestFacts]{Known: true},
		VCSIntegration: known(policyapi.VCSIntegrationFacts{
			ID: kernel.NewULID(), Name: "GitLab", Description: "Owned metadata", IsDefault: false, Provider: "GITLAB",
			Labels: []string{}, CreatedAt: run.At, UpdatedAt: run.At, Space: run.Stack.Space,
		}),
	}
}

func TestUnitPushInputBuilderPreservesDeclaredFacts(t *testing.T) {
	facts := pushInputFacts()
	raw, err := (&Service{}).BuildPushInput(t.Context(), facts)
	if err != nil {
		t.Fatal(err)
	}
	var input inputs.PushInput
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	push, _ := input.Push.Get()
	at, _ := push.CreatedAt.Get()
	count, _ := push.AffectedStacksCount.Get()
	files, _ := push.AffectedFiles.Get()
	commits, _ := push.Commits.Get()
	if at != facts.Push.Value.At.UnixNano() || count != 2 || !slices.Equal(files, []string{"root/main.tf"}) || len(commits) != 1 || !input.PullRequest.IsNull() {
		t.Fatalf("push facts changed: %s", raw)
	}
	stack, _ := input.Stack.Get()
	if stack.WorkerPool.Present() || !stack.Labels.IsNull() {
		t.Fatalf("unknown pool or nullable labels changed: %s", raw)
	}
	integration, _ := input.VCSIntegration.Get()
	isDefault, _ := integration.IsDefault.Get()
	name, _ := integration.Name.Get()
	if isDefault || name != "GitLab" || integration.Space.IsNull() {
		t.Fatalf("integration facts changed: %s", raw)
	}
	progress, _ := input.InProgress.Get()
	progressJSON, err := json.Marshal(progress)
	if err != nil {
		t.Fatal(err)
	}
	var runs []inputs.InProgressRun
	if err := json.Unmarshal(progressJSON, &runs); err != nil || len(runs) != 1 || !runs[0].TriggeredBy.IsNull() || !runs[0].Command.IsNull() {
		t.Fatalf("in-progress facts changed: %s error=%v", progressJSON, err)
	}
	if strings.Contains(string(progressJSON), `"flags"`) || strings.Contains(string(progressJSON), `"state_version"`) ||
		strings.Contains(string(progressJSON), `"runtime_config"`) {
		t.Fatalf("internal-only fields expanded the published in-progress input: %s", progressJSON)
	}
}

func TestUnitPushInputBuilderPreservesPullRequestCommentAndFalseValues(t *testing.T) {
	facts := pushInputFacts()
	facts.Push = policyapi.Fact[policyapi.PushFacts]{Known: true}
	commit := *inputFacts().Run.Commit.Value
	facts.PullRequest = known(policyapi.PullRequestFacts{
		ID: 7, Action: "commented", Author: "Alice", Title: "Review", Base: commit, Head: commit,
		AffectedStacksCount: 2, Diff: []string{}, Labels: []string{}, ActionInitiator: known("reviewer"),
		HeadOwner: known("acme"), Approved: known(false), Mergeable: known(false), Undiverged: known(true), Comment: known("plan please"),
	})
	raw, err := (&Service{}).BuildPushInput(t.Context(), facts)
	if err != nil {
		t.Fatal(err)
	}
	var input inputs.PushInput
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	pr, _ := input.PullRequest.Get()
	comment, _ := pr.Comment.Get()
	approved, _ := pr.Approved.Get()
	mergeable, _ := pr.Mergeable.Get()
	if !input.Push.IsNull() || comment != "plan please" || !pr.Approved.Present() || approved || !pr.Mergeable.Present() || mergeable {
		t.Fatalf("comment/false/null facts changed: %s", raw)
	}
}

func TestUnitPushInputBuilderRefusesUnavailableOrInvalidFacts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*policyapi.PushInputFacts)
	}{
		{"account", func(f *policyapi.PushInputFacts) { f.Account = kernel.AccountID{} }},
		{"stack", func(f *policyapi.PushInputFacts) { f.Stack.ID = kernel.ULID{} }},
		{"stack slug", func(f *policyapi.PushInputFacts) { f.Stack.Slug = "" }},
		{"unavailable state", func(f *policyapi.PushInputFacts) { f.Stack.State = "" }},
		{"no event", func(f *policyapi.PushInputFacts) { f.Push = policyapi.Fact[policyapi.PushFacts]{} }},
		{"contradictory fact", func(f *policyapi.PushInputFacts) { f.Push.Known = false }},
		{"event count", func(f *policyapi.PushInputFacts) { f.Push.Value.AffectedStacksCount = -1 }},
		{"missing hash", func(f *policyapi.PushInputFacts) { f.Push.Value.Hash = "" }},
		{"null progress", func(f *policyapi.PushInputFacts) { f.InProgress = policyapi.Fact[[]policyapi.RunFacts]{Known: true} }},
		{"invalid progress", func(f *policyapi.PushInputFacts) { f.InProgress.Value = new([]policyapi.RunFacts{{}}) }},
		{"invalid metadata", func(f *policyapi.PushInputFacts) { f.VCSIntegration.Value.ID = kernel.ULID{} }},
		{"invalid request", func(f *policyapi.PushInputFacts) { f.PullRequest = known(policyapi.PullRequestFacts{}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts := pushInputFacts()
			tc.change(&facts)
			if raw, err := (&Service{}).BuildPushInput(t.Context(), facts); !errors.Is(err, policyapi.ErrInput) || raw != nil {
				t.Fatalf("invalid facts produced input=%s error=%v", raw, err)
			}
		})
	}
}
