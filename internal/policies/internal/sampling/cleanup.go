package sampling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// Maintenance is independent of the account's current policies entitlement.
var (
	CleanupKind       = queue.Kind{Name: "policies.input-cleanup", Queue: queue.Maintenance, Policy: queue.Ignore}
	DeletedPolicyKind = queue.Kind{Name: "policies.sample-retire", Queue: queue.Maintenance, Policy: queue.Ignore}
)

// CleanupEvery matches the platform blob sweep's bounded retention cadence.
const CleanupEvery = 10 * time.Minute

// CleanupBatch bounds each sweep or policy-deletion attempt.
const CleanupBatch = 500

type policyReference struct {
	Policy kernel.ULID `json:"policy"`
}

// PolicyDeleted queues bounded sample retirement in the policy deletion TX.
// Captures are shared per space/kind and survive an individual policy deletion.
func (s *Service) PolicyDeleted(ctx context.Context, account kernel.AccountID, policy kernel.ULID) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	_, err := s.d.Queue.EnqueueTx(ctx, DeletedPolicyKind, policyReference{Policy: policy}, queue.Account(account.ULID), queue.UniqueKey(policy.String()))
	if err != nil {
		return fmt.Errorf("policies: enqueue deleted sample retirement: %w", err)
	}
	return nil
}

// RetirePolicySamples leaves immutable safe metadata for historical receipts.
func (s *Service) RetirePolicySamples(ctx context.Context, j queue.Job) error {
	var ref policyReference
	if err := json.Unmarshal(j.Payload, &ref); err != nil {
		return fmt.Errorf("policies: invalid retirement identity: %w", queue.Permanent(err))
	}
	if ref.Policy.IsZero() || j.Account.IsZero() {
		return fmt.Errorf("policies: invalid retirement identity: %w", queue.Permanent(kernel.ErrULID))
	}
	account := kernel.AccountID{ULID: j.Account}
	inputs, err := s.d.Store.PolicyInputs(ctx, account, ref.Policy, CleanupBatch)
	if err != nil {
		return fmt.Errorf("policies: inspect deleted policy samples: %w", err)
	}
	for _, probe := range inputs {
		if err := s.retireProbe(ctx, probe, &j, false); err != nil {
			return err
		}
	}
	if len(inputs) == CleanupBatch {
		return fmt.Errorf("policies: continue sample retirement: %w", queue.Snooze(time.Second))
	}
	return nil
}

// Cleanup expires private staging as well as published inputs, including dead,
// cancelled or pruned upload deliveries. Probes never retain an input lock.
func (s *Service) Cleanup(ctx context.Context) error {
	due, err := s.d.Store.DueInputs(ctx, s.d.Now(), CleanupBatch)
	if err != nil {
		return fmt.Errorf("policies: inspect retained inputs: %w", err)
	}
	for _, probe := range due {
		if err := s.retireProbe(ctx, probe, nil, true); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) retireProbe(ctx context.Context, probe Input, job *queue.Job, recheck bool) error {
	err := tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		if job != nil {
			if err := s.d.Queue.HoldTx(ctx, *job); err != nil {
				return fmt.Errorf("policies: fence sample retirement: %w", err)
			}
		}
		input, err := s.d.Store.InspectInput(ctx, probe.Account, probe.ID, true)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("policies: inspect retiring input: %w", err)
		}
		if recheck {
			due, err := s.d.Store.RetirementDue(ctx, input, s.d.Now())
			if err != nil {
				return fmt.Errorf("policies: recheck input retirement: %w", err)
			}
			if !due {
				return nil
			}
		}
		state := "failed"
		if input.PublishedAt != nil {
			state = "expired"
		}
		return s.retireInput(ctx, input, state)
	})
	if err != nil {
		return fmt.Errorf("policies: retire retained input: %w", err)
	}
	return nil
}

// PurgeAccount removes all private input and metadata for a deleted account.
// One head/input is locked per TX; no multi-head lock order is accumulated.
func (s *Service) PurgeAccount(ctx context.Context, account kernel.AccountID) error {
	for {
		inputs, err := s.d.Store.AccountInputs(ctx, account, CleanupBatch)
		if err != nil {
			return fmt.Errorf("policies: inspect account input purge: %w", err)
		}
		for _, probe := range inputs {
			err := tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
				input, err := s.d.Store.InspectInput(ctx, account, probe.ID, true)
				if errors.Is(err, ErrNotFound) {
					return nil
				}
				if err != nil {
					return fmt.Errorf("policies: fence purged input: %w", err)
				}
				if err := s.d.Objects.Delete(ctx, objectstore.PolicyInputs, input.Key); err != nil {
					return fmt.Errorf("policies: drop purged input object: %w", err)
				}
				if err := s.d.Store.DropInput(ctx, input); err != nil {
					return fmt.Errorf("policies: drop purged input metadata: %w", err)
				}
				return nil
			})
			if err != nil {
				return fmt.Errorf("policies: purge account input: %w", err)
			}
		}
		if len(inputs) == 0 {
			break
		}
	}
	for {
		var done bool
		err := tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
			var err error
			done, err = s.d.Store.PurgeMetadata(ctx, account, CleanupBatch)
			if err != nil {
				return fmt.Errorf("policies: purge account evidence: %w", err)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("policies: purge account evidence transaction: %w", err)
		}
		if done {
			return nil
		}
	}
}
