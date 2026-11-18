package pg

import (
	"context"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/audit"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
)

// RotateWebhookSecret joins the service's row-lock transaction. The old
// ciphertext must be opened and resealed: its column is part of the AAD.
func (s *Store) RotateWebhookSecret(ctx context.Context, in integration.Integration, current []byte, until time.Time) error {
	q := tx.Q(ctx, s.db)
	var previous []byte
	if err := q.QueryRow(ctx, `SELECT webhook_secret_sealed FROM vcs_integrations
		WHERE account_id=$1 AND id=$2`, in.Account, in.ID).Scan(&previous); err != nil {
		return fmt.Errorf("vcs: read current webhook secret: %w", sql.Unavailable(err))
	}
	if len(previous) > 0 {
		plain, err := s.seal.OpenColumn(ctx, webhookSecret, key(in.Account, in.ID), previous)
		if err != nil {
			return fmt.Errorf("vcs: open current webhook secret: %w", err)
		}
		previous, err = s.seal.SealColumn(ctx, previousSecret, key(in.Account, in.ID), plain.Reveal())
		if err != nil {
			return fmt.Errorf("vcs: seal previous webhook secret: %w", err)
		}
	}
	sealed, err := s.seal.SealColumn(ctx, webhookSecret, key(in.Account, in.ID), current)
	if err != nil {
		return fmt.Errorf("vcs: seal current webhook secret: %w", err)
	}
	_, err = q.Exec(ctx, `UPDATE vcs_integrations SET webhook_secret_sealed=$3,
		previous_webhook_secret_sealed=$4,previous_secret_valid_until=$5,updated_at=$6,version=$7
		WHERE account_id=$1 AND id=$2`, in.Account, in.ID, sealed, previous, until, in.UpdatedAt, in.Version)
	if err != nil {
		return fmt.Errorf("vcs: rotate webhook secret: %w", sql.Unavailable(err))
	}
	return nil
}

// Audit records the mutation in the same transaction as the secret rotation.
func (s *Store) Audit(ctx context.Context, entry kernel.AuditEntry) error {
	_, err := audit.Record(ctx, entry)
	return err //nolint:wrapcheck // platform/audit names the write
}
