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
		obj, err := s.d.Objects.LockLiveAt(ctx, objectstore.PolicyInputs, ref.Input.Key, s.d.Now())
		if errors.Is(err, objectstore.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("policies: fence retained input: %w", err)
		}
		if obj.Size < 0 || obj.Size > MaxReadBytes {
			return fmt.Errorf("policies: retained input exceeds decode limit: %w", objectstore.ErrSize)
		}
		reader, err := s.d.Objects.Open(ctx, objectstore.PolicyInputs, ref.Input.Key)
		if err != nil {
			return fmt.Errorf("policies: open retained input: %w", err)
		}
		defer reader.Close()
		raw, err := io.ReadAll(io.LimitReader(reader, MaxReadBytes+1))
		if err != nil {
			return fmt.Errorf("policies: authenticate retained input: %w", err)
		}
		if int64(len(raw)) > MaxReadBytes {
			return fmt.Errorf("policies: retained input exceeds decode limit: %w", objectstore.ErrSize)
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
