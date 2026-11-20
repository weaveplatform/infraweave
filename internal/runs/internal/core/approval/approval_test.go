package approval

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/evidence"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/review"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

var errSource = errors.New("source unavailable")

type testTransaction struct{ pgx.Tx }

func (testTransaction) Commit(context.Context) error   { return nil }
func (testTransaction) Rollback(context.Context) error { return nil }

type testDB struct{}

func (testDB) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) { return testTransaction{}, nil }

type testStore struct {
	Store
	run                       lifecycle.Run
	fail                      string
	reviews                   []review.Record
	gateWrites, receiptWrites int
	changeKey                 string
}

func (s *testStore) Lock(context.Context, kernel.AccountID, kernel.ULID) (lifecycle.Run, error) {
	if s.fail == "lock" {
		return lifecycle.Run{}, errSource
	}
	return s.run, nil
}

func (s *testStore) PolicyEvidence(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID]evidence.Snapshot, error) {
	if s.fail == "evidence" {
		return nil, errSource
	}
	if s.fail == "missing-evidence" {
		return map[kernel.ULID]evidence.Snapshot{}, nil
	}
	return map[kernel.ULID]evidence.Snapshot{s.run.ID: {Flags: []string{"owned-flag"}}}, nil
}

func (s *testStore) Reviews(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID][]review.Record, error) {
	if s.fail == "reviews" {
		return nil, errSource
	}
	return map[kernel.ULID][]review.Record{s.run.ID: s.reviews}, nil
}

func (s *testStore) Changes(context.Context, lifecycle.Run) (string, bool, error) {
	if s.fail == "changes" {
		return "", false, errSource
	}
	return s.changeKey, s.changeKey != "", nil
}

func (s *testStore) RecordApproval(context.Context, kernel.AccountID, kernel.ULID, evidence.Approval) error {
	if s.fail == "gate-write" {
		return errSource
	}
	s.gateWrites++
	return nil
}

func (s *testStore) RecordPolicyEvidence(context.Context, kernel.AccountID, kernel.ULID, int64, []policyapi.Receipt) error {
	if s.fail == "receipt-write" {
		return errSource
	}
	s.receiptWrites++
	return nil
}

type inputOwners struct {
	bad      string
	snapshot stacksapi.PolicySnapshot
	pool     workersapi.PoolMetadata
	space    accessapi.PolicySpace
	roles    []accessapi.PolicyRole
	contexts []string
	reads    int
}

func (o *inputOwners) PolicySnapshot(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.PolicySnapshot, error) {
	o.reads++
	if o.bad == "stack" {
		return stacksapi.PolicySnapshot{}, errSource
	}
	return o.snapshot, nil
}

func (o *inputOwners) PoolMetadata(context.Context, kernel.AccountID, kernel.ULID) (workersapi.PoolMetadata, error) {
	if o.bad == "pool" {
		return workersapi.PoolMetadata{}, errSource
	}
	if o.bad == "missing-pool" {
		return workersapi.PoolMetadata{}, workersapi.ErrNoPool
	}
	return o.pool, nil
}

func (o *inputOwners) StackRoles(context.Context, kernel.AccountID, kernel.ULID) ([]accessapi.PolicyRole, error) {
	if o.bad == "roles" {
		return nil, errSource
	}
	return o.roles, nil
}

func (o *inputOwners) PolicySpace(context.Context, kernel.AccountID, kernel.ULID) (accessapi.PolicySpace, error) {
	if o.bad == "space" {
		return accessapi.PolicySpace{}, errSource
	}
	return o.space, nil
}

func (o *inputOwners) PolicyContextSlugs(context.Context, kernel.AccountID, kernel.ULID) ([]string, error) {
	if o.bad == "contexts" {
		return nil, errSource
	}
	return o.contexts, nil
}

type testInputs struct {
	facts policyapi.RunInputFacts
	err   error
}

func (i *testInputs) BuildRunInput(_ context.Context, f policyapi.RunInputFacts) (json.RawMessage, error) {
	i.facts = f
	if i.err != nil {
		return nil, i.err
	}
	return json.RawMessage(`{}`), nil
}

type (
	testObjects struct {
		err    error
		body   string
		closed bool
	}
	objectReader struct {
		io.Reader
		owner *testObjects
	}
)

func (r objectReader) Close() error { r.owner.closed = true; return nil }
func (o *testObjects) Open(context.Context, objectstore.Name, string) (io.ReadCloser, error) {
	if o.err != nil {
		return nil, o.err
	}
	return objectReader{Reader: strings.NewReader(o.body), owner: o}, nil
}

