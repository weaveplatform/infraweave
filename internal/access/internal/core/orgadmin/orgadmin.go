package orgadmin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/member"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// The organization trail's actions of the tag, and the resource types they
// affect.
const (
	ActionUpdate         = "organization.update"
	ActionAccountCreate  = "account.create"
	ActionAccountClaim   = "account.claim"
	ActionAccountUpdate  = "account.update"
	ActionAccountDelete  = "account.delete"
	ActionLimitSet       = "account.limit_set"
	ActionLimitClear     = "account.limit_clear"
	ActionDeactivate     = "member.deactivate"
	ActionAdminGrant     = "member.organization_admin_grant"
	ActionAdminRevoke    = "member.organization_admin_revoke"
	ActionKeyCreate      = "organization_key.create"
	ActionKeyDisable     = "organization_key.disable"
	ActionKeyDelete      = "organization_key.delete"
	ResourceOrganization = "ORGANIZATION"
	ResourceAccount      = "ACCOUNT"
	ResourceMember       = "MEMBER"
	ResourceKey          = "ORGANIZATION_KEY"
)

var (
	// ErrForbidden refuses a member that does not hold the organization
	// role, which the API answers 403 forbidden even for an account admin,
	// since the organization's operations describe what every account of
	// the organization shares.
	// ref: decision 0007 § "Tenancy"
	ErrForbidden = errors.New("organization-admin required")
	// ErrInvalidState refuses a claim of a claimed account and the deletion
	// of an organization's last account.
	ErrInvalidState = errors.New("invalid state")
	// ErrNoAdmin refuses an account admin that is no active member of the
	// organization; the service answers it as a FieldError naming admin.
	ErrNoAdmin = errors.New("no active member of the organization")
)

// Member is a member of the organization, with whether it holds the
// organization role.
type Member struct {
	ID        kernel.MemberID
	Login     string
	Name      string
	Email     string
	Status    member.Status
	Admin     bool
	CreatedAt time.Time
}

// Limit is a limit of the catalogue as it applies to the organization or
// an account: Value nil for none, InUse nil for a limit nothing counts yet.
type Limit struct {
	Name  string
	Value *int64
	InUse *int64
}

// AccountUsage is an account's use of every limit, with its own limits.
type AccountUsage struct {
	Account account.Account
	Limits  []Limit
}

