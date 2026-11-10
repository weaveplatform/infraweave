package role

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/binding"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ErrSystem is a change to a system role, which never changes.
var ErrSystem = errors.New("a system role never changes")

// ErrInUse is a delete of a role that still has bindings.
var ErrInUse = errors.New("the role still has bindings")

// Store keeps an account's roles. A role the account lacks is
// port.ErrNotFound, and a slug it already has port.ErrSlugTaken. Each
// write bumps the account's authorization in its transaction.
type Store interface {
	// Roles reads up to limit of the account's roles after the slug after,
	// by slug, each custom role with its actions and every role with its
	// count of bindings.
	Roles(ctx context.Context, acct kernel.AccountID, after string, limit int) ([]Role, error)
	// Role reads the account's role slug, as Roles does.
	Role(ctx context.Context, acct kernel.AccountID, slug string) (Role, error)
	// CreateRole records r with its actions.
	CreateRole(ctx context.Context, r Role) error
	// UpdateRole locks the account's role slug, has edit answer it as it is
	// to be, and writes its name, description and actions, raising its
	// version.
	UpdateRole(
		ctx context.Context,
		acct kernel.AccountID,
		slug string,
		edit func(Role) (Role, error),
	) (Role, error)
	// DeleteRole locks the account's role slug, has check refuse it or
	// not, and deletes it with its actions; a role with bindings is
	// ErrInUse.
	DeleteRole(
		ctx context.Context,
		acct kernel.AccountID,
		slug string,
		check func(Role) error,
	) (Role, error)
	// RoleBindings reads the bindings of the account's role id to users,
	// groups and API keys, oldest first.
	RoleBindings(
		ctx context.Context,
		acct kernel.AccountID,
		id kernel.ULID,
	) ([]binding.Listed, error)
}

// Input is a custom role as created; a nil Description is empty.
type Input struct {
	Name        string
	Description *string
	Actions     []accessapi.Action
}

// Change is what an update replaces: each member that is nil is kept.
type Change struct {
	Name        *string
	Description *string
	Actions     *[]accessapi.Action
}

// Service keeps each account's custom roles beside its system roles. The
// route table has decided ACCOUNT_ADMIN before a write reaches it.
//
// ref: decision 0007 § "Roles and the action catalogue"
type Service struct {
	store Store
}

// Deps are what the service needs.
type Deps struct {
	Store Store
}

// NewService returns the service over d.
func NewService(d Deps) *Service { return &Service{store: d.Store} }

// Roles lists up to limit of acct's roles after the slug after, by slug.
func (s *Service) Roles(
	ctx context.Context,
	acct kernel.AccountID,
	after string,
	limit int,
) ([]Role, error) {
	rs, err := s.store.Roles(ctx, acct, after, limit)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	for i := range rs {
		rs[i] = withActions(rs[i])
	}
	return rs, nil
}

// Role reads acct's role slug.
func (s *Service) Role(ctx context.Context, acct kernel.AccountID, slug string) (Role, error) {
	r, err := s.store.Role(ctx, acct, slug)
	if err != nil {
		return Role{}, err //nolint:wrapcheck // the store names the read
	}
	return withActions(r), nil
}

// Bindings reads the bindings of acct's role r to users, groups and API
// keys. A stack's binding arrives with the stack role bindings, which
// list it with the stack's name from the stacks domain.
func (s *Service) Bindings(
	ctx context.Context,
	acct kernel.AccountID,
	r Role,
) ([]binding.Listed, error) {
	return s.store.RoleBindings(ctx, acct, r.ID) //nolint:wrapcheck // the store names the read
}

// Create records a custom role of acct from in, its slug SlugOf its name.
// A name with no letter or digit, or an action no role may hold, is a
// *kernel.Invalid; a slug the account has is port.ErrSlugTaken.
func (s *Service) Create(ctx context.Context, acct kernel.AccountID, in Input) (Role, error) {
	r := Role{
		AccountID: acct,
		ID:        kernel.NewULID(),
		Name:      in.Name,
		Slug:      SlugOf(in.Name),
		Version:   1,
	}
	if in.Description != nil {
		r.Description = *in.Description
	}
	var errs []kernel.FieldError
	if r.Slug == "" {
		errs = append(errs, nameless("/name"))
	}
	r.Actions, errs = assignable("/actions", in.Actions, errs)
	if len(errs) > 0 {
		return Role{}, &kernel.Invalid{Detail: "The role is refused.", Errors: errs}
	}
	if err := s.store.CreateRole(ctx, r); err != nil {
		return Role{}, err //nolint:wrapcheck // the store names the write
	}
	return r, nil
}

