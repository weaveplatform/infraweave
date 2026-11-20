package approval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/schedule"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

type (
	enqueued struct {
		kind    queue.Kind
		payload any
	}
	changeJobs struct {
		queued      []enqueued
		completed   int
		failKind    string
		completeErr error
	}
)

func (j *changeJobs) EnqueueTx(_ context.Context, k queue.Kind, p any, _ ...queue.Option) (int64, error) {
	if j.failKind == k.Name {
		return 0, errSource
	}
	j.queued = append(j.queued, enqueued{k, p})
	return int64(len(j.queued)), nil
}

func (j *changeJobs) CompleteTx(context.Context, queue.Job) error {
	if j.completeErr != nil {
		return j.completeErr
	}
	j.completed++
	return nil
}

type waitingStore struct {
	ReconcileStore
	stacks  []kernel.ULID
	runs    []lifecycle.Run
	current lifecycle.Run
	fail    string
}

func (s *waitingStore) WaitingApprovalStacks(context.Context, kernel.AccountID, kernel.ULID, int) ([]kernel.ULID, error) {
	if s.fail == "scan" {
		return nil, errSource
	}
	return s.stacks, nil
}

func (s *waitingStore) WaitingApprovals(context.Context, kernel.AccountID, kernel.ULID) ([]lifecycle.Run, error) {
	if s.fail == "waiting" {
		return nil, errSource
	}
	return s.runs, nil
}

func (s *waitingStore) Lock(context.Context, kernel.AccountID, kernel.ULID) (lifecycle.Run, error) {
	if s.fail == "run" {
		return lifecycle.Run{}, errSource
	}
	return s.current, nil
}

type reconcileOwners struct {
	stacksapi.Stacks
	settings stacksapi.Settings
	bad      string
	gets     int
}

func (o *reconcileOwners) Get(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.Stack, error) {
	o.gets++
	if o.bad == "missing" {
		return stacksapi.Stack{}, stacksapi.ErrNotFound
	}
	if o.bad == "get" {
		return stacksapi.Stack{}, errSource
	}
	return o.settings.Stack, nil
}

func (o *reconcileOwners) Hold(context.Context, kernel.AccountID, kernel.ULID) error {
	if o.bad == "space" {
		return errSource
	}
	return nil
}

func (o *reconcileOwners) LockForRun(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.Settings, error) {
	if o.bad == "stack" {
		return stacksapi.Settings{}, errSource
	}
	s := o.settings
	if o.bad == "moved" {
		s.Space = kernel.NewULID()
	}
	return s, nil
}

type reconcileSpaces struct {
	accessapi.Spaces
	owner *reconcileOwners
}

func (s reconcileSpaces) Hold(ctx context.Context, account kernel.AccountID, id kernel.ULID) error {
	return s.owner.Hold(ctx, account, id)
}

type scheduledRows struct {
	schedule.Store
	err error
}

func (s scheduledRows) Queued(context.Context, kernel.AccountID, kernel.ULID) ([]lifecycle.Run, error) {
	return nil, s.err
}

type gateOutcome struct {
	calls  int
	err    error
	reject bool
}

func (g *gateOutcome) Evaluate(context.Context, lifecycle.Run) (policyapi.Evaluation, error) {
	g.calls++
	if g.err != nil {
		return policyapi.Evaluation{}, g.err
	}
	out := policyapi.Undecided
	if g.reject {
		out = policyapi.Reject
	}
	return policyapi.Evaluation{Type: policyapi.Approval, HasPolicies: true, Outcome: out}, nil
}

type changeEvents struct {
	calls int
	err   error
}

func (e *changeEvents) Append(_ context.Context, v outbox.Event) (outbox.Event, error) {
	e.calls++
	return v, e.err
}

type failedMoveStore struct{ lifecycle.Store }

func (failedMoveStore) Lock(context.Context, kernel.AccountID, kernel.ULID) (lifecycle.Run, error) {
	return lifecycle.Run{}, errSource
}

