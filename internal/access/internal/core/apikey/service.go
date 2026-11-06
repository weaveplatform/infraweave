package apikey

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ErrInvalid reports input a key cannot be created or changed with; the
// error names the field.
var ErrInvalid = errors.New("invalid API key")

// DisabledByAdmin is the disabled_reason of a key an account admin
// disabled.
const DisabledByAdmin = "ADMIN"

// Store keeps an account's API keys. Every method is scoped to the
// account: a key of another account, or a deleted one, is the store's not
// found error, which the service passes through. The methods that revoke a
// key's sessions do so in the key's transaction, sending each session's
// invalidation, and return the sessions they revoked.
type Store interface {
	CreateAPIKey(ctx context.Context, k Key) error
	// AccountAPIKeys are up to limit of the account's keys, in ID order,
	// from the first after after; a zero after starts at the first.
	AccountAPIKeys(
		ctx context.Context,
		acct kernel.AccountID,
		after kernel.ULID,
		limit int,
	) ([]Key, error)
	AccountAPIKey(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (Key, error)
	UpdateAPIKey(
		ctx context.Context,
		acct kernel.AccountID,
		id kernel.ULID,
		c Change,
	) (Key, error)
	// ResetAPIKey replaces a secret key's hash.
	ResetAPIKey(
		ctx context.Context,
		acct kernel.AccountID,
		id kernel.ULID,
		hash []byte,
	) (Key, []kernel.ULID, error)
	DisableAPIKey(
		ctx context.Context,
		acct kernel.AccountID,
		id kernel.ULID,
		reason string,
	) (Key, []kernel.ULID, error)
	DeleteAPIKey(
		ctx context.Context,
		acct kernel.AccountID,
		id kernel.ULID,
	) (Key, []kernel.ULID, error)
	// SetNonAdminManagers records the account's
	// apiKeyEnableNonAdminManagers setting.
	SetNonAdminManagers(ctx context.Context, acct kernel.AccountID, enable bool) error
}

// Deps are what the service needs.
type Deps struct {
	Store Store
	// Drop drops a revoked session from this replica's cache at once; the
	// other replicas drop it on its invalidation.
	Drop func(kernel.ULID)
	// AllowedIssuers is auth.oidc_api_keys.allowed_issuers; empty allows
	// every HTTPS issuer.
	AllowedIssuers []string
	Now            func() time.Time
}

// Service manages the API keys of the caller's account. A key belongs to
// one account, and every operation reaches only the caller's.
type Service struct{ d Deps }

// NewService returns the service on d.
func NewService(d Deps) *Service { return &Service{d: d} }

// Input is a new key. OIDC is nil for a secret key.
type Input struct {
	Name        string
	Description string
	Teams       []string
	Machine     bool
	ExpiresAt   time.Time
	OIDC        *OIDC
}

// Change is an update: each non-nil field replaces the key's.
type Change struct {
	Name         *string
	Description  *string
	Teams        *[]string
	ClaimMapping *[]Mapping
}

// Create records a key of p's account, created by p, and returns it with
// its secret, which is never readable again; an OIDC key has none.
func (s *Service) Create(ctx context.Context, p kernel.Principal, in Input) (Key, string, error) {
	k := Key{
		OrganizationID: p.OrganizationID,
		AccountID:      p.AccountID,
		ID:             kernel.NewULID(),
		Name:           strings.TrimSpace(in.Name),
		Description:    in.Description,
		Type:           TypeSecret,
		Teams:          nonNil(in.Teams),
		Machine:        in.Machine,
		Creator:        p.Login,
		ExpiresAt:      in.ExpiresAt,
		CreatedAt:      s.d.Now(),
	}
	errs := []error{name(k.Name), teams(k.Teams)}
	if !k.ExpiresAt.IsZero() && !k.ExpiresAt.After(k.CreatedAt) {
		errs = append(errs, fmt.Errorf("%w: expiresAt is not in the future", ErrInvalid))
	}
	var secret string
	if in.OIDC != nil {
		k.Type, k.OIDC = TypeOIDC, *in.OIDC
		k.OIDC.ClaimMapping = nonNil(k.OIDC.ClaimMapping)
		errs = append(errs, s.oidc(k.OIDC))
	} else {
		secret = NewSecret()
		k.SecretHash, _ = Hash(secret) // NewSecret's form, so always ok
	}
	if err := errors.Join(errs...); err != nil {
		return Key{}, "", err
	}
	if err := s.d.Store.CreateAPIKey(ctx, k); err != nil {
		return Key{}, "", err //nolint:wrapcheck // the store names the write
	}
	return k, secret, nil
}

// List is up to limit of the keys of p's account, from the first after
// after.
func (s *Service) List(
	ctx context.Context,
	p kernel.Principal,
	after kernel.ULID,
	limit int,
) ([]Key, error) {
	return s.d.Store.AccountAPIKeys(ctx, p.AccountID, after, limit) //nolint:wrapcheck // as above
}

// Get reads the key id of p's account.
func (s *Service) Get(ctx context.Context, p kernel.Principal, id kernel.ULID) (Key, error) {
	return s.d.Store.AccountAPIKey(ctx, p.AccountID, id) //nolint:wrapcheck // as above
}

// Update applies c to the key id of p's account. A claim mapping applies
// to an OIDC key only.
func (s *Service) Update(
	ctx context.Context,
	p kernel.Principal,
	id kernel.ULID,
	c Change,
) (Key, error) {
	var errs []error
	if c.Name != nil {
		n := strings.TrimSpace(*c.Name)
		c.Name = &n
		errs = append(errs, name(n))
	}
	if c.Teams != nil {
		t := nonNil(*c.Teams)
		c.Teams = &t
		errs = append(errs, teams(t))
	}
	if c.ClaimMapping != nil {
		k, err := s.Get(ctx, p, id)
		if err != nil {
			return Key{}, err
		}
		if k.Type != TypeOIDC {
			errs = append(
				errs,
				fmt.Errorf("%w: oidcClaimMappings applies to an OIDC key only", ErrInvalid),
			)
		}
		m := nonNil(*c.ClaimMapping)
		c.ClaimMapping = &m
		errs = append(errs, mappings(m))
	}
	if err := errors.Join(errs...); err != nil {
		return Key{}, err
	}
	return s.d.Store.UpdateAPIKey(ctx, p.AccountID, id, c) //nolint:wrapcheck // as above
}

// Reset replaces the secret of the key id of p's account and revokes
// every session of the key, so the old secret and every token it was
// exchanged for stop working. It returns the key and its new secret. An
// OIDC key has no secret to reset.
func (s *Service) Reset(
	ctx context.Context,
	p kernel.Principal,
	id kernel.ULID,
) (Key, string, error) {
	k, err := s.Get(ctx, p, id)
	if err != nil {
		return Key{}, "", err
	}
	if k.Type != TypeSecret {
		return Key{}, "", fmt.Errorf("%w: an OIDC key has no secret to reset", ErrInvalid)
	}
	secret := NewSecret()
	hash, _ := Hash(secret) // NewSecret's form, so always ok
	return s.revoked(secret)(s.d.Store.ResetAPIKey(ctx, p.AccountID, id, hash))
}

// Disable disables the key id of p's account and revokes every session of
// the key. Disabling a disabled key keeps its first reason.
func (s *Service) Disable(ctx context.Context, p kernel.Principal, id kernel.ULID) (Key, error) {
	k, _, err := s.revoked("")(s.d.Store.DisableAPIKey(ctx, p.AccountID, id, DisabledByAdmin))
	return k, err
}

// Delete deletes the key id of p's account and revokes every session of
// the key. The row stays, deleted, while sessions and audit name it.
func (s *Service) Delete(ctx context.Context, p kernel.Principal, id kernel.ULID) (Key, error) {
	k, _, err := s.revoked("")(s.d.Store.DeleteAPIKey(ctx, p.AccountID, id))
	return k, err
}

// SetNonAdminManagers records whether admins of spaces below p's
// account's top space may manage API keys, and returns the setting.
func (s *Service) SetNonAdminManagers(
	ctx context.Context,
	p kernel.Principal,
	enable bool,
) (bool, error) {
	if err := s.d.Store.SetNonAdminManagers(ctx, p.AccountID, enable); err != nil {
		return false, err //nolint:wrapcheck // as above
	}
	return enable, nil
}

// IssuerAllowed reports whether a key may name issuer: an absolute HTTPS
// URL with a host and no query, fragment or credentials, among allowed
// when any are configured (DV-014).
func IssuerAllowed(allowed []string, issuer string) bool {
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" ||
		u.Fragment != "" {
		return false
	}
	return len(allowed) == 0 || slices.Contains(allowed, issuer)
}

// revoked drops the sessions a revoking write returns from this
// replica's cache and passes its key and error through with secret.
func (s *Service) revoked(secret string) func(Key, []kernel.ULID, error) (Key, string, error) {
	return func(k Key, ids []kernel.ULID, err error) (Key, string, error) {
		if err != nil {
			return Key{}, "", err
		}
		for _, id := range ids {
			s.d.Drop(id)
		}
		return k, secret, nil
	}
}

// oidc checks an OIDC key's settings.
func (s *Service) oidc(o OIDC) error {
	var errs []error
	if !IssuerAllowed(s.d.AllowedIssuers, o.Issuer) {
		errs = append(errs, fmt.Errorf(
			"%w: oidc.issuer %q is not an HTTPS URL this installation allows",
			ErrInvalid, o.Issuer))
	}
	if strings.TrimSpace(o.ClientID) == "" {
		errs = append(errs, fmt.Errorf("%w: oidc.clientId is required", ErrInvalid))
	}
	if strings.TrimSpace(o.SubjectExpression) == "" {
		errs = append(errs, fmt.Errorf("%w: oidc.subjectExpression is required", ErrInvalid))
	} else if _, err := CompileSubject(o.SubjectExpression); err != nil {
		errs = append(errs, fmt.Errorf("%w: oidc.subjectExpression: %w", ErrInvalid, err))
	}
	return errors.Join(append(errs, mappings(o.ClaimMapping))...)
}

func name(n string) error {
	if n == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}
	return nil
}

func teams(t []string) error {
	if slices.Contains(t, "") {
		return fmt.Errorf("%w: teams has an empty team", ErrInvalid)
	}
	return nil
}

func mappings(m []Mapping) error {
	for i, e := range m {
		if e.Name == "" || e.Value == "" {
			return fmt.Errorf("%w: claim mapping entry %d needs a name and a value", ErrInvalid, i)
		}
	}
	return nil
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