func inputFixture() (*Service, *testStore, *inputOwners, *testInputs, *testObjects) {
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	account, stack, pool, space := kernel.NewAccountID(), kernel.NewULID(), kernel.NewULID(), kernel.NewULID()
	r := lifecycle.Run{
		Account: account, ID: kernel.NewULID(), Stack: stack, State: kernel.RunStateUnconfirmed, Version: 6, Type: kernel.RunTypeTracked,
		CreatedAt: at, UpdatedAt: at, WorkerPool: &pool, Commit: stacksapi.Commit{AuthorName: "Display", AuthorLogin: new("login"), Timestamp: at, Branch: "main", Hash: "head"},
	}
	s := &testStore{run: r}
	o := &inputOwners{
		snapshot: stacksapi.PolicySnapshot{Settings: stacksapi.Settings{
			Stack:         stacksapi.Stack{ID: stack, Account: account, Space: space, Slug: "infra", Name: "Infra"},
			Vendor:        "terraform",
			VendorConfig:  json.RawMessage(`{}`),
			Branch:        "main",
			TrackedCommit: &stacksapi.Commit{AuthorName: "Display", AuthorLogin: new("login"), Timestamp: at},
		}, State: kernel.StackState("UNCONFIRMED"), Labels: []string{"stack-label"}},
		pool: workersapi.PoolMetadata{
			ID:     pool,
			Name:   "Private",
			Labels: []string{"pool-label"},
		},
		space:    accessapi.PolicySpace{ID: space, Slug: "team", Name: "Team", Labels: []string{"space-label"}},
		roles:    []accessapi.PolicyRole{{ID: kernel.NewULID(), Slug: "writer", Name: "Writer"}},
		contexts: []string{"first", "second"},
	}
	i, obj := &testInputs{}, &testObjects{body: `{"version":1,"changes":[]}`}
	return New(Deps{Store: s, Stacks: o, Pools: o, Roles: o, SpaceFacts: o, Contexts: o, Inputs: i, Objects: obj, Now: func() time.Time { return at }}), s, o, i, obj
}

func TestUnitApprovalInputKeepsOwnerMetadataAndClosesArtifacts(t *testing.T) {
	s, store, owners, inputs, objects := inputFixture()
	store.changeKey = "changes"
	if err := tx.Do(t.Context(), testDB{}, tx.Options{}, func(ctx context.Context) error { _, err := s.input(ctx, store.run); return err }); err != nil {
		t.Fatal(err)
	}
	f := inputs.facts
	if !objects.closed || f.Stack.Space.Value == nil || f.Stack.Space.Value.ID != "team" || f.Stack.Space.Value.Labels.Value == nil ||
		f.Stack.Roles.Value == nil || (*f.Stack.Roles.Value)[0].ID != owners.roles[0].ID || f.Stack.Contexts.Value == nil || len(*f.Stack.Contexts.Value) != 2 ||
		f.Stack.TrackedCommit.Value == nil || f.Stack.TrackedCommit.Value.Author != "login" || f.Stack.TrackedCommit.Value.Branch != "main" ||
		f.Run.Flags.Value == nil || (*f.Run.Flags.Value)[0] != "owned-flag" || f.Run.Changes.Value == nil || len(*f.Run.Changes.Value) != 0 || f.Reviews.Value == nil {
		t.Fatal("owner-backed metadata or known-empty changes/reviews were lost")
	}
	owners.snapshot.Settings.Lock = &stacksapi.Lock{By: "locker"}
	owners.snapshot.Settings.ProjectRoot = new("infra")
	owners.snapshot.Settings.TrackedCommit.AuthorLogin = nil
	if err := tx.Do(t.Context(), testDB{}, tx.Options{}, func(ctx context.Context) error { _, err := s.input(ctx, store.run); return err }); err != nil {
		t.Fatal(err)
	}
	if inputs.facts.Stack.LockedBy.Value == nil || *inputs.facts.Stack.LockedBy.Value != "locker" || inputs.facts.Stack.ProjectRoot != "infra" ||
		inputs.facts.Stack.TrackedCommit.Value.Author != "Display" {
		t.Fatal("stored lock/root/author fallback was not projected")
	}
}

func TestUnitApprovalInputSourceFailuresDoNotBecomePolicyFree(t *testing.T) {
	for _, phase := range []string{"lock", "evidence", "missing-evidence", "reviews", "invalid-review", "stack", "roles", "contexts", "space", "pool", "changes", "typed", "workflow"} {
		t.Run(phase, func(t *testing.T) {
			s, store, owners, inputs, _ := inputFixture()
			want := errSource
			switch phase {
			case "lock", "evidence", "reviews", "changes":
				store.fail = phase
			case "missing-evidence":
				store.fail = phase
				want = kernel.ErrUnavailable
			case "invalid-review":
				store.reviews = []review.Record{{AuthorKey: "private-review-marker"}}
				want = kernel.ErrUnavailable
			case "typed":
				inputs.err = policyapi.ErrInput
				want = policyapi.ErrInput
			case "workflow":
				owners.snapshot.Settings.VendorConfig = json.RawMessage(`{"workflowTool":false}`)
				want = policyapi.ErrInput
			default:
				owners.bad = phase
			}
			err := tx.Do(t.Context(), testDB{}, tx.Options{}, func(ctx context.Context) error { _, err := s.input(ctx, store.run); return err })
			if !errors.Is(err, want) || store.gateWrites != 0 || store.receiptWrites != 0 {
				t.Fatalf("source %s did not fail safely: %v", phase, err)
			}
			if strings.Contains(err.Error(), "private-review-marker") {
				t.Fatal("invalid review metadata echoed into error")
			}
		})
	}
}

