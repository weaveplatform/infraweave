package github_test

import (
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func TestUnitCommitChecksNativeLifecycle(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	run := kernel.NewULID()
	stack := kernel.NewULID()
	r := port.CheckReport{
		Name:       "infraweave/infra",
		State:      port.CheckQueued,
		Title:      "Queued",
		Summary:    "Waiting for a worker.",
		URL:        "https://infra.example/runs/" + run.String(),
		ExternalID: stack.String(),
		RecoveryID: "infraweave/run:" + run.String(),
		Actions:    []port.CheckAction{},
	}
	id, err := l.gh.Set(t.Context(), l.repo.Repo, l.repo.Head, r)
	if err != nil || id == "" {
		t.Fatal(id, err)
	}
	for _, state := range []port.CheckState{port.CheckInProgress, port.CheckSuccess} {
		r.State = state
		r.Title = string(state)
		r.Summary = "1 add, 0 change, 0 delete"
		next, err := l.gh.Set(t.Context(), l.repo.Repo, l.repo.Head, r)
		if err != nil || next == "" {
			t.Fatal(next, err)
		}
	}
	writes := l.fake.CheckWrites("acme", "infra")
	if len(writes) != 3 {
		t.Fatal("native state transitions", writes)
	}
	if err := nativeStateProof(writes[0].Status, writes[1].Status, writes[2].Status); err != "" {
		t.Fatal(err, writes)
	}
	count := len(writes)
	if _, err := l.gh.Set(t.Context(), l.repo.Repo, l.repo.Head, r); err != nil {
		t.Fatal(err)
	}
	if len(l.fake.CheckWrites("acme", "infra")) != count+1 {
		t.Fatal("explicit empty actions must still be sent on an update")
	}
	for _, write := range l.fake.CheckWrites("acme", "infra") {
		if write.ID != writes[0].ID {
			t.Fatal("state update created a second native check", write)
		}
	}
	r.State = port.CheckFailure
	r.Summary = "Provider-independent failure"
	if _, err := l.gh.Set(t.Context(), l.repo.Repo, l.repo.Head, r); err != nil {
		t.Fatal(err)
	}
	r.State = port.CheckQueued
	if _, err := l.gh.Set(t.Context(), l.repo.Repo, l.repo.Head, r); err != nil {
		t.Fatal("reopen", err)
	}
	for _, state := range []port.CheckState{port.CheckNeutral, port.CheckCanceled, port.CheckSkipped} {
		r.State = state
		if _, err := l.gh.Set(t.Context(), l.repo.Repo, l.repo.Head, r); err != nil {
			t.Fatal(state, err)
		}
	}
}

func nativeStateProof(a, b, c string) string {
	if a != "queued" || b != "in_progress" || c != "completed" {
		return "state mapping changed"
	}
	return ""
}

func TestUnitCommitCheckLostCreate(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.fake.FailNextCheckCreate()
	r := port.CheckReport{
		Name:       "infraweave/infra",
		State:      port.CheckInProgress,
		Title:      "Planning",
		Summary:    "Running plan.",
		URL:        "https://infra.example/runs/one",
		ExternalID: kernel.NewULID().String(),
		RecoveryID: "infraweave/run:one",
		Actions:    []port.CheckAction{},
	}
	if _, err := l.gh.Set(t.Context(), l.repo.Repo, l.repo.Head, r); err == nil {
		t.Fatal("lost response succeeded")
	}
	before := l.fake.Checks("acme", "infra")
	if len(before) != 1 {
		t.Fatal("effect not stored before lost response", before)
	}
	if _, err := l.gh.Set(t.Context(), l.repo.Repo, l.repo.Head, r); err != nil {
		t.Fatal(err)
	}
	if len(l.fake.Checks("acme", "infra")) != 1 {
		t.Fatal("recovery duplicated native effect")
	}
	r.Name = ""
	if _, err := l.gh.Set(t.Context(), l.repo.Repo, l.repo.Head, r); err == nil {
		t.Fatal("empty name accepted")
	}
	r.Name = "infraweave/infra"
	r.State = "unknown"
	if _, err := l.gh.Set(t.Context(), l.repo.Repo, l.repo.Head, r); err == nil {
		t.Fatal("unknown state accepted")
	}
	r.State = port.CheckQueued
	if _, err := l.gh.Set(t.Context(), l.repo.Repo, strings.Repeat("0", 40), r); err == nil {
		t.Fatal("zero commit accepted")
	}
}

func TestUnitCommitChecksPerRunActionsAndReopen(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	stack := kernel.NewULID()
	report := port.CheckReport{
		Name:       "infraweave/tracked",
		State:      port.CheckInProgress,
		Title:      "Planning",
		Summary:    "Plan is running.",
		URL:        "https://infra.example/runs/first",
		ExternalID: stack.String(),
		RecoveryID: "infraweave/run:first",
		Actions: []port.CheckAction{
			{Label: "Preview", Description: "Propose a run", Identifier: "preview"},
			{Label: "Deploy", Description: "Track a run", Identifier: "deploy"},
		},
	}
	first, err := l.gh.Set(t.Context(), l.repo.Repo, l.repo.Head, report)
	if err != nil {
		t.Fatal(err)
	}
	report.Actions = []port.CheckAction{}
	if _, err := l.gh.Set(t.Context(), l.repo.Repo, l.repo.Head, report); err != nil {
		t.Fatal(err)
	}
	got := l.fake.Checks("acme", "infra")
	if len(got) != 1 || got[0].Actions == nil || len(got[0].Actions) != 0 {
		t.Fatal("disabled actions retained", got)
	}
	report.URL = "https://infra.example/runs/second"
	report.RecoveryID = "infraweave/run:second"
	second, err := l.gh.Set(t.Context(), l.repo.Repo, l.repo.Head, report)
	if err != nil || second == first {
		t.Fatal("another run took over first check", first, second, err)
	}
	report.ExternalID = ""
	report.RecoveryID = "infraweave/aggregate/infraweave/tracked/" + l.repo.Head
	aggregate, err := l.gh.Set(t.Context(), l.repo.Repo, l.repo.Head, report)
	if err != nil || aggregate == first || aggregate == second {
		t.Fatal("aggregate stole a per-run identity", aggregate, err)
	}
	report.State = port.CheckSuccess
	if _, err := l.gh.Set(t.Context(), l.repo.Repo, l.repo.Head, report); err != nil {
		t.Fatal(err)
	}
	l.fake.RejectCheckReopen(true)
	report.State = port.CheckQueued
	reopened, err := l.gh.Set(t.Context(), l.repo.Repo, l.repo.Head, report)
	if err != nil || reopened == aggregate {
		t.Fatal("refused reopen did not create a recoverable generation", reopened, err)
	}
	again, err := l.gh.Set(t.Context(), l.repo.Repo, l.repo.Head, report)
	if err != nil || again != reopened || len(l.fake.Checks("acme", "infra")) != 4 {
		t.Fatal("reopened check was not recovered", again, err)
	}
}

func TestUnitCommitCheckUncertainReopenKeepsNativeID(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	report := port.CheckReport{
		Name:       "infraweave/proposed",
		State:      port.CheckSuccess,
		Title:      "Finished",
		Summary:    "1 added.",
		URL:        "https://infra.example/runs/one",
		RecoveryID: "infraweave/run:one",
		ExternalID: kernel.NewULID().String(),
		Actions:    []port.CheckAction{},
	}
	id, err := l.gh.Set(t.Context(), l.repo.Repo, l.repo.Head, report)
	if err != nil {
		t.Fatal(err)
	}
	l.fake.FailNextCheckUpdate()
	report.State = port.CheckQueued
	report.Title = "Queued"
	next, err := l.gh.Set(t.Context(), l.repo.Repo, l.repo.Head, report)
	if err == nil || next != "" || len(l.fake.Checks("acme", "infra")) != 1 {
		t.Fatal("uncertain PATCH triggered a duplicate create", next, err, l.fake.Checks("acme", "infra"))
	}
	current, err := l.gh.Set(t.Context(), l.repo.Repo, l.repo.Head, report)
	if err != nil || current != id || len(l.fake.Checks("acme", "infra")) != 1 {
		t.Fatal("later attempt failed to recover applied PATCH", current, id, err)
	}
}
