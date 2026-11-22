package sampling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// MaxReadBytes bounds a retained document before decoding its JSON tree. HTTP
// ownership and root-admin authorization remain the public reader's obligation.
const MaxReadBytes int64 = 128 << 20

// Read opens only an account-owned published sample whose policy still exists.
// The bounded read holds the blob deletion fence through authentication/decode;
// it allocates no second connection and rejects expiry even before the sweep.
func (s *Service) Read(ctx context.Context, account kernel.AccountID, evaluation kernel.ULID) (Payload, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var out Payload
	err := tx.Do(ctx, s.d.DB, tx.Options{NoRetry: true}, func(ctx context.Context) error {
		refs, err := s.d.Store.References(ctx, account, []kernel.ULID{evaluation})
		if err != nil {
			return fmt.Errorf("policies: locate retained input: %w", err)
		}
		ref, ok := refs[evaluation]
		if !ok || !ref.PolicyExists || ref.Input.PublishedAt == nil || ref.Input.State != "live" || !s.d.Now().Before(ref.Input.ExpiresAt) {
			return ErrNotFound
		}
		raw, err := s.ReadDocument(ctx, ref.Input)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return fmt.Errorf("policies: decode retained input: %w", err)
		}
		return nil
	})
	if err != nil {
		return Payload{}, fmt.Errorf("policies: read retained sample: %w", err)
	}
	return out, nil
}

// ReadDocument authenticates and bounds one already authorized input while its
// caller holds the owner fence. It cannot run outside a non-retrying transaction.
func (s *Service) ReadDocument(ctx context.Context, input Input) ([]byte, error) {
	if !tx.In(ctx) {
		return nil, fmt.Errorf("policies: input read requires owner fence: %w", ErrNotFound)
	}
	if input.PublishedAt == nil || input.State != "live" || !s.d.Now().Before(input.ExpiresAt) {
		return nil, ErrNotFound
	}
	obj, err := s.d.Objects.LockLiveAt(ctx, objectstore.PolicyInputs, input.Key, s.d.Now())
	if errors.Is(err, objectstore.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("policies: fence retained input: %w", err)
	}
	if obj.Size < 0 || obj.Size > MaxReadBytes {
		return nil, fmt.Errorf("policies: retained input exceeds decode limit: %w", objectstore.ErrSize)
	}
	reader, err := s.d.Objects.Open(ctx, objectstore.PolicyInputs, input.Key)
	if errors.Is(err, objectstore.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("policies: open retained input: %w", err)
	}
	defer reader.Close()
	raw, err := io.ReadAll(io.LimitReader(reader, MaxReadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("policies: authenticate retained input: %w", err)
	}
	if int64(len(raw)) > MaxReadBytes {
		return nil, fmt.Errorf("policies: retained input exceeds decode limit: %w", objectstore.ErrSize)
	}
	return raw, nil
}
