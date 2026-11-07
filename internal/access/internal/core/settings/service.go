package settings

import (
	"context"
	"encoding/json"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// The audit actions this service records.
const (
	// ActionRequestPersonalAPIKeys is a member's request that the account's
	// admins enable personal API keys, on the account's trail.
	ActionRequestPersonalAPIKeys = "account.request_personal_api_keys_enabling"
	// ActionDelete is an account's deletion, on its organization's trail,
	// as the account's own goes with it.
	ActionDelete = "account.delete"
	// ResourceAccount is the audit resource type of an account.
	ResourceAccount = "ACCOUNT"
)

// Store keeps the settings. Every method is scoped to one account; an
// account that does not exist is the store's not found error.
type Store interface {
	// Settings reads the account's settings, Defaults where it changed
	// none.
	Settings(ctx context.Context, acct kernel.AccountID) (Settings, error)
	// SetPersonalAPIKeys records enabled and, when disable is true,
	// disables every personal key of the account and revokes their
	// sessions, which it returns.
	SetPersonalAPIKeys(
		ctx context.Context,
		acct kernel.AccountID,
		enabled, disable bool,
	) ([]kernel.ULID, error)
	// ToggleNonHumanKeyManagers flips the setting and returns its new value.
	ToggleNonHumanKeyManagers(ctx context.Context, acct kernel.AccountID) (bool, error)
	SetSecurityEmail(ctx context.Context, acct kernel.AccountID, email string) error
	SetRedirectHosts(ctx context.Context, acct kernel.AccountID, hosts []string) error
	SetSurvey(ctx context.Context, acct kernel.AccountID, s Survey) error
	// ToggleDeletionMark records m when the account is not marked and
	// clears the mark when it is, and returns whether it is now marked.
	ToggleDeletionMark(ctx context.Context, acct kernel.AccountID, m Mark) (bool, error)
	// RequestPersonalAPIKeys records e on the account's trail.
	RequestPersonalAPIKeys(ctx context.Context, e kernel.AuditEntry) error
	// DeleteAccount deletes the account, with every row the access and
	// features domains hold of it, and records e, in one transaction, once
	// it holds the organization's lock: an account that is not marked or
	// is the organization's last is ErrInvalidState, so two deletions
	// cannot leave an organization with none. It revokes the account's
	// live sessions first and returns them.
	DeleteAccount(
		ctx context.Context,
		acct kernel.AccountID,
		e kernel.OrganizationAuditEntry,
	) ([]kernel.ULID, error)
}

// Deps are what the service needs.
type Deps struct {
	Store Store
	// Drop drops a revoked session from this replica's cache at once; the
	// other replicas drop it on its invalidation.
	Drop func(kernel.ULID)
	Now  func() time.Time
}

// Service reads and sets the settings of the caller's account. Who may
// change them is decided by P4's authorization, as for every other
// operation, but for NonHumanKeyManagers, which only a person may flip.
type Service struct{ d Deps }

// NewService returns the service on d.
func NewService(d Deps) *Service { return &Service{d: d} }

// Get reads the settings of p's account.
func (s *Service) Get(ctx context.Context, p kernel.Principal) (Settings, error) {
	return s.d.Store.Settings(ctx, p.AccountID) //nolint:wrapcheck // the store names the read
}

// SetPersonalAPIKeys allows or refuses personal key creation in p's
// account, disabling the account's personal keys when asked to while
// refusing.
func (s *Service) SetPersonalAPIKeys(
	ctx context.Context,
	p kernel.Principal,
	enabled, disableExisting bool,
) (bool, error) {
	revoked, err := s.d.Store.SetPersonalAPIKeys(ctx, p.AccountID, enabled,
		!enabled && disableExisting)
	if err != nil {
		return false, err //nolint:wrapcheck // the store names the write
	}
	s.drop(revoked)
	return enabled, nil
}

// RequestPersonalAPIKeys records p's request that the account's admins
// enable personal API keys. Nothing is delivered; the trail holds it.
func (s *Service) RequestPersonalAPIKeys(
	ctx context.Context,
	p kernel.Principal,
	remoteIP string,
) error {
	//nolint:wrapcheck // the store names the write
	return s.d.Store.RequestPersonalAPIKeys(ctx, kernel.AuditEntry{
		AccountID:        p.AccountID.ULID,
		Actor:            kernel.AuditActor{Username: p.Login},
		Action:           ActionRequestPersonalAPIKeys,
		EventType:        "NONE",
		AffectedResource: kernel.AuditResource{Type: ResourceAccount, ID: p.AccountID.String()},
		RemoteIP:         remoteIP,
		CreatedAt:        s.d.Now(),
	})
}

// ToggleNonHumanKeyManagers flips whether API keys may manage API keys. A
// key or a run is ErrNotHuman: one that could flip it could grant itself
// what the setting withholds.
func (s *Service) ToggleNonHumanKeyManagers(
	ctx context.Context,
	p kernel.Principal,
) (bool, error) {
	if !p.Human() {
		return false, ErrNotHuman
	}
	return s.d.Store.ToggleNonHumanKeyManagers(ctx, p.AccountID) //nolint:wrapcheck // as above
}

// SetSecurityEmail sets the security contact of p's account.
func (s *Service) SetSecurityEmail(
	ctx context.Context,
	p kernel.Principal,
	email string,
) (string, error) {
	if err := CheckSecurityEmail(email); err != nil {
		return "", err
	}
	if err := s.d.Store.SetSecurityEmail(ctx, p.AccountID, email); err != nil {
		return "", err //nolint:wrapcheck // as above
	}
	return email, nil
}

// SetRedirectHosts replaces the OAuth redirect allowlist of p's account.
func (s *Service) SetRedirectHosts(
	ctx context.Context,
	p kernel.Principal,
	hosts []string,
) ([]string, error) {
	hosts, err := CheckRedirectHosts(hosts)
	if err != nil {
		return nil, err
	}
	if err := s.d.Store.SetRedirectHosts(ctx, p.AccountID, hosts); err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	return hosts, nil
}

// SetSurvey records p's account's survey answer.
func (s *Service) SetSurvey(ctx context.Context, p kernel.Principal, sv Survey) (Survey, error) {
	if err := s.d.Store.SetSurvey(ctx, p.AccountID, sv); err != nil {
		return Survey{}, err //nolint:wrapcheck // as above
	}
	return sv, nil
}

// ToggleDeletionMark marks p's account for deletion, with reason and
// description, or clears its mark, and reports whether it is now marked.
func (s *Service) ToggleDeletionMark(
	ctx context.Context,
	p kernel.Principal,
	reason, description *string,
) (bool, error) {
	//nolint:wrapcheck // as above
	return s.d.Store.ToggleDeletionMark(ctx, p.AccountID, Mark{
		At: s.d.Now(), Reason: reason, Description: description,
	})
}

// Delete deletes p's account, which must be marked and not its
// organization's last, and drops every session it revoked.
func (s *Service) Delete(ctx context.Context, p kernel.Principal, remoteIP string) error {
	body, _ := json.Marshal(
		map[string]string{"account": p.AccountID.String()},
	) //nolint:errchkjson // a string
	revoked, err := s.d.Store.DeleteAccount(ctx, p.AccountID, kernel.OrganizationAuditEntry{
		OrganizationID:   p.OrganizationID,
		ActorKind:        p.Kind,
		ActorID:          p.ActorID.String(),
		Action:           ActionDelete,
		EventType:        "DELETE",
		AffectedResource: kernel.AuditResource{Type: ResourceAccount, ID: p.AccountID.String()},
		Body:             body,
		RemoteIP:         remoteIP,
		CreatedAt:        s.d.Now(),
	})
	if err != nil {
		return err //nolint:wrapcheck // as above
	}
	s.drop(revoked)
	return nil
}

func (s *Service) drop(ids []kernel.ULID) {
	for _, id := range ids {
		s.d.Drop(id)
	}
}