func reconcileFixture() (*Reconciler, *waitingStore, *reconcileOwners, *changeJobs, *gateOutcome, *changeEvents) {
	account, stack, space := kernel.NewAccountID(), kernel.NewULID(), kernel.NewULID()
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	r := lifecycle.Run{
		Account:   account,
		Stack:     stack,
		ID:        kernel.NewULID(),
		Type:      kernel.RunTypeTracked,
		State:     kernel.RunStateUnconfirmed,
		Version:   6,
		CreatedAt: at,
		UpdatedAt: at,
	}
	store := &waitingStore{current: r, runs: []lifecycle.Run{r}}
	owners := &reconcileOwners{settings: stacksapi.Settings{Stack: stacksapi.Stack{ID: stack, Account: account, Space: space, Slug: "infra", Name: "Infra"}}}
	jobs, gates, events := &changeJobs{}, &gateOutcome{}, &changeEvents{}
	lc := lifecycle.New(lifecycle.Deps{Store: failedMoveStore{}, Stacks: owners, Approvals: gates, Events: events, Jobs: jobs})
	scheduler := schedule.New(schedule.Deps{Store: scheduledRows{}, Stacks: owners, Lifecycle: lc, Queue: jobs})
	s := NewReconciler(ReconcileDeps{DB: testDB{}, Store: store, Stacks: owners, Spaces: reconcileSpaces{owner: owners}, Jobs: jobs, Lifecycle: lc, Scheduler: scheduler})
	return s, store, owners, jobs, gates, events
}

func changeJob(account kernel.AccountID, p any) queue.Job {
	raw, err := json.Marshal(p)
	if err != nil {
		panic(err)
	}
	return queue.Job{Account: account.ULID, Payload: raw}
}

func TestUnitApprovalChangesAreAcknowledgedOnlyAfterFollowup(t *testing.T) {
	for _, kind := range []string{"policy", "stack", "deleted", "bad-policy", "bad-stack", "unknown", "scan-failed", "stack-failed", "complete-failed"} {
		t.Run(kind, func(t *testing.T) {
			s, store, _, jobs, _, _ := reconcileFixture()
			id := kernel.NewULID()
			ev := outbox.Event{Account: store.current.Account.ULID, Kind: policyapi.ChangedKind, Document: []byte(fmt.Sprintf(`{"policy":"%s","change":"updated"}`, id))}
			var want error
			switch kind {
			case "stack", "deleted", "stack-failed":
				ev.Kind = stacksapi.ChangedKind
				action := stacksapi.ChangeUpdated
				if kind == "deleted" {
					action = stacksapi.ChangeDeleted
				}
				ev.Document = []byte(fmt.Sprintf(`{"stack":"%s","change":"%s"}`, id, action))
			case "bad-policy":
				ev.Document = []byte(`{}`)
				want = errPayload
			case "bad-stack":
				ev.Kind = stacksapi.ChangedKind
				ev.Document = []byte(`{}`)
				want = errPayload
			case "unknown":
				ev.Kind = "unsupported"
				want = errPayload
			case "scan-failed":
				jobs.failKind = ScanKind.Name
				want = errSource
			case "complete-failed":
				jobs.completeErr = errSource
				want = errSource
			}
			if kind == "stack-failed" {
				jobs.failKind = ReevaluateKind.Name
				want = errSource
			}
			err := s.Changed(t.Context(), ev, queue.Job{})
			if !errors.Is(err, want) {
				t.Fatalf("change %s: %v want %v", kind, err, want)
			}
			if want != nil && jobs.completed != 0 {
				t.Fatal("failed follow-up acknowledged its source")
			}
			if want == nil && jobs.completed != 1 {
				t.Fatal("successful follow-up did not acknowledge")
			}
			if kind == "deleted" && len(jobs.queued) != 0 {
				t.Fatal("deleted stack was reevaluated")
			}
		})
	}
}

