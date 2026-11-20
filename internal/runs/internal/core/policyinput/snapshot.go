package policyinput

import (
	"context"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/evidence"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
)

// Store reads the current run under its state fence and the committed evidence
// for the current and immediately preceding account-owned stack run.
type Store interface {
	Lock(context.Context, kernel.AccountID, kernel.ULID) (lifecycle.Run, error)
	PreviousPolicyRun(context.Context, lifecycle.Run) (lifecycle.Run, bool, error)
	PolicyEvidence(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID]evidence.Snapshot, error)
	Overrides(context.Context, lifecycle.Run) ([]lifecycle.Override, error)
}

// Snapshot contains values read in the caller's evaluation transaction. Flags
// are inputs only; creating a run never copies a previous run's ownership rows.
type Snapshot struct {
	Run         lifecycle.Run
	Flags       []string
	Environment policyapi.Fact[map[string]string]
	Previous    *Snapshot
}

// Read locks the current run before reading its policy facts. Call it lazily
// after effective policy selection, so policy-free transitions need no input.
func Read(ctx context.Context, store Store, account kernel.AccountID, id kernel.ULID) (Snapshot, error) {
	return readSnapshot(ctx, store, account, id, true)
}

func readSnapshot(ctx context.Context, store Store, account kernel.AccountID, id kernel.ULID, includePrevious bool) (Snapshot, error) {
	if !tx.In(ctx) {
		return Snapshot{}, lifecycle.ErrNoTransaction
	}
	run, err := store.Lock(ctx, account, id)
	if err != nil {
		return Snapshot{}, err //nolint:wrapcheck // the owner names the read
	}
	var previous lifecycle.Run
	found := false
	if includePrevious {
		previous, found, err = store.PreviousPolicyRun(ctx, run)
		if err != nil {
			return Snapshot{}, fmt.Errorf("runs: read previous policy facts: %w", err)
		}
	}
	ids := []kernel.ULID{run.ID}
	if found {
		ids = append(ids, previous.ID)
	}
	evidence, err := store.PolicyEvidence(ctx, account, ids)
	if err != nil {
		return Snapshot{}, err //nolint:wrapcheck // the owner names the read
	}
	for _, id := range ids {
		if _, ok := evidence[id]; !ok {
			return Snapshot{}, fmt.Errorf("runs: policy evidence snapshot is missing: %w", kernel.ErrUnavailable)
		}
	}
	out, err := snapshotOf(ctx, store, run, evidence[run.ID].Flags)
	if err != nil {
		return Snapshot{}, err
	}
	if found {
		prior, err := snapshotOf(ctx, store, previous, evidence[previous.ID].Flags)
		if err != nil {
			return Snapshot{}, err
		}
		out.Previous = &prior
	}
	return out, nil
}

func snapshotOf(ctx context.Context, store Store, run lifecycle.Run, flags []string) (Snapshot, error) {
	out := Snapshot{Run: run, Flags: flags}
	if run.ResolvedConfig == nil {
		return out, nil
	}
	keys := run.ResolvedConfig.Configuration.Environment
	values := map[string]string{}
	if len(keys) != 0 {
		overrides, err := store.Overrides(ctx, run)
		if err != nil {
			return Snapshot{}, err //nolint:wrapcheck // sealed storage errors stay operational
		}
		for _, override := range overrides {
			values[override.Name] = override.Value
		}
	}
	// The record distinguishes runtime variables from proposal-only overrides.
	// Its values are redacted; the sealed row retains the original winners.
	environment := make(map[string]string, len(keys))
	for _, key := range keys {
		value, ok := values[key.Key]
		if !ok {
			return Snapshot{}, policyapi.ErrInput
		}
		environment[key.Key] = value
	}
	out.Environment = known(environment)
	return out, nil
}

// Current reads only the current run's facts for contracts without previous_run.
func Current(ctx context.Context, store Store, account kernel.AccountID, id kernel.ULID) (Snapshot, error) {
	return readSnapshot(ctx, store, account, id, false)
}
