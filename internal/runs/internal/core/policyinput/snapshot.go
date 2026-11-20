package policyinput

import (
	"context"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/evidence"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
)

// Store reads the current run under its state fence and the committed evidence
// for the current and immediately preceding account-owned stack run.
type Store interface {
	Lock(context.Context, kernel.AccountID, kernel.ULID) (lifecycle.Run, error)
	PreviousPolicyRun(context.Context, lifecycle.Run) (lifecycle.Run, bool, error)
	PolicyEvidence(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID]evidence.Snapshot, error)
}

// Snapshot contains values read in the caller's evaluation transaction. Flags
// are inputs only; creating a run never copies a previous run's ownership rows.
type Snapshot struct {
	Run      lifecycle.Run
	Flags    []string
	Previous *Snapshot
}

// Read locks the current run before reading its policy facts. Call it lazily
// after effective policy selection, so policy-free transitions need no input.
func Read(ctx context.Context, store Store, account kernel.AccountID, id kernel.ULID) (Snapshot, error) {
	if !tx.In(ctx) {
		return Snapshot{}, lifecycle.ErrNoTransaction
	}
	run, err := store.Lock(ctx, account, id)
	if err != nil {
		return Snapshot{}, err //nolint:wrapcheck // the owner names the read
	}
	previous, found, err := store.PreviousPolicyRun(ctx, run)
	if err != nil {
		return Snapshot{}, err //nolint:wrapcheck // the owner names the read
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
	out := Snapshot{Run: run, Flags: evidence[run.ID].Flags}
	if found {
		out.Previous = &Snapshot{Run: previous, Flags: evidence[previous.ID].Flags}
	}
	return out, nil
}
