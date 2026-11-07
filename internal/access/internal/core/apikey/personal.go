package apikey

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ErrPersonalOff reports a personal key created while the account's
// personalApiKeysEnabled is off.
var ErrPersonalOff = errors.New("personal API keys are turned off in this account")

// ErrNoOwner reports a personal key whose creator is not a member the
// account admits: an API key, a run, the break-glass admin, or a member
// deactivated or removed since their session began.
var ErrNoOwner = errors.New("only a member of this account creates a personal API key")

// PersonalInput is a new personal key.
type PersonalInput struct {
	Name        string
	Description string
	ExpiresAt   time.Time
}

// Owner is a personal key's owner as the key's account admits them: the
// principal its tokens act as.
type Owner struct {
	Login string
	Name  string
	// Teams are the identity provider groups the owner's latest sign-in to
	// the account reported, the teams a session of theirs holds.
	Teams []string
}

// CreatePersonal records a personal key of p's account owned by p, and
// returns it with its secret, which is never readable again. Only a
// member's own session creates one: a key minted by a key or a run would
// act as a person who never chose it.
func (s *Service) CreatePersonal(
	ctx context.Context,
	p kernel.Principal,
	in PersonalInput,
) (Key, string, error) {
	if err := creator(p); err != nil {
		return Key{}, "", err
	}
	secret := NewSecret()
	k := Key{
		OrganizationID: p.OrganizationID,
		AccountID:      p.AccountID,
		ID:             kernel.NewULID(),
		Name:           strings.TrimSpace(in.Name),
		Description:    in.Description,
		Type:           TypeSecret,
		Teams:          []string{},
		Owner:          p.MemberID,
		Creator:        p.Login,
		ExpiresAt:      in.ExpiresAt,
		CreatedAt:      s.d.Now(),
	}
	k.SecretHash, _ = Hash(secret) // NewSecret's form, so always ok
	errs := []error{name(k.Name)}
	if !k.ExpiresAt.IsZero() && !k.ExpiresAt.After(k.CreatedAt) {
		errs = append(errs, fmt.Errorf("%w: expiresAt is not in the future", ErrInvalid))
	}
	if err := errors.Join(errs...); err != nil {
		return Key{}, "", err
	}
	k, err := s.d.Store.CreatePersonalAPIKey(ctx, k)
	if err != nil {
		return Key{}, "", err //nolint:wrapcheck // the store names the write
	}
	return k, secret, nil
}

// Held is what the API key domain answers of a principal on the User
// view, from the rules its operations enforce, so the view never claims
// what the operations would refuse.
type Held struct {
	// Created are the keys of the account the principal created.
	Created []kernel.ULID
	// Owned are the principal's personal keys in the account.
	Owned []kernel.ULID
	// CanCreatePersonal is whether CreatePersonal would accept the
	// principal now.
	CanCreatePersonal bool
}

// Held reads what p holds of the API key domain in p's account; a
// principal in no account holds nothing.
func (s *Service) Held(ctx context.Context, p kernel.Principal) (Held, error) {
	var h Held
	if p.AccountID.IsZero() {
		return h, nil
	}
	var err error
	h.Created, h.Owned, err = s.d.Store.KeysOf(ctx, p.AccountID, p.Login, p.MemberID)
	if err != nil {
		return Held{}, err //nolint:wrapcheck // the store names the read
	}
	if creator(p) != nil {
		return h, nil
	}
	switch err := s.d.Store.PersonalCreatable(ctx, p.AccountID, p.MemberID); {
	case err == nil:
		h.CanCreatePersonal = true
	case !errors.Is(err, ErrNoOwner) && !errors.Is(err, ErrPersonalOff):
		return Held{}, err //nolint:wrapcheck // as above
	}
	return h, nil
}

// creator refuses a principal that is not a member's own session.
func creator(p kernel.Principal) error {
	if p.Kind != kernel.PrincipalUser || p.MemberID.IsZero() {
		return ErrNoOwner
	}
	return nil
}

// Personal is up to limit of the personal keys of p's account, from the
// first after after.
func (s *Service) Personal(
	ctx context.Context,
	p kernel.Principal,
	after kernel.ULID,
	limit int,
) ([]Key, error) {
	//nolint:wrapcheck // the store names the read
	return s.d.Store.AccountPersonalAPIKeys(ctx, p.AccountID, after, limit)
}

// PersonalCount counts the personal keys of p's account that still work.
func (s *Service) PersonalCount(ctx context.Context, p kernel.Principal) (int, error) {
	return s.d.Store.CountPersonalAPIKeys(ctx, p.AccountID) //nolint:wrapcheck // as above
}

// DisablePersonal disables the personal key id of p's account and revokes
// every session of it, as Disable does a key of the account's.
func (s *Service) DisablePersonal(
	ctx context.Context,
	p kernel.Principal,
	id kernel.ULID,
) (Key, error) {
	if err := s.manages(ctx, p); err != nil {
		return Key{}, err
	}
	k, _, err := s.revoked("")(
		s.d.Store.DisablePersonalAPIKey(ctx, p.AccountID, id, DisabledByAdmin))
	return k, err
}
