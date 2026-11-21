package initialization_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/evidence"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/initialization"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/policyinput"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

var errUnavailable = errors.New("owner unavailable")

type transaction struct{ pgx.Tx }

func (transaction) Commit(context.Context) error   { return nil }
func (transaction) Rollback(context.Context) error { return nil }

type database struct{}

func (database) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) { return transaction{}, nil }

// These unit ports isolate operational error propagation. The workflow
// acceptance runs the actual policy owner/OPA and PostgreSQL separately.
type sources struct {
	initialization.Store
	run   lifecycle.Run
	fail  string
	locks int
}

func (s *sources) Lock(context.Context, kernel.AccountID, kernel.ULID) (lifecycle.Run, error) {
	s.locks++
	if s.fail == "lock" || s.fail == "input" && s.locks > 1 {
		return lifecycle.Run{}, errUnavailable
	}
	return s.run, nil
}

func (s *sources) PolicyEvidence(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID]evidence.Snapshot, error) {
	return map[kernel.ULID]evidence.Snapshot{s.run.ID: evidence.Empty()}, nil
}

func (s *sources) RecordPolicyEvidence(context.Context, kernel.AccountID, kernel.ULID, int64, []policyapi.Receipt) error {
	if s.fail == "record" {
		return errUnavailable
	}
	return nil
}

func (s *sources) PolicySnapshot(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.PolicySnapshot, error) {
	if s.fail == "stack" {
		return stacksapi.PolicySnapshot{}, errUnavailable
	}
	return stacksapi.PolicySnapshot{}, nil
}

func (*sources) StackRoles(context.Context, kernel.AccountID, kernel.ULID) ([]accessapi.PolicyRole, error) {
	return nil, errUnavailable
}

type evaluator struct {
	build bool
	fail  bool
}

func (e evaluator) Evaluate(ctx context.Context, request policyapi.EvaluationRequest) (policyapi.Evaluation, error) {
	if e.fail {
		return policyapi.Evaluation{}, errUnavailable
	}
	if e.build {
		if _, err := request.BuildInput(ctx); err != nil {
			return policyapi.Evaluation{}, err
		}
	}
	return policyapi.Evaluation{Type: policyapi.Initialization, Outcome: policyapi.NoPolicies}, nil
}

func TestUnitInitializationOperationalFailuresStayErrors(t *testing.T) {
	for _, failure := range []string{"lock", "evaluate", "record", "input", "stack", "roles"} {
		t.Run(failure, func(t *testing.T) {
			run := lifecycle.Run{Account: kernel.NewAccountID(), ID: kernel.NewULID(), Stack: kernel.NewULID(), State: kernel.RunStateReady, Version: 2}
			source := &sources{run: run, fail: failure}
			service := initialization.New(initialization.Deps{
				Store: source, Stacks: source, Metadata: policyinput.Metadata{Roles: source},
				Evaluator: evaluator{build: failure == "input" || failure == "stack" || failure == "roles", fail: failure == "evaluate"},
			})
			err := tx.Do(t.Context(), database{}, tx.Options{}, func(ctx context.Context) error {
				_, err := service.Evaluate(ctx, run)
				return err
			})
			if !errors.Is(err, errUnavailable) {
				t.Fatalf("operational %s failure became an authorization decision: %v", failure, err)
			}
		})
	}
}

func TestUnitInitializationRequiresCurrentReadyTransaction(t *testing.T) {
	run := lifecycle.Run{Account: kernel.NewAccountID(), ID: kernel.NewULID(), State: kernel.RunStateReady, Version: 2}
	source := &sources{run: run}
	service := initialization.New(initialization.Deps{Store: source, Evaluator: evaluator{}})
	if _, err := service.Evaluate(t.Context(), run); !errors.Is(err, lifecycle.ErrNoTransaction) {
		t.Fatalf("evaluation outside transition transaction: %v", err)
	}
	if err := tx.Do(t.Context(), database{}, tx.Options{}, func(ctx context.Context) error {
		stale := run
		stale.Version--
		if _, err := service.Evaluate(ctx, stale); err == nil {
			t.Fatal("stale state version evaluated")
		}
		result, err := service.Evaluate(ctx, run)
		if err != nil || result.Outcome != policyapi.NoPolicies {
			t.Fatalf("policy-free assignment demanded unavailable input facts: %+v %v", result, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