// Update replaces what c names of acct's custom role slug, keeping its
// slug, when expect accepts its version. A system role is ErrSystem; a
// name with no letter or digit, or an action no role may hold, is a
// *kernel.Invalid; a role whose version expect refuses is kernel.ErrStale.
func (s *Service) Update(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
	expect kernel.Expect,
	c Change,
) (Role, error) {
	r, err := s.store.UpdateRole(ctx, acct, slug, func(r Role) (Role, error) {
		if r.System {
			return Role{}, fmt.Errorf("%w: %s", ErrSystem, r.Slug)
		}
		if err := expect.Check(r.Version); err != nil {
			return Role{}, err //nolint:wrapcheck // kernel.ErrStale, answered as it is
		}
		var errs []kernel.FieldError
		if c.Name != nil {
			if SlugOf(*c.Name) == "" {
				errs = append(errs, nameless("/name"))
			}
			r.Name = *c.Name
		}
		if c.Description != nil {
			r.Description = *c.Description
		}
		if c.Actions != nil {
			r.Actions, errs = assignable("/Actions", *c.Actions, errs)
		}
		if len(errs) > 0 {
			return Role{}, &kernel.Invalid{Detail: "The role change is refused.", Errors: errs}
		}
		return r, nil
	})
	if err != nil {
		return Role{}, err //nolint:wrapcheck // the store names the write; a refusal is answered as it is
	}
	return withActions(r), nil
}

// Delete deletes acct's custom role slug when expect accepts its version,
// and answers it as it was. A system role is ErrSystem, one with bindings
// ErrInUse, and one whose version expect refuses kernel.ErrStale.
func (s *Service) Delete(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
	expect kernel.Expect,
) (Role, error) {
	r, err := s.store.DeleteRole(ctx, acct, slug, func(r Role) error {
		if r.System {
			return fmt.Errorf("%w: %s", ErrSystem, r.Slug)
		}
		return expect.Check(r.Version) //nolint:wrapcheck // kernel.ErrStale, answered as it is
	})
	if err != nil {
		return Role{}, err //nolint:wrapcheck // the store names the write; a refusal is answered as it is
	}
	return withActions(r), nil
}

// withActions is r with a system role's actions from the catalogue, and a
// custom role's in catalogue order.
func withActions(r Role) Role {
	if r.System {
		r.Actions, _ = SystemActions(r.Slug)
		return r
	}
	r.Actions = inCatalogueOrder(r.Actions)
	return r
}

// inCatalogueOrder is each of actions once, in the catalogue's order.
func inCatalogueOrder(actions []accessapi.Action) []accessapi.Action {
	out := []accessapi.Action{}
	for _, a := range accessapi.Actions() {
		if slices.Contains(actions, a.Action) {
			out = append(out, a.Action)
		}
	}
	return out
}

// assignable is actions in catalogue order, appending to errs an error at
// pointer/<index> for each that no role may hold: one the catalogue
// lacks, and one it marks disabled or deprecated, which grants nothing.
//
// ref: DV-022
func assignable(
	pointer string,
	actions []accessapi.Action,
	errs []kernel.FieldError,
) ([]accessapi.Action, []kernel.FieldError) {
	for i, a := range actions {
		info, ok := accessapi.ActionOf(a)
		var why string
		switch {
		case !ok:
			why = "is not an action of the catalogue"
		case info.Disabled:
			why = "is disabled, so a role cannot hold it"
		case info.Deprecated:
			why = "is deprecated, so a role cannot hold it"
		default:
			continue
		}
		errs = append(errs, kernel.FieldError{
			Pointer: fmt.Sprintf("%s/%d", pointer, i),
			Reason:  kernel.ReasonRule,
			Message: fmt.Sprintf("%s %s.", a, why),
		})
	}
	return inCatalogueOrder(actions), errs
}

func nameless(pointer string) kernel.FieldError {
	return kernel.FieldError{
		Pointer: pointer, Reason: kernel.ReasonRule,
		Message: "A role's name needs a letter or a digit, which its slug is made of.",
	}
}

// SlugOf is the slug a custom role named name has: its letters and digits
// lowercased, each run of anything else between them one hyphen, and
// empty when the name has none.
func SlugOf(name string) string {
	var b strings.Builder
	hyphen := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			if hyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			hyphen = false
			b.WriteRune(r)
		default:
			hyphen = true
		}
	}
	return b.String()
}