// Store keeps what the tag reads and writes. Each write records e, its
// organization trail entry, in its own transaction. Everything is scoped to
// org, so a row of another organization is the store's not found error.
type Store interface {
	Organization(ctx context.Context, id kernel.OrganizationID) (organization.Organization, error)
	RenameOrganization(
		ctx context.Context,
		org kernel.OrganizationID,
		name string,
		e kernel.OrganizationAuditEntry,
	) (organization.Organization, error)
	// IsOrganizationAdmin reports whether the member holds the
	// organization role.
	IsOrganizationAdmin(
		ctx context.Context,
		org kernel.OrganizationID,
		member kernel.MemberID,
	) (bool, error)

	// OrganizationAccounts are up to limit of org's accounts in ID order
	// from the first after after.
	OrganizationAccounts(
		ctx context.Context,
		org kernel.OrganizationID,
		after kernel.AccountID,
		limit int,
	) ([]account.Account, error)
	AccountBySlug(
		ctx context.Context,
		org kernel.OrganizationID,
		slug kernel.Slug,
	) (account.Account, error)
	// CreateAccount records a, claimed, admitting admin, which must be an
	// active member of a's organization or ErrNoAdmin.
	CreateAccount(
		ctx context.Context,
		a account.Account,
		admin kernel.MemberID,
		e kernel.OrganizationAuditEntry,
	) error
	// ClaimAccount claims the unclaimed account at, admitting admin, and
	// returns it; a claimed one is ErrInvalidState. The admitted member
	// takes a seat in the account, which the seats limit checks.
	ClaimAccount(
		ctx context.Context,
		org kernel.OrganizationID,
		acct kernel.AccountID,
		admin kernel.MemberID,
		at time.Time,
		e kernel.OrganizationAuditEntry,
	) (account.Account, error)
	RenameAccount(
		ctx context.Context,
		org kernel.OrganizationID,
		acct kernel.AccountID,
		name string,
		e kernel.OrganizationAuditEntry,
	) (account.Account, error)
	// RemoveAccount deletes the account, marked for deletion or not,
	// revoking its live sessions, which it returns; the organization's
	// last account is ErrInvalidState.
	RemoveAccount(
		ctx context.Context,
		org kernel.OrganizationID,
		acct kernel.AccountID,
		e kernel.OrganizationAuditEntry,
	) ([]kernel.ULID, error)
	// Seats counts the seats in use in org and in acct.
	Seats(
		ctx context.Context,
		org kernel.OrganizationID,
		acct kernel.AccountID,
	) (featuresapi.Usage, error)

	// Members are up to limit of org's members in ID order from the first
	// after after.
	Members(
		ctx context.Context,
		org kernel.OrganizationID,
		after kernel.MemberID,
		limit int,
	) ([]Member, error)
	Member(ctx context.Context, org kernel.OrganizationID, id kernel.MemberID) (Member, error)
	// DeactivateMember deactivates the member, disables its personal keys
	// as DEPROVISIONED and revokes its sessions and theirs, which it
	// returns.
	DeactivateMember(
		ctx context.Context,
		org kernel.OrganizationID,
		id kernel.MemberID,
		e kernel.OrganizationAuditEntry,
	) ([]kernel.ULID, error)
	// SetOrganizationAdmin grants the role when admin is true, by the
	// principal by names, and revokes it otherwise; either is no change
	// when the member already stands so.
	SetOrganizationAdmin(
		ctx context.Context,
		org kernel.OrganizationID,
		id kernel.MemberID,
		admin bool,
		by string,
		e kernel.OrganizationAuditEntry,
	) error

	OrganizationKeys(
		ctx context.Context,
		org kernel.OrganizationID,
		after kernel.ULID,
		limit int,
	) ([]apikey.OrganizationKey, error)
	CreateOrganizationKey(
		ctx context.Context,
		k apikey.OrganizationKey,
		e kernel.OrganizationAuditEntry,
	) error
	// DisableOrganizationKey disables the key unless it is, revokes its
	// sessions, which it returns, and returns the key as it is now.
	DisableOrganizationKey(
		ctx context.Context,
		org kernel.OrganizationID,
		id kernel.ULID,
		e kernel.OrganizationAuditEntry,
	) (apikey.OrganizationKey, []kernel.ULID, error)
	// DeleteOrganizationKey deletes the key, revoking its sessions, and
	// returns it as it was and the sessions.
	DeleteOrganizationKey(
		ctx context.Context,
		org kernel.OrganizationID,
		id kernel.ULID,
		e kernel.OrganizationAuditEntry,
	) (apikey.OrganizationKey, []kernel.ULID, error)
	// Record runs write, a write of another domain's rows such as an
	// account limit, and records e, in one transaction.
	Record(
		ctx context.Context,
		e kernel.OrganizationAuditEntry,
		write func(context.Context) error,
	) error
}

// Deps are what the service needs.
type Deps struct {
	Store Store
	// Features holds the entitlement and the account limits; nil applies
	// no limit, as billing off does.
	Features featuresapi.Entitlements
	// Drop drops a revoked session from this replica's cache at once.
	Drop func(kernel.ULID)
	Now  func() time.Time
}

// Service is the Organization tag. The authenticator admits only
// principals that may hold the organization role and organization tokens;
// the service refuses a member that does not hold it.
//
// ref: decision 0021 § "The organization API"
type Service struct{ d Deps }

// NewService returns the service on d.
func NewService(d Deps) *Service { return &Service{d: d} }

// Actor is who calls the service: the principal and the client's address,
// which each write's trail entry records.
type Actor struct {
	Principal kernel.Principal
	RemoteIP  string
}

