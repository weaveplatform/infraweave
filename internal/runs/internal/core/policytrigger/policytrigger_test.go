package policytrigger_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/evidence"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/policyinput"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/policytrigger"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

var errOwner = errors.New("required owner unavailable")

// Faults enter through published owner ports. Atomic fan-out and actual lease
// generations are tested against PostgreSQL and both production replicas.
func TestUnitTriggerOperationalFailuresRemainRetryable(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		want error
	}{
		{"queue fence", errOwner},
		{"account fence", errOwner},
		{"tree hold", errOwner},
		{"tree read", errOwner},
		{"inventory read", errOwner},
		{"stack space hold", errOwner},
		{"stack settings", errOwner},
		{"changed stack owner", kernel.ErrUnavailable},
		{"source inventory missing", kernel.ErrUnavailable},
		{"source read", errOwner},
		{"evaluation unavailable", errOwner},
		{"wrong evaluation type", evidence.ErrReceipt},
		{"missing trigger decision", evidence.ErrReceipt},
		{"source snapshot", errOwner},
		{"queued counts", errOwner},
		{"missing queued count", kernel.ErrUnavailable},
		{"policy stack snapshot", errOwner},
		{"stack metadata", errOwner},
		{"source history", errOwner},
		{"missing source history", kernel.ErrUnavailable},
		{"stale source history", kernel.ErrUnavailable},
		{"source changes", errOwner},
		{"input projection", errOwner},
		{"policy identities", errOwner},
		{"changed policy version", kernel.ErrUnavailable},
		{"invalid receipt decision", kernel.ErrUnavailable},
		{"child creation", errOwner},
		{"source evidence", errOwner},
		{"source notification", errOwner},
		{"completion fence", queue.ErrLeaseLost},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			f := newTriggerFixture(t, test.name)
			if err := f.service.Handle(t.Context(), f.job); !errors.Is(err, test.want) {
				t.Fatalf("owner refusal became a successful decision: got %v, want %v", err, test.want)
			}
			if f.commits != 0 {
				t.Fatal("failed trigger acknowledged a transaction commit")
			}
		})
	}
}

func TestUnitTriggerPriorityAppliesOnlyToPrivateTargets(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		phase string
		want  kernel.RunPriority
	}{
		{"public prioritized target", ""},
		{"private prioritized target", kernel.RunPriorityHigh},
	} {
		t.Run(test.phase, func(t *testing.T) {
			t.Parallel()
			f := newTriggerFixture(t, test.phase)
			if test.want != "" {
				pool := kernel.NewULID()
				f.settings.WorkerPool = &pool
			}
			if err := f.service.Handle(t.Context(), f.job); err != nil {
				t.Fatal(err)
			}
			if len(f.created) != 1 || f.created[0].Priority != test.want || f.commits != 1 {
				t.Fatalf("trigger targets %+v committed %d times, want priority %q", f.created, f.commits, test.want)
			}
		})
	}
}

func TestUnitTriggerObsoleteSourcesSettleWithoutNewEvidence(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{"deleted source", "different source stack", "old source version", "nonterminal source", "proposed source", "no policies", "unbound evaluator"} {
		t.Run(reason, func(t *testing.T) {
			t.Parallel()
			f := newTriggerFixture(t, reason)
			if err := f.service.Handle(t.Context(), f.job); err != nil {
				t.Fatal(err)
			}
			if f.commits != 1 || f.evidenceWrites != 0 {
				t.Fatal("obsolete source was not settled without publishing new evidence")
			}
		})
	}
}

func TestUnitTriggerInvalidQueueIdentityNeverOpensATransaction(t *testing.T) {
	t.Parallel()
	for _, body := range []string{`null`, `[]`, `{}`, `{"run":"invalid"}`, `{"run":"01KACME0000000000000000001","stack":"01KACME0000000000000000002","stateVersion":-1}`} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			f := newTriggerFixture(t, "")
			f.job.Payload = json.RawMessage(body)
			if err := f.service.Handle(t.Context(), f.job); !errors.Is(err, kernel.ErrUnavailable) || f.begins != 0 {
				t.Fatal("malformed job crossed its identity boundary")
			}
		})
	}
	f := newTriggerFixture(t, "")
	f.job.Account = kernel.ULID{}
	if err := f.service.Handle(t.Context(), f.job); !errors.Is(err, kernel.ErrUnavailable) || f.begins != 0 {
		t.Fatal("accountless job opened a transaction")
	}
}

type triggerFixture struct {
	policytrigger.Store
	phase                        string
	service                      *policytrigger.Service
	job                          queue.Job
	run                          lifecycle.Run
	settings                     stacksapi.Settings
	policy                       kernel.ULID
	begins, commits, sourceReads int
	evidenceWrites               int
	created                      []runsapi.CreateRequest
}

