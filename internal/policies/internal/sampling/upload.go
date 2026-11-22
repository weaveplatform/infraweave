package sampling

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// Upload durably records that an immutable key's one allowed Put has started.
// No transaction/connection is held during that external upload. A reclaimed
// generation only recovers publication, never repeats Put for an ambiguous key.
func (s *Service) Upload(ctx context.Context, j queue.Job) error {
	var ref reference
	if err := json.Unmarshal(j.Payload, &ref); err != nil {
		return fmt.Errorf("policies: refuse upload identity: %w", queue.Permanent(fmt.Errorf("decode input upload identity: %w", err)))
	}
	if ref.ID.IsZero() || j.Account.IsZero() {
		return fmt.Errorf("policies: refuse upload identity: %w", queue.Permanent(kernel.ErrULID))
	}
	account := kernel.AccountID{ULID: j.Account}
	var input Input
	started, settled := false, false
	err := tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		started, settled = false, false
		if err := s.d.Queue.HoldTx(ctx, j); err != nil {
			return fmt.Errorf("policies: retain input: %w", err)
		}
		var err error
		input, err = s.d.Store.Input(ctx, account, ref.ID, true)
		if errors.Is(err, ErrNotFound) {
			settled = true
			return s.d.Queue.CompleteTx(ctx, j)
		}
		if err != nil {
			return fmt.Errorf("policies: retain input: %w", err)
		}
		if input.State != "pending" && input.State != "uploading" {
			settled = true
			return s.d.Queue.CompleteTx(ctx, j)
		}
		if !s.d.Now().Before(input.ExpiresAt) {
			settled = true
			return s.retire(ctx, j, input, "failed")
		}
		if input.State == "pending" {
			started = true
			return s.d.Store.StartUpload(ctx, account, input.ID)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("policies: begin input upload: %w", err)
	}
	if settled {
		return nil
	}
	if started {
		raw := input.Staging.Reveal()
		_, err = s.d.Objects.Put(ctx, objectstore.PolicyInputs, input.Key, bytes.NewReader(raw), int64(len(raw)))
		if err != nil {
			// The policy result already committed. A failed/ambiguous sample
			// cannot change it; cleanup is fenced and drops the sealed staging.
			return s.settle(ctx, j, account, input.ID, false)
		}
	}
	return s.settle(ctx, j, account, input.ID, true)
}

func (s *Service) settle(ctx context.Context, j queue.Job, account kernel.AccountID, id kernel.ULID, uploaded bool) error {
	err := tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		if err := s.d.Queue.HoldTx(ctx, j); err != nil {
			return fmt.Errorf("policies: retain input: %w", err)
		}
		input, err := s.d.Store.Input(ctx, account, id, true)
		if errors.Is(err, ErrNotFound) {
			return s.d.Queue.CompleteTx(ctx, j)
		}
		if err != nil {
			return fmt.Errorf("policies: retain input: %w", err)
		}
		if input.State != "uploading" {
			return s.d.Queue.CompleteTx(ctx, j)
		}
		if !uploaded || !s.d.Now().Before(input.ExpiresAt) {
			return s.retire(ctx, j, input, "failed")
		}
		complete, err := s.d.Objects.Uploaded(ctx, objectstore.PolicyInputs, input.Key)
		if err != nil {
			return fmt.Errorf("policies: retain input: %w", err)
		}
		if !complete {
			return s.retire(ctx, j, input, "failed")
		}

		var previous *kernel.ULID
		if input.CaptureKind != "" {
			var published bool
			previous, published, err = s.d.Store.PublishCapture(ctx, input)
			if err != nil {
				return fmt.Errorf("policies: publish capture head: %w", err)
			}
			if !published {
				return s.retire(ctx, j, input, "superseded")
			}
		}
		if err := s.d.Objects.CommitUntil(ctx, objectstore.PolicyInputs, input.Key, input.ExpiresAt); err != nil {
			return fmt.Errorf("policies: retain input: %w", err)
		}
		if err := s.d.Store.FinishInput(ctx, account, id, "live", s.d.Now()); err != nil {
			return fmt.Errorf("policies: retain input: %w", err)
		}

		if previous != nil {
			old, err := s.d.Store.Input(ctx, account, *previous, true)
			if err != nil {
				return fmt.Errorf("policies: read replaced capture: %w", err)
			}
			if err := s.retireInput(ctx, old, "superseded"); err != nil {
				return err
			}
		}
		return s.d.Queue.CompleteTx(ctx, j)
	})
	if err != nil {
		return fmt.Errorf("policies: settle input upload: %w", err)
	}
	return nil
}

func (s *Service) retire(ctx context.Context, j queue.Job, input Input, state string) error {
	if err := s.retireInput(ctx, input, state); err != nil {
		return err
	}
	if err := s.d.Queue.CompleteTx(ctx, j); err != nil {
		return fmt.Errorf("policies: settle retired input job: %w", err)
	}
	return nil
}

func (s *Service) retireInput(ctx context.Context, input Input, state string) error {
	if err := s.d.Objects.Delete(ctx, objectstore.PolicyInputs, input.Key); err != nil {
		return fmt.Errorf("policies: retire input object: %w", err)
	}
	if err := s.d.Store.FinishInput(ctx, input.Account, input.ID, state, s.d.Now()); err != nil {
		return fmt.Errorf("policies: retire input metadata: %w", err)
	}
	if err := s.d.Store.ClearCapturePending(ctx, input); err != nil {
		return fmt.Errorf("policies: retire pending capture: %w", err)
	}
	return nil
}