// admit refuses a member without the organization role. Organization
// tokens and the break-glass admin hold it by what they are; the
// authenticator admits no other kind.
func (s *Service) admit(ctx context.Context, by Actor) error {
	p := by.Principal
	switch p.Kind {
	case kernel.PrincipalOrganization, kernel.PrincipalBreakGlass:
		return nil
	case kernel.PrincipalUser:
		ok, err := s.d.Store.IsOrganizationAdmin(ctx, p.OrganizationID, p.MemberID)
		if err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		if ok {
			return nil
		}
	}
	return ErrForbidden
}

// Organization reads the caller's organization.
func (s *Service) Organization(ctx context.Context, by Actor) (organization.Organization, error) {
	if err := s.admit(ctx, by); err != nil {
		return organization.Organization{}, err
	}
	//nolint:wrapcheck // the store names the read
	return s.d.Store.Organization(ctx, by.Principal.OrganizationID)
}

// Rename renames the caller's organization; a name it refuses is
// organization.ErrInvalid.
func (s *Service) Rename(
	ctx context.Context,
	by Actor,
	name string,
) (organization.Organization, error) {
	if err := s.admit(ctx, by); err != nil {
		return organization.Organization{}, err
	}
	if err := organization.CheckName("name", name); err != nil {
		return organization.Organization{}, err //nolint:wrapcheck // a FieldError, answered as it is
	}
	org := by.Principal.OrganizationID
	o, err := s.d.Store.Organization(ctx, org)
	if err != nil {
		return organization.Organization{}, err //nolint:wrapcheck // as above
	}
	//nolint:wrapcheck // the store names the write
	return s.d.Store.RenameOrganization(ctx, org, name, by.entry(org, ActionUpdate, "UPDATE",
		kernel.AuditResource{Type: ResourceOrganization, ID: string(o.Slug)},
		body(map[string]any{"name": name}), s.d.Now()))
}

// Accounts are up to limit of the organization's accounts from the first
// after after.
func (s *Service) Accounts(
	ctx context.Context,
	by Actor,
	after kernel.AccountID,
	limit int,
) ([]account.Account, error) {
	if err := s.admit(ctx, by); err != nil {
		return nil, err
	}
	//nolint:wrapcheck // the store names the read
	return s.d.Store.OrganizationAccounts(ctx, by.Principal.OrganizationID, after, limit)
}

// CreateAccount creates an account of the organization, claimed by the
// member admin names. Invalid input is organization.ErrInvalid, naming
// each field, and a slug in use is the store's slug taken error.
func (s *Service) CreateAccount(
	ctx context.Context,
	by Actor,
	slug, name, admin string,
) (account.Account, error) {
	if err := s.admit(ctx, by); err != nil {
		return account.Account{}, err
	}
	org := by.Principal.OrganizationID
	a, aerr := account.New(org, "", slug, name)
	m, merr := memberID(admin)
	if err := errors.Join(aerr, merr); err != nil {
		return account.Account{}, err
	}
	now := s.d.Now()
	a.CreatedAt, a.ClaimedAt = now, now
	e := by.entry(org, ActionAccountCreate, "CREATE",
		kernel.AuditResource{Type: ResourceAccount, ID: string(a.Slug)},
		body(map[string]any{"slug": a.Slug, "name": a.Name, "admin": m.String()}), now)
	if err := s.d.Store.CreateAccount(ctx, a, m, e); err != nil {
		return account.Account{}, noAdmin(err)
	}
	return a, nil
}

// Claim claims the unclaimed account slug names for the member admin
// names; a claimed account is ErrInvalidState.
func (s *Service) Claim(
	ctx context.Context,
	by Actor,
	slug, admin string,
) (account.Account, error) {
	a, err := s.account(ctx, by, slug)
	if err != nil {
		return account.Account{}, err
	}
	m, err := memberID(admin)
	if err != nil {
		return account.Account{}, err
	}
	now := s.d.Now()
	e := by.entry(a.OrganizationID, ActionAccountClaim, "UPDATE",
		kernel.AuditResource{Type: ResourceAccount, ID: string(a.Slug)},
		body(map[string]any{"admin": m.String()}), now)
	a, err = s.d.Store.ClaimAccount(ctx, a.OrganizationID, a.ID, m, now, e)
	if err != nil {
		return account.Account{}, noAdmin(err)
	}
	return a, nil
}