func newTriggerFixture(t *testing.T, phase string) *triggerFixture {
	t.Helper()
	account, stack, space := kernel.NewAccountID(), kernel.NewULID(), kernel.NewULID()
	now := time.Now().UTC()
	f := &triggerFixture{
		phase: phase, policy: kernel.NewULID(),
		run: lifecycle.Run{
			Account: account, ID: kernel.NewULID(), Stack: stack, Type: kernel.RunTypeTracked, State: kernel.RunStateCanceled,
			Version: 2, Priority: kernel.RunPriorityNormal, CreatedAt: now, UpdatedAt: now,
		},
		settings: stacksapi.Settings{
			Stack:  stacksapi.Stack{ID: stack, Account: account, Space: space, Slug: "source", Name: "Source"},
			Vendor: "terraform", TrackedCommit: &stacksapi.Commit{Hash: "tracked-source"},
		},
	}
	body, err := json.Marshal(lifecycle.PolicyTrigger{Run: f.run.ID, Stack: stack, StateVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	f.job = queue.Job{ID: 1, Kind: lifecycle.PolicyTriggerKind.Name, Account: account.ULID, Payload: body, Generation: 1}
	spaces, stacks := triggerSpaces{f: f}, triggerStacks{f: f}
	d := policytrigger.Deps{
		DB: f, Store: f, Jobs: f, Spaces: spaces, Stacks: stacks, Snapshots: f, Identities: f, Evaluator: f, Inputs: f, Creator: f,
		Metadata:  policyinput.Metadata{Store: f, Roles: f, Contexts: f, SpaceFacts: f, Pools: f},
		Lifecycle: lifecycle.New(lifecycle.Deps{Stacks: stacks, Events: f}),
	}
	if phase == "unbound evaluator" {
		d.Evaluator = nil
	}
	f.service = policytrigger.New(d)
	switch phase {
	case "different source stack":
		f.run.Stack = kernel.NewULID()
	case "old source version":
		f.run.Version++
	case "nonterminal source":
		f.run.State = kernel.RunStateQueued
	case "proposed source":
		f.run.Type = kernel.RunTypeProposed
	}
	return f
}

func (f *triggerFixture) fail(phase string) error {
	if f.phase == phase {
		return errOwner
	}
	return nil
}

type triggerTx struct {
	pgx.Tx
	f *triggerFixture
}

func (f *triggerFixture) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	f.begins++
	return triggerTx{f: f}, nil
}

func (t triggerTx) Commit(context.Context) error   { t.f.commits++; return nil }
func (t triggerTx) Rollback(context.Context) error { return nil }

func (f *triggerFixture) HoldTx(context.Context, queue.Job) error { return f.fail("queue fence") }

func (f *triggerFixture) CompleteTx(context.Context, queue.Job) error {
	if f.phase == "completion fence" {
		return queue.ErrLeaseLost
	}
	return nil
}

func (f *triggerFixture) HoldPolicyTriggers(context.Context, kernel.AccountID) error {
	return f.fail("account fence")
}

type triggerSpaces struct {
	accessapi.Spaces
	f *triggerFixture
}

func (s triggerSpaces) Hold(_ context.Context, account kernel.AccountID, space kernel.ULID) error {
	if space == account.ULID {
		return s.f.fail("tree hold")
	}
	return s.f.fail("stack space hold")
}

func (s triggerSpaces) Tree(context.Context, kernel.AccountID) ([]accessapi.SpaceNode, error) {
	return []accessapi.SpaceNode{{ID: s.f.settings.Space}}, s.f.fail("tree read")
}

type triggerStacks struct {
	stacksapi.Stacks
	f *triggerFixture
}

func (s triggerStacks) InSpaces(context.Context, kernel.AccountID, []kernel.ULID) ([]stacksapi.Stack, error) {
	if s.f.phase == "source inventory missing" {
		return []stacksapi.Stack{}, nil
	}
	return []stacksapi.Stack{s.f.settings.Stack}, s.f.fail("inventory read")
}

func (s triggerStacks) LockForRun(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.Settings, error) {
	settings := s.f.settings
	if s.f.phase == "changed stack owner" {
		settings.Account = kernel.NewAccountID()
	}
	return settings, s.f.fail("stack settings")
}

func (s triggerStacks) Get(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.Stack, error) {
	return s.f.settings.Stack, nil
}

func (f *triggerFixture) Lock(context.Context, kernel.AccountID, kernel.ULID) (lifecycle.Run, error) {
	f.sourceReads++
	if f.phase == "deleted source" {
		return lifecycle.Run{}, lifecycle.ErrNotFound
	}
	if f.phase == "source snapshot" && f.sourceReads > 1 {
		return lifecycle.Run{}, errOwner
	}
	return f.run, f.fail("source read")
}

func (f *triggerFixture) QueuedForPolicyTrigger(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID]int64, error) {
	if f.phase == "missing queued count" {
		return map[kernel.ULID]int64{}, nil
	}
	return map[kernel.ULID]int64{f.run.Stack: 0}, f.fail("queued counts")
}