func TestUnitApprovalScanPagesAndKeepsFailuresRetryable(t *testing.T) {
	for _, phase := range []string{"page", "empty", "bad-payload", "read", "stack-enqueue", "continuation-enqueue", "complete"} {
		t.Run(phase, func(t *testing.T) {
			s, store, _, jobs, _, _ := reconcileFixture()
			for range 26 {
				store.stacks = append(store.stacks, kernel.NewULID())
			}
			job := changeJob(store.current.Account, scanPage{})
			var want error
			switch phase {
			case "empty":
				store.stacks = nil
			case "bad-payload":
				job.Payload = []byte("{")
			case "read":
				store.fail = "scan"
				want = errSource
			case "stack-enqueue":
				jobs.failKind = ReevaluateKind.Name
				want = errSource
			case "continuation-enqueue":
				jobs.failKind = ScanKind.Name
				want = errSource
			case "complete":
				jobs.completeErr = errSource
				want = errSource
			}
			err := s.Scan(t.Context(), job)
			if phase == "bad-payload" {
				if err == nil || jobs.completed != 0 {
					t.Fatal("malformed scan was accepted")
				}
				return
			}
			if !errors.Is(err, want) {
				t.Fatalf("scan %s: %v want %v", phase, err, want)
			}
			if want != nil && jobs.completed != 0 {
				t.Fatal("failed scan was acknowledged")
			}
			if phase == "page" {
				if len(jobs.queued) != 26 || jobs.queued[25].kind != ScanKind || jobs.queued[25].payload.(scanPage).After != store.stacks[24] {
					t.Fatal("scan lost its bounded continuation")
				}
			}
		})
	}
}

func TestUnitApprovalReevaluationFencesAndPreservesErrors(t *testing.T) {
	for _, phase := range []string{"waiting", "missing", "bad-payload", "get", "space", "stack", "moved", "schedule", "waiting-read", "run", "advanced", "evaluation", "apply", "event", "complete"} {
		t.Run(phase, func(t *testing.T) {
			s, store, owners, jobs, gates, events := reconcileFixture()
			job := changeJob(store.current.Account, lifecycle.Schedule{Stack: store.current.Stack})
			var want error
			switch phase {
			case "bad-payload":
				job.Payload = []byte(`{}`)
				want = errPayload
			case "missing":
				owners.bad = phase
			case "get", "space", "stack":
				owners.bad = phase
				want = errSource
			case "moved":
				owners.bad = phase
				want = kernel.ErrUnavailable
			case "schedule":
				s.d.Scheduler = schedule.New(schedule.Deps{Store: scheduledRows{err: errSource}, Stacks: owners})
				want = errSource
			case "waiting-read":
				store.fail = "waiting"
				want = errSource
			case "run":
				store.fail = phase
				want = errSource
			case "advanced":
				store.current.Version++
			case "evaluation":
				gates.err = errSource
				want = errSource
			case "apply":
				gates.reject = true
				want = errSource
			case "event":
				events.err = errSource
				want = errSource
			case "complete":
				jobs.completeErr = errSource
				want = errSource
			}
			err := s.Reevaluate(t.Context(), job)
			if !errors.Is(err, want) {
				t.Fatalf("reevaluation %s: %v want %v", phase, err, want)
			}
			if want != nil && jobs.completed != 0 {
				t.Fatal("failed reevaluation was acknowledged")
			}
			if phase == "advanced" && (gates.calls != 0 || events.calls != 0) {
				t.Fatal("stale job evaluated an advanced run")
			}
			if phase == "waiting" && (gates.calls != 1 || events.calls != 1 || jobs.completed != 1) {
				t.Fatal("waiting-state evidence was not refreshed and acknowledged")
			}
		})
	}
}

func TestUnitApprovalSpaceChangesQueueCommittedVersionWork(t *testing.T) {
	s, store, _, jobs, _, _ := reconcileFixture()
	account := store.current.Account
	space := kernel.NewULID()
	if used, err := s.SpaceInUse(t.Context(), account, space); err != nil || used {
		t.Fatal("runs incorrectly own a space reference")
	}
	if lost, err := s.LostOnMove(t.Context(), accessapi.SpaceMove{Account: account, Space: space}); err != nil || len(lost) != 0 {
		t.Fatal("runs incorrectly vetoed an owner move")
	}
	for _, version := range []int64{1, 2} {
		if err := s.SpaceChanging(t.Context(), accessapi.SpaceChange{Account: account, Space: space, Version: version}); err != nil {
			t.Fatal(err)
		}
	}
	if len(jobs.queued) != 2 {
		t.Fatal("successive space versions lost reevaluation work")
	}
}