// RenameAccount renames the account slug names.
func (s *Service) RenameAccount(
	ctx context.Context,
	by Actor,
	slug, name string,
) (account.Account, error) {
	a, err := s.account(ctx, by, slug)
	if err != nil {
		return account.Account{}, err
	}
	if err := organization.CheckName("name", name); err != nil {
		return account.Account{}, err //nolint:wrapcheck // as above
	}
	//nolint:wrapcheck // the store names the write
	return s.d.Store.RenameAccount(ctx, a.OrganizationID, a.ID, name,
		by.entry(a.OrganizationID, ActionAccountUpdate, "UPDATE",
			kernel.AuditResource{Type: ResourceAccount, ID: string(a.Slug)},
			body(map[string]any{"name": name}), s.d.Now()))
}

// DeleteAccount deletes the account slug names and returns it as it was;
// the organization's last account is ErrInvalidState. Its sessions stop
// at once on this replica and on their invalidation on the others.
func (s *Service) DeleteAccount(
	ctx context.Context,
	by Actor,
	slug string,
) (account.Account, error) {
	a, err := s.account(ctx, by, slug)
	if err != nil {
		return account.Account{}, err
	}
	ids, err := s.d.Store.RemoveAccount(ctx, a.OrganizationID, a.ID,
		by.entry(a.OrganizationID, ActionAccountDelete, "DELETE",
			kernel.AuditResource{Type: ResourceAccount, ID: string(a.Slug)}, nil, s.d.Now()))
	if err != nil {
		return account.Account{}, err //nolint:wrapcheck // as above
	}
	s.drop(ids)
	return a, nil
}

// account reads the account slug names in the caller's organization; a
// slug that cannot be one is not found, as an account of another
// organization is.
func (s *Service) account(ctx context.Context, by Actor, slug string) (account.Account, error) {
	if err := s.admit(ctx, by); err != nil {
		return account.Account{}, err
	}
	sl, err := organization.ParseSlug("account", slug)
	if err != nil {
		return account.Account{}, fmt.Errorf("%w: account %q", port.ErrNotFound, slug)
	}
	//nolint:wrapcheck // the store names the read
	return s.d.Store.AccountBySlug(ctx, by.Principal.OrganizationID, sl)
}

// Members are up to limit of the organization's members from the first
// after after.
func (s *Service) Members(
	ctx context.Context,
	by Actor,
	after kernel.MemberID,
	limit int,
) ([]Member, error) {
	if err := s.admit(ctx, by); err != nil {
		return nil, err
	}
	//nolint:wrapcheck // the store names the read
	return s.d.Store.Members(ctx, by.Principal.OrganizationID, after, limit)
}

// Deactivate deactivates the member id names and returns it as it is now.
// Its sessions stop at once on this replica and on their invalidation on
// the others.
func (s *Service) Deactivate(ctx context.Context, by Actor, id string) (Member, error) {
	m, err := s.member(ctx, by, id)
	if err != nil {
		return Member{}, err
	}
	org := by.Principal.OrganizationID
	ids, err := s.d.Store.DeactivateMember(ctx, org, m.ID,
		by.entry(org, ActionDeactivate, "UPDATE",
			kernel.AuditResource{Type: ResourceMember, ID: m.ID.String()}, nil, s.d.Now()))
	if err != nil {
		return Member{}, err //nolint:wrapcheck // the store names the write
	}
	s.drop(ids)
	m.Status = member.Deactivated
	return m, nil
}