func (f *triggerFixture) PolicyEvidence(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID]evidence.Snapshot, error) {
	return map[kernel.ULID]evidence.Snapshot{f.run.ID: evidence.Empty()}, nil
}

func (f *triggerFixture) Histories(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID][]lifecycle.Step, error) {
	if f.phase == "missing source history" {
		return map[kernel.ULID][]lifecycle.Step{}, nil
	}
	version := f.run.Version
	if f.phase == "stale source history" {
		version--
	}
	return map[kernel.ULID][]lifecycle.Step{f.run.ID: {{Sequence: version, State: f.run.State}}}, f.fail("source history")
}

func (f *triggerFixture) PolicySnapshot(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.PolicySnapshot, error) {
	return stacksapi.PolicySnapshot{Settings: f.settings}, f.fail("policy stack snapshot")
}

func (f *triggerFixture) StackRoles(context.Context, kernel.AccountID, kernel.ULID) ([]accessapi.PolicyRole, error) {
	return []accessapi.PolicyRole{}, f.fail("stack metadata")
}

func (f *triggerFixture) PolicyContextSlugs(context.Context, kernel.AccountID, kernel.ULID) ([]string, error) {
	return []string{}, nil
}

func (f *triggerFixture) PolicySpace(context.Context, kernel.AccountID, kernel.ULID) (accessapi.PolicySpace, error) {
	return accessapi.PolicySpace{ID: f.settings.Space, Slug: "legacy", Name: "Legacy"}, nil
}

func (f *triggerFixture) PoolMetadata(context.Context, kernel.AccountID, kernel.ULID) (workersapi.PoolMetadata, error) {
	return workersapi.PoolMetadata{}, workersapi.ErrNoPool
}

func (f *triggerFixture) Changes(context.Context, lifecycle.Run) (string, bool, error) {
	return "", false, f.fail("source changes")
}

func (f *triggerFixture) BuildRunInput(context.Context, policyapi.RunInputFacts) (json.RawMessage, error) {
	return json.RawMessage(`{}`), f.fail("input projection")
}

func (f *triggerFixture) Evaluate(ctx context.Context, request policyapi.EvaluationRequest) (policyapi.Evaluation, error) {
	if err := f.fail("evaluation unavailable"); err != nil {
		return policyapi.Evaluation{}, err
	}
	if f.phase == "no policies" {
		return policyapi.Evaluation{Type: policyapi.Trigger, Outcome: policyapi.NoPolicies}, nil
	}
	if _, err := request.BuildInput(ctx); err != nil {
		return policyapi.Evaluation{}, err
	}
	result := policyapi.Evaluation{
		Type: policyapi.Trigger, HasPolicies: true, Outcome: policyapi.Ignore,
		Trigger: &policyapi.TriggerDecision{Targets: []string{}, Priority: "normal"},
		Receipts: []policyapi.Receipt{{
			ID: kernel.NewULID(), Policy: f.policy, PolicyVersion: 1, Type: policyapi.Trigger,
			PolicyName: "Guard", Engine: "REGO_V1", Outcome: policyapi.Ignore, Decision: json.RawMessage(`{}`), CreatedAt: time.Now().UTC(),
		}},
	}
	switch f.phase {
	case "wrong evaluation type":
		result.Type = policyapi.Plan
	case "missing trigger decision":
		result.Trigger = nil
	case "invalid receipt decision":
		result.Receipts[0].Decision = json.RawMessage(`[]`)
	case "child creation", "public prioritized target", "private prioritized target":
		result.Outcome = policyapi.TriggerRuns
		result.Trigger.Targets = []string{"source"}
		result.Receipts[0].Outcome = policyapi.TriggerRuns
		result.Receipts[0].Decision = json.RawMessage(`{"trigger":["source"]}`)
		if f.phase != "child creation" {
			result.Trigger.Priority = string(kernel.RunPriorityHigh)
		}
	}
	return result, nil
}

func (f *triggerFixture) Identities(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID]policyapi.PolicyIdentity, error) {
	version := int64(1)
	if f.phase == "changed policy version" {
		version++
	}
	return map[kernel.ULID]policyapi.PolicyIdentity{f.policy: {ID: f.policy, Space: f.settings.Space, Type: policyapi.Trigger, Version: version}}, f.fail("policy identities")
}

func (f *triggerFixture) Create(_ context.Context, request runsapi.CreateRequest) (runsapi.CreatedRun, error) {
	f.created = append(f.created, request)
	return runsapi.CreatedRun{ID: kernel.NewULID(), Created: true}, f.fail("child creation")
}

func (f *triggerFixture) RecordPolicyEvidence(context.Context, kernel.AccountID, kernel.ULID, int64, []policyapi.Receipt) error {
	f.evidenceWrites++
	return f.fail("source evidence")
}

func (f *triggerFixture) Append(_ context.Context, event outbox.Event) (outbox.Event, error) {
	return event, f.fail("source notification")
}

var _ tx.Beginner = (*triggerFixture)(nil)
