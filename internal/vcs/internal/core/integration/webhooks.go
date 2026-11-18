package integration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// Rotation is the committed integration and its request's private secret
// snapshot. A later rotation cannot change the value this request answers.
type Rotation struct {
	Integration
	secret, credential crypt.Value
}

// Format keeps fmt from reflecting private fields without invoking the
// nested secret values' formatters.
func (r Rotation) Format(f fmt.State, verb rune) { r.secret.Format(f, verb) }

// RotateWebhookSecret replaces the webhook secret under the integration's
// row lock. The prior value and the audit entry commit with the new one.
// The route has decided SPACE_ADMIN on the integration's space.
func (s *Service) RotateWebhookSecret(ctx context.Context, p kernel.Principal, id, provider, remoteIP string) (Rotation, error) {
	var out Rotation
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		in, err := s.locked(ctx, p.AccountID, id, provider)
		if err != nil {
			return err
		}
		out = Rotation{Integration: in}
		// The printable form matches create: 32 bytes of entropy, hex
		// encoded for provider password/token fields and JSON views.
		var entropy [32]byte
		_, _ = rand.Read(entropy[:])
		secret := []byte(hex.EncodeToString(entropy[:]))
		out.secret = crypt.NewValue(secret)
		out.UpdatedAt = s.now()
		out.Version++
		if err := s.store.RotateWebhookSecret(ctx, out.Integration, secret, out.UpdatedAt.Add(s.secretOverlap)); err != nil {
			return err //nolint:wrapcheck // the store names the atomic write
		}
		if out.Settings.AppID != "" && s.apps != nil {
			sec, err := s.store.Secrets(ctx, out.Account, out.ID)
			if err != nil {
				return err //nolint:wrapcheck // the store names the credential read
			}
			out.credential = crypt.NewValue(sec.Credential)
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
	if err != nil {
		return Rotation{}, err //nolint:wrapcheck // each transaction step names its failure
	}
	return out, nil
}

// RotationDetails answers the rotation's captured secret and App details.
// Provider requests run after commit, outside the integration's row lock.
func (s *Service) RotationDetails(ctx context.Context, in Rotation) Details {
	d := Details{WebhookSecret: string(in.secret.Reveal()), WebhookURL: s.WebhookURL(in.Provider, in.ID)}
	if in.Settings.AppID != "" && s.apps != nil {
		app := s.apps.App(ctx, port.Integration{
			ID: in.ID, Provider: in.Provider, Settings: in.Settings,
			Credential: in.credential.Reveal(), WebhookSecret: in.secret.Reveal(),
		})
		d.App = &app
	}
	return d
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