// SetAdmin grants the member id names the organization role, or revokes
// it, and returns the member as it is now.
func (s *Service) SetAdmin(ctx context.Context, by Actor, id string, admin bool) (Member, error) {
	m, err := s.member(ctx, by, id)
	if err != nil {
		return Member{}, err
	}
	org := by.Principal.OrganizationID
	action := ActionAdminRevoke
	if admin {
		action = ActionAdminGrant
	}
	if err := s.d.Store.SetOrganizationAdmin(ctx, org, m.ID, admin, by.name(),
		by.entry(org, action, "UPDATE",
			kernel.AuditResource{Type: ResourceMember, ID: m.ID.String()}, nil, s.d.Now()),
	); err != nil {
		return Member{}, err //nolint:wrapcheck // as above
	}
	m.Admin = admin
	return m, nil
}

// member reads the member id names in the caller's organization; an ID
// that cannot be one is not found.
func (s *Service) member(ctx context.Context, by Actor, id string) (Member, error) {
	if err := s.admit(ctx, by); err != nil {
		return Member{}, err
	}
	u, err := kernel.ParseULID(id)
	if err != nil {
		return Member{}, fmt.Errorf("%w: member %q", port.ErrNotFound, id)
	}
	//nolint:wrapcheck // the store names the read
	return s.d.Store.Member(ctx, by.Principal.OrganizationID, kernel.MemberID{ULID: u})
}

// Keys are up to limit of the organization's keys from the first after
// after.
func (s *Service) Keys(
	ctx context.Context,
	by Actor,
	after kernel.ULID,
	limit int,
) ([]apikey.OrganizationKey, error) {
	if err := s.admit(ctx, by); err != nil {
		return nil, err
	}
	//nolint:wrapcheck // the store names the read
	return s.d.Store.OrganizationKeys(ctx, by.Principal.OrganizationID, after, limit)
}

// CreateKey creates an organization key named name, expiring at expires
// unless it is zero, and returns it with its secret, which nothing stores.
// A name it refuses, or an expiry not in the future, is
// organization.ErrInvalid.
func (s *Service) CreateKey(
	ctx context.Context,
	by Actor,
	name string,
	expires time.Time,
) (apikey.OrganizationKey, string, error) {
	if err := s.admit(ctx, by); err != nil {
		return apikey.OrganizationKey{}, "", err
	}
	now := s.d.Now()
	errs := []error{organization.CheckName("name", name)}
	if !expires.IsZero() && !expires.After(now) {
		errs = append(
			errs,
			&organization.FieldError{Field: "expiresAt", Reason: "is not in the future"},
		)
	}
	if err := errors.Join(errs...); err != nil {
		return apikey.OrganizationKey{}, "", err
	}
	secret := apikey.NewOrganizationSecret()
	h, _ := apikey.HashOrganization(secret) // a new secret always hashes
	org := by.Principal.OrganizationID
	k := apikey.OrganizationKey{
		OrganizationID: org,
		ID:             kernel.NewULID(),
		Name:           name,
		SecretHash:     h,
		ExpiresAt:      expires,
		CreatedAt:      now,
	}
	if by.Principal.Kind == kernel.PrincipalUser {
		k.CreatorMemberID = by.Principal.MemberID
	}
	in := map[string]any{"name": name}
	if !expires.IsZero() {
		in["expiresAt"] = expires.Unix()
	}
	if err := s.d.Store.CreateOrganizationKey(ctx, k, by.entry(org, ActionKeyCreate, "CREATE",
		kernel.AuditResource{Type: ResourceKey, ID: k.ID.String()}, body(in), now)); err != nil {
		return apikey.OrganizationKey{}, "", err //nolint:wrapcheck // the store names the write
	}
	return k, secret, nil
}

// DisableKey disables the key id names and returns it as it is now.
func (s *Service) DisableKey(
	ctx context.Context,
	by Actor,
	id string,
) (apikey.OrganizationKey, error) {
	return s.endKey(ctx, by, id, ActionKeyDisable, s.d.Store.DisableOrganizationKey)
}

// DeleteKey deletes the key id names and returns it as it was.
func (s *Service) DeleteKey(
	ctx context.Context,
	by Actor,
	id string,
) (apikey.OrganizationKey, error) {
	return s.endKey(ctx, by, id, ActionKeyDelete, s.d.Store.DeleteOrganizationKey)
}

