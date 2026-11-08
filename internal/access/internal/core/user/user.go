package user

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/member"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// The audit actions this service records on the account's trail.
const (
	ActionInvite            = "managed_user.invite"
	ActionUpdate            = "managed_user.update"
	ActionDelete            = "managed_user.delete"
	ActionResendInvite      = "managed_user.resend_invite"
	ActionRequestInvite     = "managed_user.request_invite"
	ActionRequestRoleChange = "managed_user.request_role_change"
	// ResourceManagedUser and ResourceSpace are the audit resource types
	// of a managed user and a space.
	ResourceManagedUser = "MANAGED_USER"
	ResourceSpace       = "SPACE"
	// InvitationTTL is how long an invitation may be accepted after it was
	// sent: Spacelift's 24 hours (report 14, C125).
	InvitationTTL = 24 * time.Hour
)

// AccessLevels are the space access levels a member may ask for.
var AccessLevels = []string{"NONE", "READ", "WRITE", "ADMIN"}

var (
	// ErrInvalidState refuses an invite of a member the account admits or
	// the organization deactivated, an invite for an e-mail address with a
	// pending invitation in the account, and a resend to a managed user
	// with no pending invitation.
	ErrInvalidState = errors.New("invalid managed user state")
	// ErrBindingsUnsupported refuses role bindings and space access rules,
	// which nothing stores until P4b's bindings, so none is dropped
	// silently.
	ErrBindingsUnsupported = errors.New("role bindings are not served yet")
	// ErrNotMember refuses a role change request of a caller that is no
	// member, as only a person holds a role.
	ErrNotMember = errors.New("only a member may ask for a role change")
)

// Invitation is the latest invitation of a member to an account. The
// person accepts it by signing in through its link, which names its ID,
// never by an e-mail match (decision 0006).
type Invitation struct {
	ID        kernel.ULID
	Email     string
	CreatedAt time.Time
	SentAt    time.Time
}

// Expired is whether InvitationTTL has passed since the invitation was
// last sent.
func (i Invitation) Expired(now time.Time) bool {
	return !now.Before(i.SentAt.Add(InvitationTTL))
}

// State is a managed user's standing in the account.
type State string

const (
	StatePending  State = "PENDING"
	StateExpired  State = "EXPIRED"
	StateActive   State = "ACTIVE"
	StateInactive State = "INACTIVE"
)

// User is a managed user: a member of the organization as one account
// sees it.
type User struct {
	AccountID kernel.AccountID
	Member    member.Member
	// SlackMemberID is the Slack user that acts as the managed user; empty
	// for none.
	SlackMemberID string
	// LatestGroups are the groups the identity provider reported at the
	// member's latest sign-in to the account, and GroupCount how many of
	// the account's groups they match.
	LatestGroups []string
	GroupCount   int
	// Invitation is the latest invitation to the account; nil for a
	// member admitted without one.
	Invitation *Invitation
}

// State derives the user's state at now: EXPIRED is a pending member's
// invitation past InvitationTTL, which Spacelift's UserStatus has no value
// for and its UI derives from the invitation's sentAt.
func (u User) State(now time.Time) State {
	switch u.Member.Status {
	case member.Active:
		return StateActive
	case member.Deactivated:
		return StateInactive
	}
	if u.Invitation != nil && u.Invitation.Expired(now) {
		return StateExpired
	}
	return StatePending
}

// Invite is an invitation's input.
type Invite struct {
	Login         string
	Email         string
	SlackMemberID string
}