func TestUnitApprovalArtifactsPreserveAbsenceAndFailClosed(t *testing.T) {
	for _, phase := range []string{"missing-pool", "no-private-pool", "no-tracked-commit", "missing-object", "object-down", "malformed", "unsupported-vendor"} {
		t.Run(phase, func(t *testing.T) {
			s, store, owners, inputs, obj := inputFixture()
			var want error
			switch phase {
			case "missing-pool":
				owners.bad = phase
			case "no-private-pool":
				store.run.WorkerPool = nil
			case "no-tracked-commit":
				owners.snapshot.Settings.TrackedCommit = nil
			case "missing-object":
				store.changeKey = "changes"
				obj.err = objectstore.ErrNotFound
				want = policyapi.ErrInput
			case "object-down":
				store.changeKey = "changes"
				obj.err = errSource
				want = errSource
			case "malformed":
				store.changeKey = "changes"
				obj.body = "private-invalid-object"
				want = policyapi.ErrInput
			case "unsupported-vendor":
				store.changeKey = "changes"
				owners.snapshot.Settings.Vendor = "pulumi"
				want = policyapi.ErrInput
			}
			err := tx.Do(t.Context(), testDB{}, tx.Options{}, func(ctx context.Context) error { _, err := s.input(ctx, store.run); return err })
			if !errors.Is(err, want) {
				t.Fatalf("artifact %s: %v want %v", phase, err, want)
			}
			if err == nil && (phase == "missing-pool" || phase == "no-private-pool") && inputs.facts.Stack.WorkerPool.Known {
				t.Fatal("unmaterialized pool became a fact")
			}
			if err == nil && phase == "no-tracked-commit" && (!inputs.facts.Stack.TrackedCommit.Known || inputs.facts.Stack.TrackedCommit.Value != nil) {
				t.Fatal("known missing tracked commit lost its null")
			}
		})
	}
}

type evaluationResult struct {
	out     policyapi.Evaluation
	err     error
	request policyapi.EvaluationRequest
}

func (e *evaluationResult) Evaluate(_ context.Context, r policyapi.EvaluationRequest) (policyapi.Evaluation, error) {
	e.request = r
	return e.out, e.err
}

func TestUnitApprovalEvaluationFencesAndPersistenceFailures(t *testing.T) {
	for _, phase := range []string{"policy-free", "lock", "stale-version", "stale-state", "evaluation", "wrong-type", "wrong-outcome", "gate-write", "receipt-write"} {
		t.Run(phase, func(t *testing.T) {
			s, store, owners, _, _ := inputFixture()
			requested := store.run
			e := &evaluationResult{out: policyapi.Evaluation{Type: policyapi.Approval, Outcome: policyapi.NoPolicies}}
			s.d.Evaluator = e
			var want error
			switch phase {
			case "lock":
				store.fail = phase
				want = errSource
			case "stale-version":
				requested.Version--
				want = lifecycle.ErrConflict
			case "stale-state":
				requested.State = kernel.RunStateQueued
				want = lifecycle.ErrConflict
			case "evaluation":
				e.err = errSource
				want = errSource
			case "wrong-type":
				e.out.Type = policyapi.Plan
				want = evidence.ErrReceipt
			case "wrong-outcome":
				e.out.Outcome = policyapi.Deny
				want = evidence.ErrReceipt
			case "gate-write", "receipt-write":
				store.fail = phase
				want = errSource
			}
			err := tx.Do(t.Context(), testDB{}, tx.Options{}, func(ctx context.Context) error { _, err := s.Evaluate(ctx, requested); return err })
			if !errors.Is(err, want) {
				t.Fatalf("evaluation %s: %v want %v", phase, err, want)
			}
			if phase == "policy-free" &&
				(owners.reads != 0 || store.gateWrites != 1 || store.receiptWrites != 1 || e.request.Type != policyapi.Approval || e.request.Target.ID != requested.Stack || e.request.Run == nil || *e.request.Run != requested.ID) {
				t.Fatal("policy-free result materialized input or lost its target/evidence fence")
			}
		})
	}
	s, store, _, _, _ := inputFixture()
	if _, err := s.Evaluate(t.Context(), store.run); !errors.Is(err, lifecycle.ErrNoTransaction) {
		t.Fatalf("evaluation outside transaction: %v", err)
	}
}