// endKey is DisableKey and DeleteKey through write, which revokes the
// key's sessions; they stop at once on this replica.
func (s *Service) endKey(
	ctx context.Context,
	by Actor,
	id, action string,
	write func(
		context.Context,
		kernel.OrganizationID,
		kernel.ULID,
		kernel.OrganizationAuditEntry,
	) (apikey.OrganizationKey, []kernel.ULID, error),
) (apikey.OrganizationKey, error) {
	if err := s.admit(ctx, by); err != nil {
		return apikey.OrganizationKey{}, err
	}
	u, err := kernel.ParseULID(id)
	if err != nil {
		return apikey.OrganizationKey{}, fmt.Errorf("%w: organization key %q", port.ErrNotFound, id)
	}
	org := by.Principal.OrganizationID
	event := "UPDATE"
	if action == ActionKeyDelete {
		event = "DELETE"
	}
	k, ids, err := write(ctx, org, u, by.entry(org, action, event,
		kernel.AuditResource{Type: ResourceKey, ID: u.String()}, nil, s.d.Now()))
	if err != nil {
		return apikey.OrganizationKey{}, err
	}
	s.drop(ids)
	return k, nil
}

// Limits are every limit of the catalogue, in name order, as the
// organization's entitlement applies it now, with the use of those
// something counts: seats, across every account.
func (s *Service) Limits(ctx context.Context, by Actor) ([]Limit, error) {
	if err := s.admit(ctx, by); err != nil {
		return nil, err
	}
	org := by.Principal.OrganizationID
	pool, err := s.pool(ctx, org)
	if err != nil {
		return nil, err
	}
	used, err := s.d.Store.Seats(ctx, org, kernel.AccountID{})
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	return s.limits(pool, used.Organization), nil
}

