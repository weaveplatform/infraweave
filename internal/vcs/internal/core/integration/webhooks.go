package integration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// RotateWebhookSecret replaces the webhook secret under the integration's
// row lock. The prior value and the audit entry commit with the new one.
// The route has decided SPACE_ADMIN on the integration's space.
func (s *Service) RotateWebhookSecret(ctx context.Context, p kernel.Principal, id, provider, remoteIP string) (Integration, error) {
	var out Integration
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		var err error
		if out, err = s.locked(ctx, p.AccountID, id, provider); err != nil {
			return err
		}
		// The printable form matches create: 32 bytes of entropy, hex
		// encoded for provider password/token fields and JSON views.
		var entropy [32]byte
		_, _ = rand.Read(entropy[:])
		secret := []byte(hex.EncodeToString(entropy[:]))
		out.UpdatedAt = s.now()
		out.Version++
		if err := s.store.RotateWebhookSecret(ctx, out, secret, out.UpdatedAt.Add(s.secretOverlap)); err != nil {
			return err //nolint:wrapcheck // the store names the atomic write
		}
		actor := kernel.AuditActor{Username: p.Login}
		if actor.Username == "" {
			actor.Username = p.ActorID.String()
		}
		if p.Kind == kernel.PrincipalRun {
			actor = kernel.AuditActor{Username: "infraweave", Run: p.ActorID}
		}
		return s.store.Audit(ctx, kernel.AuditEntry{ //nolint:wrapcheck // the audit boundary names the write
			AccountID: p.AccountID.ULID, Actor: actor,
			Action: "vcs_integration.rotate_webhook_secret", EventType: "UPDATE",
			AffectedResource: kernel.AuditResource{Type: "VCS_INTEGRATION", ID: out.ID.String()},
			RemoteIP:         remoteIP, CreatedAt: out.UpdatedAt,
		})
	})
	return out, err //nolint:wrapcheck // each transaction step names its failure
}

// WebhookEndpoint is the provider's account-default integration URL, or
// the named GitHub App integration's URL. The route has decided its admin
// action in the top space or the named integration's own space.
func (s *Service) WebhookEndpoint(ctx context.Context, p kernel.Principal, provider, id string) (string, error) {
	var in Integration
	var err error
	if id == "" {
		in, err = s.store.Default(ctx, p.AccountID, provider)
	} else {
		in, err = s.get(ctx, p.AccountID, id, provider)
	}
	if errors.Is(err, ErrNotFound) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err //nolint:wrapcheck // the store names the lookup
	}
	return s.WebhookURL(in.Provider, in.ID), nil
}
