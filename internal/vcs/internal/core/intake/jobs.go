package intake

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type job struct {
	Account kernel.AccountID `json:"account"`
	ID      kernel.ULID      `json:"id"`
	Replays int64            `json:"replays"`
}

// Processed parses a verified delivery, then commits its state and the job's
// completion together. A stale lease rolls the state change back too.
func (s *Service) Processed(ctx context.Context, j queue.Job) error {
	var p job
	if json.Unmarshal(j.Payload, &p) != nil || p.Account.IsZero() || p.ID.IsZero() || p.Account.ULID != j.Account {
		return queue.Permanent(errPayload) //nolint:wrapcheck // marks the fixed payload refusal permanent
	}
	d, err := s.d.Store.Read(ctx, p.Account, p.ID)
	if errors.Is(err, ErrNotFound) {
		return s.complete(ctx, j, p, "")
	}
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	if d.State != Received || d.Replays != p.Replays {
		return s.complete(ctx, j, p, "")
	}
	if !s.d.Now().Before(d.Deadline) {
		return s.complete(ctx, j, p, Expired)
	}
	body, err := s.body(ctx, d)
	if err != nil {
		return s.failed(ctx, j, p, errBody)
	}
	provider := s.d.Providers[d.Provider]
	if provider == nil {
		return s.failed(ctx, j, p, errParse)
	}
	events, err := provider.Parse(d.Headers, body)
	if err != nil {
		// A parser error may contain the raw payload. Only this fixed error
		// reaches last_error, queue errors and logs.
		return s.failed(ctx, j, p, errParse)
	}
	var plan DispatchPlan
	if s.d.Dispatcher != nil {
		plan, err = s.d.Dispatcher.Prepare(ctx, d, events)
		if err != nil {
			now := s.d.Now()
			if !now.Before(d.Deadline) {
				return s.complete(ctx, j, p, Expired)
			}
			// Provider diagnostics may echo raw data or credentials. Only
			// the bounded reset time survives a rate-limit refusal.
			if limited, ok := errors.AsType[*port.RateLimited](err); ok {
				return queue.Snooze(min(max(time.Second, limited.Reset.Sub(now)), d.Deadline.Sub(now))) //nolint:wrapcheck // the queue interprets its scheduling control
			}
			return s.failed(ctx, j, p, errDispatch)
		}
	}
	state := Dispatched
	if !s.d.Now().Before(d.Deadline) {
		state = Expired
	}
	if state == Expired {
		return s.complete(ctx, j, p, state)
	}
	return s.dispatched(ctx, j, p, plan)
}

func (s *Service) dispatched(ctx context.Context, j queue.Job, p job, plan DispatchPlan) error {
	return tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error { //nolint:wrapcheck // each fenced step names its boundary
		if err := s.d.Jobs.HoldTx(ctx, j); err != nil {
			return err //nolint:wrapcheck // the queue names the lost claim
		}
		current, err := s.d.Store.Hold(ctx, Ref{Account: p.Account, ID: p.ID, Replays: p.Replays, State: Received})
		if err != nil {
			return err //nolint:wrapcheck // the store names the generation hold
		}
		if !current {
			return s.d.Jobs.CompleteTx(ctx, j)
		}
		if plan != nil {
			if err := plan.Commit(ctx); err != nil {
				return err //nolint:wrapcheck // the dispatcher names the prepared write
			}
		}
		if err := s.d.Store.State(ctx, p.Account, p.ID, p.Replays, Dispatched, ""); err != nil {
			return err //nolint:wrapcheck // the store names the metadata write
		}
		return s.d.Jobs.CompleteTx(ctx, j) //nolint:wrapcheck // the queue names the completion
	})
}

func (s *Service) body(ctx context.Context, d Delivery) ([]byte, error) {
	if d.BodyKey == "" {
		return d.Body, nil
	}
	r, err := s.d.Objects.Open(ctx, objectstore.Inbox, d.BodyKey)
	if err != nil {
		return nil, errBody
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, MaxBody+1))
	if err != nil || len(b) > MaxBody {
		return nil, errBody
	}
	return b, nil
}

func (s *Service) complete(ctx context.Context, j queue.Job, p job, state string) error {
	//nolint:wrapcheck // each fenced transaction step names its failure
	return tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		if state != "" {
			if err := s.d.Jobs.HoldTx(ctx, j); err != nil {
				return err //nolint:wrapcheck // the queue names a lost claim
			}
			if err := s.d.Store.State(ctx, p.Account, p.ID, p.Replays, state, ""); err != nil {
				return err //nolint:wrapcheck // the store names the state write
			}
		}
		return s.d.Jobs.CompleteTx(ctx, j)
	})
}

func (s *Service) failed(ctx context.Context, j queue.Job, p job, cause error) error {
	state := Received
	if j.Attempt == j.MaxAttempts {
		state = Failed
	}
	if err := tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		if err := s.d.Jobs.HoldTx(ctx, j); err != nil {
			return err //nolint:wrapcheck // the queue names a lost claim
		}
		return s.d.Store.State(ctx, p.Account, p.ID, p.Replays, state, cause.Error())
	}); err != nil {
		return err //nolint:wrapcheck // the fenced write names its failure
	}
	return cause
}

// Pruned removes expired delivery rows and schedules their blob deletion in the
// same transaction. A failed prune is safely retried by the hourly task.
func (s *Service) Pruned(ctx context.Context, j queue.Job) error {
	before := s.d.Now().Add(-s.d.Retention)
	for {
		n, err := s.drop(ctx, nil, &before)
		if err != nil {
			return err
		}
		if n < PruneBatch {
			return nil
		}
	}
}

// Purge drops a deleted account's deliveries without affecting another account.
func (s *Service) Purge(ctx context.Context, acct kernel.AccountID) error {
	for {
		n, err := s.drop(ctx, &acct, nil)
		if err != nil {
			return err
		}
		if n < PruneBatch {
			return nil
		}
	}
}

func (s *Service) drop(ctx context.Context, acct *kernel.AccountID, before *time.Time) (int, error) {
	n := 0
	err := tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		keys, count, err := s.d.Store.Drop(ctx, acct, before, PruneBatch)
		if err != nil {
			return err //nolint:wrapcheck // the store names the removal
		}
		n = count
		for _, key := range keys {
			if err := s.d.Objects.Delete(ctx, objectstore.Inbox, key); err != nil {
				return err //nolint:wrapcheck // the blob lifecycle names its deletion
			}
		}
		return nil
	})
	return n, err //nolint:wrapcheck // each step names itself
}