// Store keeps the account's managed users. A member the account does not
// admit is port.ErrNotFound.
type Store interface {
	// Users are up to limit of acct's managed users in member ID order
	// from the first after after.
	Users(
		ctx context.Context,
		acct kernel.AccountID,
		after kernel.MemberID,
		limit int,
	) ([]User, error)
	User(ctx context.Context, acct kernel.AccountID, id kernel.MemberID) (User, error)
	// Invite admits the organization's member with in.Login to acct,
	// creating a pending member when there is none, records inv and e,
	// and takes the member a seat unless another account admits it, in
	// one transaction; ErrInvalidState as the error says.
	Invite(
		ctx context.Context,
		org kernel.OrganizationID,
		acct kernel.AccountID,
		in Invite,
		inv Invitation,
		e kernel.AuditEntry,
	) (User, error)
	// SetSlackMemberID sets the managed user's Slack member and records e.
	SetSlackMemberID(
		ctx context.Context,
		acct kernel.AccountID,
		id kernel.MemberID,
		slack string,
		e kernel.AuditEntry,
	) (User, error)
	// Resend moves the managed user's latest invitation's sentAt to at and
	// records e; a user with no pending invitation is ErrInvalidState.
	Resend(
		ctx context.Context,
		acct kernel.AccountID,
		id kernel.MemberID,
		at time.Time,
		e kernel.AuditEntry,
	) (User, error)
	// Remove stops acct admitting the member: it deletes the admission and
	// the member's invitations to acct, disables their personal keys in
	// acct as DEPROVISIONED and revokes their sessions in acct and those
	// keys', which it returns, and records e, in one transaction.
	Remove(
		ctx context.Context,
		acct kernel.AccountID,
		id kernel.MemberID,
		e kernel.AuditEntry,
	) ([]kernel.ULID, error)
	// PersonalAPIKeys are up to limit of the member's personal keys in
	// acct, deleted ones left out, in ID order from the first after after.
	PersonalAPIKeys(
		ctx context.Context,
		acct kernel.AccountID,
		owner kernel.MemberID,
		after kernel.ULID,
		limit int,
	) ([]apikey.Key, error)
	// RecordRequest records e, a request of a managed user or a person, on
	// the account's trail.
	RecordRequest(ctx context.Context, e kernel.AuditEntry) error
}

// Deps are what the service needs.
type Deps struct {
	Store Store
	// Drop drops a revoked session from this replica's cache at once; the
	// other replicas drop it on its invalidation.
	Drop func(kernel.ULID)
	Now  func() time.Time
}

// Service manages the managed users of the caller's account. Who may is
// decided by P4b's authorization, as for the account's API keys.
type Service struct{ d Deps }

// NewService returns the service on d.
func NewService(d Deps) *Service { return &Service{d: d} }

// Users are up to limit of p's account's managed users from the first
// after after.
func (s *Service) Users(
	ctx context.Context,
	p kernel.Principal,
	after kernel.MemberID,
	limit int,
) ([]User, error) {
	//nolint:wrapcheck // the store names the read
	return s.d.Store.Users(ctx, p.AccountID, after, limit)
}

// User reads the managed user id names; an ID that cannot be a member's is
// not found, as a member of another account is.
func (s *Service) User(ctx context.Context, p kernel.Principal, id string) (User, error) {
	m, err := parse(id)
	if err != nil {
		return User{}, err
	}
	return s.d.Store.User(ctx, p.AccountID, m) //nolint:wrapcheck // as above
}

// Invite invites the person in names to p's account. bindings is how many
// role bindings the input carries.
func (s *Service) Invite(
	ctx context.Context,
	p kernel.Principal,
	remoteIP string,
	in Invite,
	bindings int,
) (User, error) {
	if bindings > 0 {
		return User{}, fmt.Errorf("%w: roleBindings", ErrBindingsUnsupported)
	}
	if err := errors.Join(
		organization.CheckName("username", in.Login),
		member.CheckEmail("invitationEmail", in.Email),
	); err != nil {
		return User{}, err //nolint:wrapcheck // FieldErrors, answered as they are
	}
	now := s.d.Now()
	inv := Invitation{ID: kernel.NewULID(), Email: in.Email, CreatedAt: now, SentAt: now}
	e := s.entry(p, remoteIP, ActionInvite, "CREATE",
		kernel.AuditResource{Type: ResourceManagedUser, ID: in.Login},
		map[string]any{"username": in.Login, "invitationEmail": in.Email})
	//nolint:wrapcheck // the store names the write and the features domain the refusal
	return s.d.Store.Invite(ctx, p.OrganizationID, p.AccountID, in, inv, e)
}

// Update sets the managed user's Slack member. rules is how many space
// access rules the input carries.
func (s *Service) Update(
	ctx context.Context,
	p kernel.Principal,
	remoteIP, id string,
	rules int,
	slack string,
) (User, error) {
	if rules > 0 {
		return User{}, fmt.Errorf("%w: accessRules", ErrBindingsUnsupported)
	}
	m, err := parse(id)
	if err != nil {
		return User{}, err
	}
	e := s.entry(p, remoteIP, ActionUpdate, "UPDATE",
		kernel.AuditResource{Type: ResourceManagedUser, ID: m.String()},
		map[string]any{"slackMemberID": slack})
	return s.d.Store.SetSlackMemberID(ctx, p.AccountID, m, slack, e) //nolint:wrapcheck // as above
}

