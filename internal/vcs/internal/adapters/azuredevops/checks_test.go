package azuredevops_test

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
	id, err := l.az.Set(t.Context(), l.repo.Repo, l.repo.Head, r)
	if err != nil || id == "" {
		t.Fatal(id, err)
	}
	for _, state := range []port.CheckState{port.CheckInProgress, port.CheckSuccess} {
		r.State = state
		r.Title = string(state)
		r.Summary = "1 add, 0 change, 0 delete"
		next, err := l.az.Set(t.Context(), l.repo.Repo, l.repo.Head, r)
		if err != nil || next == "" {
			t.Fatal(next, err)
		}
	}
	writes := l.fake.CheckWrites("Platform", "infra")
	if len(writes) != 3 {
		t.Fatal("native state transitions", writes)
	}
	if err := nativeStateProof(writes[0].State, writes[1].State, writes[2].State); err != "" {
		t.Fatal(err, writes)
	}
	count := len(writes)
	if _, err := l.az.Set(t.Context(), l.repo.Repo, l.repo.Head, r); err != nil {
		t.Fatal(err)
	}
	if len(l.fake.CheckWrites("Platform", "infra")) != count {
		t.Fatal("repeated desired state mutated native history")
	}
	r.State = port.CheckFailure
	r.Summary = "Provider-independent failure"
	if _, err := l.az.Set(t.Context(), l.repo.Repo, l.repo.Head, r); err != nil {
		t.Fatal(err)
	}
	r.State = port.CheckQueued
	if _, err := l.az.Set(t.Context(), l.repo.Repo, l.repo.Head, r); err != nil {
		t.Fatal("reopen", err)
	}
	for _, state := range []port.CheckState{port.CheckNeutral, port.CheckCanceled, port.CheckSkipped} {
		r.State = state
		if _, err := l.az.Set(t.Context(), l.repo.Repo, l.repo.Head, r); err != nil {
			t.Fatal(state, err)
		}
	}
}

func nativeStateProof(a, b, c string) string {
	if a != "pending" || b != "pending" || c != "succeeded" {
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
	if _, err := l.az.Set(t.Context(), l.repo.Repo, l.repo.Head, r); err == nil {
		t.Fatal("lost response succeeded")
	}
	before := l.fake.Checks("Platform", "infra")
	if len(before) != 1 {
		t.Fatal("effect not stored before lost response", before)
	}
	if _, err := l.az.Set(t.Context(), l.repo.Repo, l.repo.Head, r); err != nil {
		t.Fatal(err)
	}
	if len(l.fake.Checks("Platform", "infra")) != 1 || len(l.fake.CheckWrites("Platform", "infra")) != 1 {
		t.Fatal("recovery duplicated native effect")
	}
	r.Name = ""
	if _, err := l.az.Set(t.Context(), l.repo.Repo, l.repo.Head, r); err == nil {
		t.Fatal("empty name accepted")
	}
	r.Name = "infraweave/infra"
	r.State = "unknown"
	if _, err := l.az.Set(t.Context(), l.repo.Repo, l.repo.Head, r); err == nil {
		t.Fatal("unknown state accepted")
	}
	if _, err := l.az.Set(t.Context(), l.repo.Repo, strings.Repeat("0", 40), r); err == nil {
		t.Fatal("zero commit accepted")
	}
}
