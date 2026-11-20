package policytrigger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/evidence"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/policyinput"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Store supplies only account-qualified run facts and evidence.
type Store interface {
	policyinput.Store
	evidence.Store
	HoldPolicyTriggers(context.Context, kernel.AccountID) error
	QueuedForPolicyTrigger(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID]int64, error)
	Histories(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID][]lifecycle.Step, error)
}

// Jobs fences and completes the same generation as the workflow writes.
type Jobs interface {
	HoldTx(context.Context, queue.Job) error
	CompleteTx(context.Context, queue.Job) error
}

// Deps are explicit owners; this consumer never joins another domain's SQL.
type Deps struct {
	DB         tx.Beginner
	Store      Store
	Jobs       Jobs
	Spaces     accessapi.Spaces
	Stacks     stacksapi.Stacks
	Snapshots  stacksapi.PolicySnapshots
	Metadata   policyinput.Metadata
	Identities policyapi.PolicyIdentities
	Evaluator  policyapi.Evaluator
	Inputs     policyapi.RunInputBuilder
	Creator    runsapi.Creator
	Lifecycle  *lifecycle.Lifecycle
	Now        func() time.Time
}

// Service runs terminal trigger decisions under their queue and owner fences.
type Service struct{ d Deps }

// New constructs the terminal policy consumer.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d}
}

// Handle commits the whole fan-out or none of it. A completed/reclaimed queue
// generation cannot repeat a decision or leave a provisional child behind.
func (s *Service) Handle(ctx context.Context, job queue.Job) error {
	var payload lifecycle.PolicyTrigger
	if json.Unmarshal(job.Payload, &payload) != nil || payload.Run.IsZero() || payload.Stack.IsZero() || payload.StateVersion < 1 || job.Account.IsZero() {
		return queue.Permanent(fmt.Errorf("%w: invalid terminal trigger job", kernel.ErrUnavailable)) //nolint:wrapcheck // retain the permanent-job classification
	}
	return tx.Do(ctx, s.d.DB, tx.Options{Serializable: true}, func(ctx context.Context) error { //nolint:wrapcheck // each owner boundary identifies its failure
		if err := s.d.Jobs.HoldTx(ctx, job); err != nil {
			return fmt.Errorf("runs: hold trigger job: %w", err)
		}
		account := kernel.AccountID{ULID: job.Account}
		owners, err := s.owners(ctx, account)
		if err != nil {
			return err
		}
		run, err := s.d.Store.Lock(ctx, account, payload.Run)
		if errors.Is(err, lifecycle.ErrNotFound) {
			return s.complete(ctx, job)
		}
		if err != nil {
			return fmt.Errorf("runs: lock trigger source: %w", err)
		}
		if run.Stack != payload.Stack || run.Version != payload.StateVersion || !run.State.Terminal() ||
			(run.Type != kernel.RunTypeTracked && run.Type != kernel.RunTypeTask) {
			return s.complete(ctx, job)
		}
		if _, exists := owners.Settings[run.Stack]; !exists {
			return kernel.ErrUnavailable
		}
		if s.d.Evaluator == nil {
			return s.complete(ctx, job)
		}
		result, err := s.evaluate(ctx, run, owners.Stacks)
		if err != nil {
			return err
		}
		if result.HasPolicies {
			if err := s.apply(ctx, run, &result, owners); err != nil {
				return err
			}
		}
		return s.complete(ctx, job)
	})
}

func (s *Service) complete(ctx context.Context, job queue.Job) error {
	if err := s.d.Jobs.CompleteTx(ctx, job); err != nil {
		return fmt.Errorf("runs: complete trigger job: %w", err)
	}
	return nil
}

func (s *Service) evaluate(ctx context.Context, run lifecycle.Run, owners []stacksapi.Stack) (policyapi.Evaluation, error) {
	result, err := s.d.Evaluator.Evaluate(ctx, policyapi.EvaluationRequest{
		Account: run.Account, Type: policyapi.Trigger, Target: policyapi.Target{Kind: policyapi.StackTarget, ID: run.Stack}, Run: &run.ID,
		BuildInput: func(ctx context.Context) (json.RawMessage, error) { return s.input(ctx, run, owners) },
	})
	if err != nil {
		return policyapi.Evaluation{}, fmt.Errorf("runs: evaluate trigger policies: %w", err)
	}
	valid := !result.HasPolicies && result.Outcome == policyapi.NoPolicies || result.HasPolicies && result.Trigger != nil &&
		(result.Outcome == policyapi.Ignore || result.Outcome == policyapi.TriggerRuns)
	if result.Type != policyapi.Trigger || !valid {
		return policyapi.Evaluation{}, evidence.ErrReceipt
	}
	return result, nil
}

func (s *Service) apply(ctx context.Context, run lifecycle.Run, result *policyapi.Evaluation, owners ownerSnapshot) error {
	targets := make(map[string]stacksapi.Settings, len(owners.Settings))
	for _, st := range owners.Settings {
		targets[st.Slug] = st
	}
	if err := s.directives(ctx, run, result, targets, owners.Tree); err != nil {
		return err
	}
	for _, name := range result.Trigger.Targets {
		target := targets[name]
		if _, err := s.d.Creator.Create(ctx, runsapi.CreateRequest{
			Account: run.Account, Stack: target.ID, Type: kernel.RunTypeTracked, Commit: runsapi.Commit(*target.TrackedCommit),
			TriggeredBy: "policy/" + owners.Settings[run.Stack].Slug + "/" + run.ID.String(), Mode: runsapi.None,
			Priority: kernel.RunPriority(result.Trigger.Priority),
		}); err != nil {
			return fmt.Errorf("runs: create trigger target: %w", err)
		}
	}
	if err := s.d.Store.RecordPolicyEvidence(ctx, run.Account, run.ID, run.Version, result.Receipts); err != nil {
		return fmt.Errorf("runs: record trigger evidence: %w", err)
	}
	if err := s.d.Lifecycle.Refresh(ctx, run); err != nil {
		return fmt.Errorf("runs: refresh trigger source: %w", err)
	}
	return nil
}
