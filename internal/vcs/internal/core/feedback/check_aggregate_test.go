package feedback

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type aggregateWorld struct {
	*checkTargetFacts
	rows    []runsapi.CheckRun
	stacks  []stacksapi.Stack
	caps    []CheckBinding
	missing bool
	phase   string
}

func (f *aggregateWorld) EventUse(_ context.Context, account kernel.AccountID, id kernel.ULID) (stacksapi.Use, bool, error) {
	if account != f.kept.Account || id != f.kept.Integration {
		return stacksapi.Use{}, false, errors.New("wrong integration scope")
	}
	if f.phase == "integration" {
		return stacksapi.Use{}, false, ErrCheckReport
	}
	return stacksapi.Use{Provider: f.kept.Provider, Integration: id}, !f.missing, nil
}

func (f *aggregateWorld) ByVCSRepository(_ context.Context, account kernel.AccountID, u stacksapi.Use, ns, repo string) ([]stacksapi.Stack, error) {
	if account != f.kept.Account || u.Integration != f.kept.Integration || ns != f.kept.Namespace || repo != f.kept.Repository {
		return nil, errors.New("wrong repository scope")
	}
	if f.phase == "stacks" {
		return nil, ErrCheckReport
	}
	return f.stacks, nil
}

func (f *aggregateWorld) OnCommit(_ context.Context, account kernel.AccountID, ids []kernel.ULID, sha string) ([]runsapi.CheckRun, error) {
	if account != f.kept.Account || sha != f.kept.Commit || len(ids) != len(f.stacks) {
		return nil, errors.New("wrong run scope")
	}
	for _, r := range f.stacks {
		if !slices.Contains(ids, r.ID) {
			return nil, errors.New("stack scope omitted")
		}
	}
	if f.phase == "runs" {
		return nil, ErrCheckReport
	}
	return f.rows, nil
}

func (f *aggregateWorld) NoRunChecks(_ context.Context, b CheckBinding, kind kernel.RunType) ([]CheckBinding, error) {
	if b.Account != f.kept.Account || b.Integration != f.kept.Integration || b.Namespace != f.kept.Namespace || b.Repository != f.kept.Repository || b.Commit != f.kept.Commit {
		return nil, errors.New("wrong cap scope")
	}
	if f.phase == "caps" {
		return nil, ErrCheckReport
	}
	return slices.DeleteFunc(slices.Clone(f.caps), func(c CheckBinding) bool { return c.Type != kind }), nil
}

func newAggregateWorld(t *testing.T) (*Service, *aggregateWorld) {
	t.Helper()
	s, base, _ := checkTargetWorld(t)
	b, err := s.selectCheck(t.Context(), kernel.AccountID{ULID: kernel.NewULID()}, base.run)
	if err != nil {
		t.Fatal(err)
	}
	b.Policy.Mode = "ALL"
	f := &aggregateWorld{checkTargetFacts: base}
	f.kept = b
	f.stacks = []stacksapi.Stack{{ID: b.Stack, Slug: b.Slug}, {ID: kernel.NewULID(), Slug: "other"}}
	s.checks.Integrations = f
	s.checks.Stacks = f
	s.checks.Facts = f
	s.checks.Store = f
	return s, f
}

func TestUnitAggregateCheckLife(t *testing.T) {
	s, f := newAggregateWorld(t)
	first := f.run
	first.Type = kernel.RunTypeProposed
	first.State = kernel.RunStateQueued
	second := first
	second.ID = kernel.NewULID()
	second.Stack = f.stacks[1].ID
	second.CreatedAt = first.CreatedAt.Add(time.Second)
	f.rows = []runsapi.CheckRun{first, second}
	assert := func(name string, state port.CheckState, found bool) port.CheckReport {
		t.Helper()
		r, _, _, got, err := s.aggregateReport(t.Context(), f.kept, name)
		if err != nil || got != found || got && r.State != state {
			t.Fatal(name, r, got, err)
		}
		return r
	}
	assert("infraweave/proposed", port.CheckQueued, true)
	assert("infraweave/tracked", "", false)
	assert("infraweave/modules", "", false)
	f.rows[0].State = kernel.RunStatePlanning
	assert("infraweave/proposed", port.CheckInProgress, true)
	f.rows[0].State = kernel.RunStateFinished
	f.rows[0].Delta = &kernel.PlanDelta{Add: 2}
	f.rows[1].State = kernel.RunStateFailed
	r := assert("infraweave/proposed", port.CheckFailure, true)
	if !strings.Contains(r.Summary, "other: FAILED") || !strings.Contains(r.Summary, "2 add") {
		t.Fatal("failure/counts omitted", r)
	}
	for _, name := range []string{"infraweave/tracked", "infraweave/modules"} {
		r = assert(name, port.CheckSkipped, true)
		if r.Summary != "0 runs." {
			t.Fatal(r)
		}
	}
	// A later run reopens the same aggregate even though another run has failed.
	later := second
	later.ID = kernel.NewULID()
	later.CreatedAt = later.CreatedAt.Add(time.Second)
	later.State = kernel.RunStatePreparing
	f.rows = append(f.rows, later)
	assert("infraweave/proposed", port.CheckInProgress, true)
	f.rows[2].State = kernel.RunStateFinished
	assert("infraweave/proposed", port.CheckFailure, true)
	f.rows[1].State = kernel.RunStateFinished
	assert("infraweave/proposed", port.CheckSuccess, true)
}

func TestUnitAggregateCapsScopeAndErrors(t *testing.T) {
	s, f := newAggregateWorld(t)
	cap := f.kept
	cap.Cap = true
	cap.Run = kernel.ULID{}
	cap.Limit = 3
	cap.Type = kernel.RunTypeTracked
	f.caps = []CheckBinding{cap}
	report, _, _, found, err := s.aggregateReport(t.Context(), f.kept, "infraweave/tracked")
	if err != nil || !found || report.State != port.CheckFailure || !strings.Contains(report.Summary, "limit of 3 runs") {
		t.Fatal(report, found, err)
	}
	run := f.run
	run.State = kernel.RunStateFinished
	run.Type = kernel.RunTypeTracked
	f.rows = []runsapi.CheckRun{run}
	report, _, _, _, err = s.aggregateReport(t.Context(), f.kept, "infraweave/tracked")
	if err != nil || report.State != port.CheckSuccess || strings.Contains(report.Summary, "limit") {
		t.Fatal("obsolete cap overrides later run", report, err)
	}
	for _, kind := range []string{"local", "drift", "task", "other stack", "other commit"} {
		bad := run
		switch kind {
		case "local":
			bad.Local = true
		case "drift":
			bad.DriftDetection = true
		case "task":
			bad.Type = kernel.RunTypeTask
		case "other stack":
			bad.Stack = kernel.NewULID()
		case "other commit":
			bad.CommitSHA = strings.Repeat("b", 40)
		}
		f.rows = []runsapi.CheckRun{bad}
		f.caps = nil
		_, _, _, found, err = s.aggregateReport(t.Context(), f.kept, "infraweave/tracked")
		if err != nil || found {
			t.Fatal(kind, found, err)
		}
	}
	for _, phase := range []string{"integration", "stacks", "runs", "caps"} {
		f.phase = phase
		_, _, _, _, err = s.aggregateReport(t.Context(), f.kept, "infraweave/tracked")
		if !errors.Is(err, ErrCheckReport) {
			t.Fatal(phase, err)
		}
	}
	f.phase = ""
	f.missing = true
	_, _, _, found, err = s.aggregateReport(t.Context(), f.kept, "infraweave/tracked")
	if err != nil || found {
		t.Fatal(found, err)
	}
}
