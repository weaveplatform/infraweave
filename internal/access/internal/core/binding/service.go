package binding

import (
	"context"
	"errors"
	"fmt"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ErrDuplicate is a binding the account already holds: the same actor,
// role and space.
var ErrDuplicate = errors.New("the actor already holds the role in the space")

// Duplicate is ErrDuplicate at Index of the bindings a write named.
type Duplicate struct{ Index int }

func (d *Duplicate) Error() string { return fmt.Sprintf("binding %d: %v", d.Index, ErrDuplicate) }

func (d *Duplicate) Unwrap() error { return ErrDuplicate }

// Store keeps an account's bindings. A binding, role, space or actor the
// account lacks is port.ErrNotFound. Each write bumps the account's
// authorization in its transaction, which a context carrying one joins.
type Store interface {
	// RoleID is the ID of the account's role slug.
	RoleID(ctx context.Context, acct kernel.AccountID, slug string) (kernel.ULID, error)
	// SpaceID is the ID of the account's space slug.
	SpaceID(ctx context.Context, acct kernel.AccountID, slug string) (kernel.ULID, error)
	// HasActor reports whether the account holds the actor of kind id.
	HasActor(
		ctx context.Context,
		acct kernel.AccountID,
		kind ActorKind,
		id kernel.ULID,
	) (bool, error)
	// Binding reads the account's binding id of an actor of kind.
	Binding(
		ctx context.Context,
		acct kernel.AccountID,
		kind ActorKind,
		id kernel.ULID,
	) (Listed, error)
	// ActorBindings reads the bindings of the actor of kind id, oldest
	// first.
	ActorBindings(
		ctx context.Context,
		acct kernel.AccountID,
		kind ActorKind,
		id kernel.ULID,
	) ([]Listed, error)
	// SpaceBindings reads the bindings in the account's space id, oldest
	// first.
	SpaceBindings(ctx context.Context, acct kernel.AccountID, id kernel.ULID) ([]Listed, error)
	// CreateBindings records bs, all or none; one the account holds is a
	// *Duplicate naming its index.
	CreateBindings(ctx context.Context, acct kernel.AccountID, bs []Binding) ([]Listed, error)
	// UpdateBinding locks the account's binding id of an actor of kind,
	// has edit answer it as it is to be, and writes its role and space,
	// raising its version; a role and space the actor holds already is a
	// *Duplicate.
	UpdateBinding(
		ctx context.Context,
		acct kernel.AccountID,
		kind ActorKind,
		id kernel.ULID,
		edit func(Listed) (Binding, error),
	) (Listed, error)
	// DeleteBinding locks the account's binding id of an actor of kind,
	// has check refuse it or not, and deletes it.
	DeleteBinding(
		ctx context.Context,
		acct kernel.AccountID,
		kind ActorKind,
		id kernel.ULID,
		check func(Listed) error,
	) (Listed, error)
	// ReplaceAccessRules replaces the access_rule bindings of the actor of
	// kind id with bs.
	ReplaceAccessRules(
		ctx context.Context,
		acct kernel.AccountID,
		kind ActorKind,
		id kernel.ULID,
		bs []Binding,
	) error
	// DeleteActorBindings deletes every binding of the actor of kind id.
	DeleteActorBindings(
		ctx context.Context,
		acct kernel.AccountID,
		kind ActorKind,
		id kernel.ULID,
	) error
}

// Request is a binding as an input names it: its actor's ID, empty when
// the actor is created with its bindings, and its role's and space's
// slugs.
type Request struct {
	Actor string
	Role  string
	Space string
}

// Fields are the names an input gives a Request's members, which a
// refusal's pointer ends in.
type Fields struct {
	Actor, Role, Space string
}

// Service grants roles to users, groups and API keys in spaces. Creating,
// changing or deleting a binding needs SPACE_ADMIN on its space, so an
// admin of a space below the top binds only in the spaces they administer;
// the route table decides it for a binding a path or body names, and the
// service for each element of a batch, the new space of an update and
// the bindings an actor's own input carries.
//
// ref: decision 0007 § "Bindings"
type Service struct {
	store Store
	authz accessapi.Authorizer
	drop  func(kernel.AccountID)
}

// Deps are what the service needs. Drop forgets this replica's
// authorization snapshot of an account after a change of its bindings.
type Deps struct {
	Store Store
	Authz accessapi.Authorizer
	Drop  func(kernel.AccountID)
}

// NewService returns the service over d.
func NewService(d Deps) *Service { return &Service{store: d.Store, authz: d.Authz, drop: d.Drop} }

// Prepare turns reqs, the bindings of actors of kind an input names at
// pointer, into bindings of origin made by p, refusing each element whose
// actor, role or space p's account lacks, or whose space p does not
// administer, at pointer/<index>/<field>. A space p cannot read is
// refused as one the account lacks, so no refusal names it.
func (s *Service) Prepare(
	ctx context.Context,
	p kernel.Principal,
	kind ActorKind,
	origin Origin,
	pointer string,
	f Fields,
	reqs []Request,
) ([]Binding, error) {
	out := make([]Binding, 0, len(reqs))
	var errs []kernel.FieldError
	for i, r := range reqs {
		at := func(field string) string { return fmt.Sprintf("%s/%d/%s", pointer, i, field) }
		b := Binding{
			AccountID: p.AccountID, ID: kernel.NewULID(), ActorKind: kind, Origin: origin,
			CreatedBy: p.Caller(), Version: 1,
		}
		if r.Actor != "" {
			id, err := s.actor(ctx, p.AccountID, kind, r.Actor)
			if err != nil && !errors.Is(err, port.ErrNotFound) {
				return nil, err
			}
			if err != nil {
				errs = append(errs, missing(at(f.Actor), "actor", r.Actor))
			}
			b.ActorID = id
		}
		var err error
		if b.RoleID, err = s.store.RoleID(
			ctx,
			p.AccountID,
			r.Role,
		); errors.Is(
			err,
			port.ErrNotFound,
		) {
			errs = append(errs, missing(at(f.Role), "role", r.Role))
		} else if err != nil {
			return nil, err //nolint:wrapcheck // the store names the read
		}
		refused, err := s.administered(ctx, p, r.Space, &b)
		if err != nil {
			return nil, err
		}
		for _, e := range refused {
			e.Pointer = at(f.Space)
			errs = append(errs, e)
		}
		out = append(out, b)
	}
	if len(errs) > 0 {
		return nil, &kernel.Invalid{Detail: "The bindings are refused.", Errors: errs}
	}
	return out, nil
}

// administered sets b's space to the account's space slug, answering the
// refusal when p's account lacks it, p cannot read it or does not
// administer it, and no refusal otherwise.
func (s *Service) administered(
	ctx context.Context,
	p kernel.Principal,
	slug string,
	b *Binding,
) ([]kernel.FieldError, error) {
	id, err := s.store.SpaceID(ctx, p.AccountID, slug)
	if err != nil && !errors.Is(err, port.ErrNotFound) {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	readable := err == nil
	if readable {
		if readable, err = s.authz.Authorize(ctx, p, accessapi.ActionSpaceRead, id); err != nil {
			return nil, err //nolint:wrapcheck // the authorizer names its failure
		}
	}
	if !readable {
		return []kernel.FieldError{missing("", "space", slug)}, nil
	}
	admin, err := s.authz.Authorize(ctx, p, accessapi.ActionSpaceAdmin, id)
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	if !admin {
		return []kernel.FieldError{{
			Reason:  kernel.ReasonRule,
			Message: fmt.Sprintf("Binding in the space %q needs SPACE_ADMIN there.", slug),
		}}, nil
	}
	b.SpaceID = id
	return []kernel.FieldError{}, nil
}

func missing(pointer, what, id string) kernel.FieldError {
	return kernel.FieldError{
		Pointer: pointer, Reason: kernel.ReasonNotFound,
		Message: fmt.Sprintf("The account has no %s %q.", what, id),
	}
}

// actor is the ID of the account's actor of kind raw names.
func (s *Service) actor(
	ctx context.Context,
	acct kernel.AccountID,
	kind ActorKind,
	raw string,
) (kernel.ULID, error) {
	id, err := kernel.ParseULID(raw)
	if err != nil {
		return kernel.ULID{}, fmt.Errorf("%w: %s %q", port.ErrNotFound, kind, raw)
	}
	ok, err := s.store.HasActor(ctx, acct, kind, id)
	switch {
	case err != nil:
		return kernel.ULID{}, err //nolint:wrapcheck // the store names the read
	case !ok:
		return kernel.ULID{}, fmt.Errorf("%w: %s %q", port.ErrNotFound, kind, raw)
	}
	return id, nil
}

// Create records the bindings reqs name at pointer, all or none, as p.
// An empty pointer names an input of one binding, whose refusals point at
// its fields.
func (s *Service) Create(
	ctx context.Context,
	p kernel.Principal,
	kind ActorKind,
	pointer string,
	f Fields,
	reqs []Request,
) ([]Listed, error) {
	bs, err := s.Prepare(ctx, p, kind, OriginBinding, pointer, f, reqs)
	if err != nil && pointer == "" {
		return nil, unprefixed(err, "The binding is refused.")
	}
	if err != nil {
		return nil, err
	}
	out, err := s.store.CreateBindings(ctx, p.AccountID, bs)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the write; a duplicate is answered as it is
	}
	s.drop(p.AccountID)
	return out, nil
}

// Bind records bs, which Prepare made, for the actor of kind id, in the
// transaction ctx carries when the actor is created with them.
func (s *Service) Bind(
	ctx context.Context,
	acct kernel.AccountID,
	kind ActorKind,
	id kernel.ULID,
	bs []Binding,
) ([]Listed, error) {
	if len(bs) == 0 {
		return []Listed{}, nil
	}
	for i := range bs {
		bs[i].ActorKind, bs[i].ActorID = kind, id
	}
	out, err := s.store.CreateBindings(ctx, acct, bs)
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	s.drop(acct)
	return out, nil
}

// ReplaceRules replaces the access_rule bindings of the actor of kind id
// with bs, which Prepare made.
func (s *Service) ReplaceRules(
	ctx context.Context,
	acct kernel.AccountID,
	kind ActorKind,
	id kernel.ULID,
	bs []Binding,
) error {
	for i := range bs {
		bs[i].ActorKind, bs[i].ActorID = kind, id
	}
	if err := s.store.ReplaceAccessRules(ctx, acct, kind, id, bs); err != nil {
		return err //nolint:wrapcheck // as above
	}
	s.drop(acct)
	return nil
}

// Releasable refuses, at pointer, each access_rule binding of p's
// account's actor of kind id that keep omits in a space p does not
// administer, so replacing an actor's access rules never removes one p
// could not have made.
func (s *Service) Releasable(
	ctx context.Context,
	p kernel.Principal,
	kind ActorKind,
	id kernel.ULID,
	pointer string,
	keep []Binding,
) error {
	held, err := s.store.ActorBindings(ctx, p.AccountID, kind, id)
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	kept := make(map[[2]kernel.ULID]bool, len(keep))
	for _, b := range keep {
		kept[[2]kernel.ULID{b.RoleID, b.SpaceID}] = true
	}
	var errs []kernel.FieldError
	for _, l := range held {
		if l.Origin != OriginAccessRule || kept[[2]kernel.ULID{l.RoleID, l.SpaceID}] {
			continue
		}
		admin, err := s.authz.Authorize(ctx, p, accessapi.ActionSpaceAdmin, l.SpaceID)
		if err != nil {
			return err //nolint:wrapcheck // the authorizer names its failure
		}
		if !admin {
			errs = append(errs, kernel.FieldError{
				Pointer: pointer,
				Reason:  kernel.ReasonRule,
				Message: fmt.Sprintf(
					"Removing the access rule in the space %q needs SPACE_ADMIN there.",
					l.Space,
				),
			})
		}
	}
	if len(errs) > 0 {
		return &kernel.Invalid{Detail: "The bindings are refused.", Errors: errs}
	}
	return nil
}

// Binding reads acct's binding raw names of an actor of kind.
func (s *Service) Binding(
	ctx context.Context,
	acct kernel.AccountID,
	kind ActorKind,
	raw string,
) (Listed, error) {
	id, err := kernel.ParseULID(raw)
	if err != nil {
		return Listed{}, fmt.Errorf("%w: binding %q", port.ErrNotFound, raw)
	}
	return s.store.Binding(ctx, acct, kind, id) //nolint:wrapcheck // the store names the read
}

// Held reads the bindings of acct's actor of kind id, oldest first.
func (s *Service) Held(
	ctx context.Context,
	acct kernel.AccountID,
	kind ActorKind,
	id kernel.ULID,
) ([]Listed, error) {
	return s.store.ActorBindings(ctx, acct, kind, id) //nolint:wrapcheck // the store names the read
}

// InSpace reads the bindings in acct's space id, oldest first.
func (s *Service) InSpace(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) ([]Listed, error) {
	return s.store.SpaceBindings(ctx, acct, id) //nolint:wrapcheck // the store names the read
}

// Unbind deletes every binding of acct's actor of kind id, as removing
// an actor another domain keeps does.
func (s *Service) Unbind(
	ctx context.Context,
	acct kernel.AccountID,
	kind ActorKind,
	id kernel.ULID,
) error {
	if err := s.store.DeleteActorBindings(ctx, acct, kind, id); err != nil {
		return err //nolint:wrapcheck // the store names the write
	}
	s.drop(acct)
	return nil
}

// Of reads the bindings of acct's actor of kind raw names, oldest first;
// an actor the account lacks is port.ErrNotFound.
func (s *Service) Of(
	ctx context.Context,
	acct kernel.AccountID,
	kind ActorKind,
	raw string,
) ([]Listed, error) {
	id, err := s.actor(ctx, acct, kind, raw)
	if err != nil {
		return nil, err
	}
	return s.store.ActorBindings(ctx, acct, kind, id) //nolint:wrapcheck // as above
}

// Update moves p's account's binding raw names to the role and space r
// names, when expect accepts its version. Its actor never changes, and
// the new space needs SPACE_ADMIN as the old one does.
func (s *Service) Update(
	ctx context.Context,
	p kernel.Principal,
	kind ActorKind,
	raw string,
	expect kernel.Expect,
	f Fields,
	r Request,
) (Listed, error) {
	cur, err := s.Binding(ctx, p.AccountID, kind, raw)
	if err != nil {
		return Listed{}, err
	}
	bs, err := s.Prepare(
		ctx,
		p,
		kind,
		OriginBinding,
		"",
		f,
		[]Request{{Role: r.Role, Space: r.Space}},
	)
	if err != nil {
		return Listed{}, unprefixed(err, "The binding change is refused.")
	}
	if r.Actor != "" && r.Actor != cur.ActorID.String() {
		return Listed{}, &kernel.Invalid{
			Detail: "The binding change is refused.",
			Errors: []kernel.FieldError{{
				Pointer: "/" + f.Actor, Reason: kernel.ReasonRule,
				Message: "A binding's actor never changes; create a binding for the other actor.",
			}},
		}
	}
	out, err := s.store.UpdateBinding(
		ctx,
		p.AccountID,
		kind,
		cur.ID,
		func(l Listed) (Binding, error) {
			if err := expect.Check(l.Version); err != nil {
				return Binding{}, err //nolint:wrapcheck // kernel.ErrStale, answered as it is
			}
			b := l.Binding
			b.RoleID, b.SpaceID = bs[0].RoleID, bs[0].SpaceID
			return b, nil
		},
	)
	if err != nil {
		return Listed{}, err //nolint:wrapcheck // the store names the write; a refusal is answered as it is
	}
	s.drop(p.AccountID)
	return out, nil
}

// unprefixed is err with each of its field errors' pointers less the
// element index Prepare gives them, for an input of one binding, and with
// detail.
func unprefixed(err error, detail string) error {
	inv, ok := errors.AsType[*kernel.Invalid](err)
	if !ok {
		return err
	}
	for i, e := range inv.Errors {
		inv.Errors[i].Pointer = e.Pointer[len("/0"):]
	}
	inv.Detail = detail
	return inv
}

// Delete deletes p's account's binding raw names, when expect accepts
// its version, and answers it as it was.
func (s *Service) Delete(
	ctx context.Context,
	acct kernel.AccountID,
	kind ActorKind,
	raw string,
	expect kernel.Expect,
) (Listed, error) {
	id, err := kernel.ParseULID(raw)
	if err != nil {
		return Listed{}, fmt.Errorf("%w: binding %q", port.ErrNotFound, raw)
	}
	out, err := s.store.DeleteBinding(ctx, acct, kind, id, func(l Listed) error {
		return expect.Check(l.Version) //nolint:wrapcheck // kernel.ErrStale, answered as it is
	})
	if err != nil {
		return Listed{}, err //nolint:wrapcheck // as above
	}
	s.drop(acct)
	return out, nil
}