// Usage is up to limit of the organization's accounts from the first
// after after, each with its use of every limit and its own limits.
func (s *Service) Usage(
	ctx context.Context,
	by Actor,
	after kernel.AccountID,
	limit int,
) ([]AccountUsage, error) {
	accts, err := s.Accounts(ctx, by, after, limit)
	if err != nil {
		return nil, err
	}
	out := make([]AccountUsage, 0, len(accts))
	for _, a := range accts {
		u, err := s.usage(ctx, a)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

// SetAccountLimit sets the limit named limit of the account slug names to
// value and returns the account's usage. A value the features domain
// refuses is a *featuresapi.AccountLimitError.
func (s *Service) SetAccountLimit(
	ctx context.Context,
	by Actor,
	slug, limit string,
	value int64,
) (AccountUsage, error) {
	return s.writeLimit(ctx, by, slug, limit, ActionLimitSet,
		map[string]any{"limit": limit, "value": value},
		func(ctx context.Context, a account.Account) error {
			if s.d.Features == nil {
				return nil
			}
			return s.d.Features.SetAccountLimit(ctx, a.OrganizationID, a.ID, limit, value)
		})
}

// ClearAccountLimit clears the limit named limit of the account slug
// names, and returns the account's usage. A name that is no limit is a
// *featuresapi.AccountLimitError.
func (s *Service) ClearAccountLimit(
	ctx context.Context,
	by Actor,
	slug, limit string,
) (AccountUsage, error) {
	return s.writeLimit(ctx, by, slug, limit, ActionLimitClear,
		map[string]any{"limit": limit},
		func(ctx context.Context, a account.Account) error {
			if s.d.Features == nil {
				return nil
			}
			return s.d.Features.ClearAccountLimit(ctx, a.ID, limit)
		})
}

// writeLimit runs write on the account slug names, records its trail
// entry and answers the account's usage.
func (s *Service) writeLimit(
	ctx context.Context,
	by Actor,
	slug, limit, action string,
	in map[string]any,
	write func(context.Context, account.Account) error,
) (AccountUsage, error) {
	a, err := s.account(ctx, by, slug)
	if err != nil {
		return AccountUsage{}, err
	}
	e := by.entry(a.OrganizationID, action, "UPDATE",
		kernel.AuditResource{Type: ResourceAccount, ID: string(a.Slug)}, body(in), s.d.Now())
	if err := s.d.Store.Record(ctx, e, func(ctx context.Context) error {
		return write(ctx, a)
	}); err != nil {
		return AccountUsage{}, err //nolint:wrapcheck // the write names itself
	}
	return s.usage(ctx, a)
}

// usage is a's use of every limit, with its own limits.
func (s *Service) usage(ctx context.Context, a account.Account) (AccountUsage, error) {
	own := map[string]int64{}
	if s.d.Features != nil {
		var err error
		if own, err = s.d.Features.AccountLimits(ctx, a.ID); err != nil {
			return AccountUsage{}, err //nolint:wrapcheck // the features domain names the read
		}
	}
	used, err := s.d.Store.Seats(ctx, a.OrganizationID, a.ID)
	if err != nil {
		return AccountUsage{}, err //nolint:wrapcheck // the store names the read
	}
	return AccountUsage{Account: a, Limits: s.limits(own, used.Account)}, nil
}

// pool is the organization's limits as they apply now: none while billing
// is off or without the features domain.
func (s *Service) pool(ctx context.Context, org kernel.OrganizationID) (map[string]int64, error) {
	if s.d.Features == nil {
		return map[string]int64{}, nil
	}
	e, err := s.d.Features.Entitlement(ctx, org)
	if errors.Is(err, featuresapi.ErrNotFound) {
		return map[string]int64{}, nil
	}
	if err != nil {
		return nil, err //nolint:wrapcheck // the features domain names the read
	}
	return e.Limits, nil
}

// limits is every limit of the catalogue, in name order, with its value in
// values and, for seats, seats in use. Nothing else is counted yet: each
// counted thing's domain counts it as it arrives.
func (s *Service) limits(values map[string]int64, seats int64) []Limit {
	if s.d.Features == nil {
		return []Limit{}
	}
	names := s.d.Features.LimitNames()
	out := make([]Limit, 0, len(names))
	for _, name := range names {
		l := Limit{Name: name}
		if v, ok := values[name]; ok {
			l.Value = &v
		}
		if name == featuresapi.Seats {
			n := seats
			l.InUse = &n
		}
		out = append(out, l)
	}
	return out
}

func (s *Service) drop(ids []kernel.ULID) {
	for _, id := range ids {
		s.d.Drop(id)
	}
}

// memberID parses an account admin's member ID, refusing one that cannot
// be one as a FieldError naming admin.
func memberID(s string) (kernel.MemberID, error) {
	u, err := kernel.ParseULID(s)
	if err != nil {
		return kernel.MemberID{}, &organization.FieldError{
			Field:  "admin",
			Reason: "is not a member ID",
		}
	}
	return kernel.MemberID{ULID: u}, nil
}

// noAdmin answers ErrNoAdmin as the FieldError naming admin.
func noAdmin(err error) error {
	if errors.Is(err, ErrNoAdmin) {
		return &organization.FieldError{Field: "admin", Reason: ErrNoAdmin.Error()}
	}
	return err
}

// name is how the actor appears as a grant's created_by:
// <kind>:<actor ID>.
func (a Actor) name() string {
	return string(a.Principal.Kind) + ":" + a.Principal.ActorID.String()
}

// entry is the trail entry of a change to r by a.
func (a Actor) entry(
	org kernel.OrganizationID,
	action, eventType string,
	r kernel.AuditResource,
	b json.RawMessage,
	at time.Time,
) kernel.OrganizationAuditEntry {
	return kernel.OrganizationAuditEntry{
		OrganizationID:   org,
		ActorKind:        a.Principal.Kind,
		ActorID:          a.Principal.ActorID.String(),
		Action:           action,
		EventType:        eventType,
		AffectedResource: r,
		Body:             b,
		RemoteIP:         a.RemoteIP,
		CreatedAt:        at,
	}
}

func body(v map[string]any) json.RawMessage {
	b, _ := json.Marshal(v) //nolint:errchkjson // strings and numbers only
	return b
}
