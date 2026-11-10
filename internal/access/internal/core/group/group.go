package group

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/user"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// The audit actions this service records on the account's trail.
const (
	ActionCreate = "managed_user_group.create"
	ActionUpdate = "managed_user_group.update"
	ActionDelete = "managed_user_group.delete"
	// ResourceGroup is the audit resource type of a managed user group.
	ResourceGroup = "MANAGED_USER_GROUP"
)

// Group is a managed user group of one account: the identity provider
// group Name names, as the account sees it. Its members are the account's
// managed users whose latest sign-in to it reported Name, so a group
// reaches no other account.
type Group struct {
	AccountID   kernel.AccountID
	ID          kernel.ULID
	Name        string
	Description string
	CreatedAt   time.Time
	// Members are the logins of the group's members, in login order.
	Members []string
	// Version is raised by every change of the group; a conditional
	// change names the version it expects.
	Version int64
}

// Store keeps the account's groups. A group of another account is
// port.ErrNotFound. A method given an expect locks the group and changes
// nothing, answering kernel.ErrStale, when expect does not accept its
// version; each change raises it.
type Store interface {
	// Groups are up to limit of acct's groups in ID order from the first
	// after after.
	Groups(
		ctx context.Context,
		acct kernel.AccountID,
		after kernel.ULID,
		limit int,
	) ([]Group, error)
	Group(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (Group, error)
	// CreateGroup creates g and records e; a name another group of the
	// account holds is port.ErrSlugTaken.
	CreateGroup(ctx context.Context, g Group, e kernel.AuditEntry) (Group, error)
	SetDescription(
		ctx context.Context,
		acct kernel.AccountID,
		id kernel.ULID,
		expect kernel.Expect,
		description string,
		e kernel.AuditEntry,
	) (Group, error)
	DeleteGroup(
		ctx context.Context,
		acct kernel.AccountID,
		id kernel.ULID,
		expect kernel.Expect,
		e kernel.AuditEntry,
	) error
	// GroupUsers are up to limit of the group's members in member ID order
	// from the first after after.
	GroupUsers(
		ctx context.Context,
		acct kernel.AccountID,
		id kernel.ULID,
		after kernel.MemberID,
		limit int,
	) ([]user.User, error)
	// AvailableGroups are up to limit of the group names the account's
	// managed users' latest sign-ins reported, in name order from the
	// first after after.
	AvailableGroups(
		ctx context.Context,
		acct kernel.AccountID,
		after string,
		limit int,
	) ([]string, error)
}

// Deps are what the service needs.
type Deps struct {
	Store Store
	Now   func() time.Time
}

// Service manages the managed user groups of the caller's account.
type Service struct{ d Deps }

// NewService returns the service on d.
func NewService(d Deps) *Service { return &Service{d: d} }

// Groups are up to limit of p's account's groups from the first after
// after.
func (s *Service) Groups(
	ctx context.Context,
	p kernel.Principal,
	after kernel.ULID,
	limit int,
) ([]Group, error) {
	//nolint:wrapcheck // the store names the read
	return s.d.Store.Groups(ctx, p.AccountID, after, limit)
}

// Group reads the group id names in p's account; an ID that cannot be a
// group's is not found.
func (s *Service) Group(ctx context.Context, p kernel.Principal, id string) (Group, error) {
	g, err := parse(id)
	if err != nil {
		return Group{}, err
	}
	return s.d.Store.Group(ctx, p.AccountID, g) //nolint:wrapcheck // as above
}

// Create creates a group of p's account.
func (s *Service) Create(
	ctx context.Context,
	p kernel.Principal,
	remoteIP, name, description string,
) (Group, error) {
	if err := organization.CheckName("groupName", name); err != nil {
		return Group{}, err //nolint:wrapcheck // a FieldError, answered as it is
	}
	now := s.d.Now()
	g := Group{
		AccountID: p.AccountID, ID: kernel.NewULID(), Name: name, Description: description,
		CreatedAt: now,
	}
	e := s.entry(p, remoteIP, ActionCreate, "CREATE", g.ID,
		map[string]any{"groupName": name, "description": description})
	return s.d.Store.CreateGroup(ctx, g, e) //nolint:wrapcheck // the store names the write
}

// Update sets the description of the group id names. A group whose
// version expect does not accept is kernel.ErrStale and is not changed.
func (s *Service) Update(
	ctx context.Context,
	p kernel.Principal,
	remoteIP, id string,
	expect kernel.Expect,
	description string,
) (Group, error) {
	g, err := parse(id)
	if err != nil {
		return Group{}, err
	}
	e := s.entry(p, remoteIP, ActionUpdate, "UPDATE", g,
		map[string]any{"description": description})
	//nolint:wrapcheck // as above
	return s.d.Store.SetDescription(ctx, p.AccountID, g, expect, description, e)
}

// Delete deletes the group id names and answers it as it was. Its members
// stay managed users of the account. A group whose version expect does
// not accept is kernel.ErrStale and is kept.
func (s *Service) Delete(
	ctx context.Context,
	p kernel.Principal,
	remoteIP, id string,
	expect kernel.Expect,
) (Group, error) {
	g, err := s.Group(ctx, p, id)
	if err != nil {
		return Group{}, err
	}
	err = s.d.Store.DeleteGroup(ctx, p.AccountID, g.ID, expect,
		s.entry(p, remoteIP, ActionDelete, "DELETE", g.ID, nil))
	if err != nil {
		return Group{}, err //nolint:wrapcheck // as above
	}
	return g, nil
}

// Users are up to limit of the group's members from the first after after.
func (s *Service) Users(
	ctx context.Context,
	p kernel.Principal,
	id string,
	after kernel.MemberID,
	limit int,
) ([]user.User, error) {
	g, err := s.Group(ctx, p, id)
	if err != nil {
		return nil, err
	}
	//nolint:wrapcheck // the store names the read
	return s.d.Store.GroupUsers(ctx, p.AccountID, g.ID, after, limit)
}

// Available are up to limit of the group names p's account's managed
// users' latest sign-ins reported, from the first after after.
func (s *Service) Available(
	ctx context.Context,
	p kernel.Principal,
	after string,
	limit int,
) ([]string, error) {
	return s.d.Store.AvailableGroups(ctx, p.AccountID, after, limit) //nolint:wrapcheck // as above
}

// entry is an audit entry of p's account at the service's clock.
func (s *Service) entry(
	p kernel.Principal,
	remoteIP, action, eventType string,
	id kernel.ULID,
	body map[string]any,
) kernel.AuditEntry {
	e := kernel.AuditEntry{
		AccountID:        p.AccountID.ULID,
		Actor:            kernel.AuditActor{Username: p.Login},
		Action:           action,
		EventType:        eventType,
		AffectedResource: kernel.AuditResource{Type: ResourceGroup, ID: id.String()},
		RemoteIP:         remoteIP,
		CreatedAt:        s.d.Now(),
	}
	if body != nil {
		// A map of strings always marshals.
		e.Body, _ = json.Marshal(body)
	}
	return e
}

// parse reads a group's ID; one that cannot be is not found.
func parse(id string) (kernel.ULID, error) {
	g, err := kernel.ParseULID(id)
	if err != nil {
		return kernel.ULID{}, fmt.Errorf("%w: managed user group %q", port.ErrNotFound, id)
	}
	return g, nil
}