// Delete removes the managed user from p's account and answers them as
// they were.
func (s *Service) Delete(
	ctx context.Context,
	p kernel.Principal,
	remoteIP, id string,
) (User, error) {
	u, err := s.User(ctx, p, id)
	if err != nil {
		return User{}, err
	}
	ids, err := s.d.Store.Remove(ctx, p.AccountID, u.Member.ID,
		s.entry(p, remoteIP, ActionDelete, "DELETE",
			kernel.AuditResource{Type: ResourceManagedUser, ID: u.Member.ID.String()}, nil))
	if err != nil {
		return User{}, err //nolint:wrapcheck // as above
	}
	for _, id := range ids {
		s.d.Drop(id)
	}
	return u, nil
}

// Resend moves the managed user's invitation's sentAt to now, restarting
// its InvitationTTL. Nothing is delivered.
func (s *Service) Resend(
	ctx context.Context,
	p kernel.Principal,
	remoteIP, id string,
) (User, error) {
	m, err := parse(id)
	if err != nil {
		return User{}, err
	}
	e := s.entry(p, remoteIP, ActionResendInvite, "UPDATE",
		kernel.AuditResource{Type: ResourceManagedUser, ID: m.String()}, nil)
	return s.d.Store.Resend(ctx, p.AccountID, m, s.d.Now(), e) //nolint:wrapcheck // as above
}

// RequestInvite records a request that the account's admins invite the
// person email and name name. Nothing is delivered; the trail holds it.
func (s *Service) RequestInvite(
	ctx context.Context,
	p kernel.Principal,
	remoteIP, email, name string,
) error {
	emailErr := member.CheckEmail("email", email)
	if email == "" {
		emailErr = &organization.FieldError{Field: "email", Reason: "is empty"}
	}
	if err := errors.Join(emailErr, organization.CheckName("userName", name)); err != nil {
		return err //nolint:wrapcheck // FieldErrors, answered as they are
	}
	//nolint:wrapcheck // the store names the write
	return s.d.Store.RecordRequest(ctx, s.entry(p, remoteIP, ActionRequestInvite, "CREATE",
		kernel.AuditResource{Type: ResourceManagedUser, ID: name},
		map[string]any{"email": email, "userName": name}))
}

// RequestRoleChange records p's request for level on space, the slug of
// a space of p's account that p can read, which the route table checks
// before the request reaches the service. Nothing is delivered; the
// trail holds it.
func (s *Service) RequestRoleChange(
	ctx context.Context,
	p kernel.Principal,
	remoteIP, space, level string,
) error {
	if p.Kind != kernel.PrincipalUser {
		return ErrNotMember
	}
	if !slices.Contains(AccessLevels, level) {
		return &organization.FieldError{
			Field:  "requestedAccessLevel",
			Reason: fmt.Sprintf("%q is none of %v", level, AccessLevels),
		}
	}
	//nolint:wrapcheck // the store names the write
	return s.d.Store.RecordRequest(ctx, s.entry(p, remoteIP, ActionRequestRoleChange, "CREATE",
		kernel.AuditResource{Type: ResourceSpace, ID: space},
		map[string]any{"requestedAccessLevel": level}))
}

// PersonalAPIKeys are up to limit of the managed user's personal keys in
// p's account from the first after after.
func (s *Service) PersonalAPIKeys(
	ctx context.Context,
	p kernel.Principal,
	id string,
	after kernel.ULID,
	limit int,
) ([]apikey.Key, error) {
	u, err := s.User(ctx, p, id)
	if err != nil {
		return nil, err
	}
	//nolint:wrapcheck // the store names the read
	return s.d.Store.PersonalAPIKeys(ctx, p.AccountID, u.Member.ID, after, limit)
}

// entry is an audit entry of p's account at the service's clock.
func (s *Service) entry(
	p kernel.Principal,
	remoteIP, action, eventType string,
	res kernel.AuditResource,
	body map[string]any,
) kernel.AuditEntry {
	e := kernel.AuditEntry{
		AccountID:        p.AccountID.ULID,
		Actor:            kernel.AuditActor{Username: p.Login},
		Action:           action,
		EventType:        eventType,
		AffectedResource: res,
		RemoteIP:         remoteIP,
		CreatedAt:        s.d.Now(),
	}
	if body != nil {
		// A map of strings always marshals.
		e.Body, _ = json.Marshal(body)
	}
	return e
}

// parse reads a managed user's ID, a member's; one that cannot be is not
// found.
func parse(id string) (kernel.MemberID, error) {
	m, err := kernel.ParseMemberID(id)
	if err != nil {
		return kernel.MemberID{}, fmt.Errorf("%w: managed user %q", port.ErrNotFound, id)
	}
	return m, nil
}
