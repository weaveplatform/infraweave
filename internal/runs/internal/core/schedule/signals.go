package schedule

import (
	"context"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// SignalStore is the signals' storage port. Each method joins the
// transaction its context carries.
type SignalStore interface {
	// Signalled reads the runs worker holds with a stop or kill no poll
	// delivered, and locks their rows FOR UPDATE.
	Signalled(ctx context.Context, worker kernel.ULID) ([]lifecycle.Run, error)
	// Delivered records the delivery of run's request of kind at at; a
	// kill's records its stop's too.
	Delivered(ctx context.Context, run lifecycle.Run, kind workersapi.Signal, at time.Time) error
}

// Signals is the runs domain's workersapi.Signals: the stops and kills
// callers asked for, which their runs' workers' polls deliver.
type Signals struct {
	store SignalStore
	now   func() time.Time
}

var _ workersapi.Signals = (*Signals)(nil)

// NewSignals returns the signals over store; now is the clock, time.Now
// when nil.
func NewSignals(store SignalStore, now func() time.Time) *Signals {
	if now == nil {
		now = time.Now
	}
	return &Signals{store: store, now: now}
}

// Deliver answers and records the signal of the run worker holds, as
// workersapi.Signals says.
//
// ref: decision 0008 § "Stop and kill"
func (s *Signals) Deliver(ctx context.Context, worker kernel.ULID) (workersapi.Signal, error) {
	if !tx.In(ctx) {
		return "", lifecycle.ErrNoTransaction
	}
	runs, err := s.store.Signalled(ctx, worker)
	if err != nil {
		return "", err //nolint:wrapcheck // the store names the read
	}
	for _, r := range runs {
		kind, ok := pending(r)
		if !ok {
			continue
		}
		at := s.now().UTC().Truncate(time.Microsecond)
		if err := s.store.Delivered(ctx, r, kind, at); err != nil {
			return "", err //nolint:wrapcheck // the store names the write
		}
		return kind, nil
	}
	return "", nil
}

// pending is r's signal no poll delivered: its kill, or else its stop
// while r's state takes one.
func pending(r lifecycle.Run) (workersapi.Signal, bool) {
	switch {
	case r.Kill != nil && r.Kill.DeliveredAt == nil:
		return workersapi.SignalKill, true
	case r.Stop != nil && r.Stop.DeliveredAt == nil && r.Stoppable():
		return workersapi.SignalStop, true
	}
	return "", false
}
