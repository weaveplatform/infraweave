package assembly

import (
	"context"
	"errors"
	"fmt"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/binding"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// bindingEntities are the route table's entity of each binding kind a
// path names, as its operations' x-infraweave-subject spells it.
var bindingEntities = map[string]binding.ActorKind{
	"user_role_binding":       binding.ActorUser,
	"user_group_role_binding": binding.ActorGroup,
	"api_key_role_binding":    binding.ActorAPIKey,
}

// bindingSpace finds the space of the account's binding of an actor of
// kind, so the middleware decides its operations on that space.
func bindingSpace(store binding.Store, kind binding.ActorKind) accessapi.SpaceOf {
	return func(ctx context.Context, acct kernel.AccountID, raw string) (kernel.ULID, error) {
		id, err := kernel.ParseULID(raw)
		if err != nil {
			return kernel.ULID{}, accessapi.ErrNoSubject
		}
		b, err := store.Binding(ctx, acct, kind, id)
		if errors.Is(err, port.ErrNotFound) {
			return kernel.ULID{}, accessapi.ErrNoSubject
		}
		return b.SpaceID, err //nolint:wrapcheck // the store names the read
	}
}

// Bindings are the role bindings of the actors another domain keeps.
func (d *Domain) Bindings() accessapi.Bindings { return bindings{d.bindings} }

// bindings is accessapi.Bindings over the binding service.
type bindings struct{ svc *binding.Service }

func (b bindings) Bind(
	ctx context.Context,
	p kernel.Principal,
	actor string,
	pointer string,
	reqs []accessapi.BindingRequest,
) ([]accessapi.RoleBinding, error) {
	rs := make([]binding.Request, 0, len(reqs))
	for _, r := range reqs {
		rs = append(rs, binding.Request{Role: r.Role, Space: r.Space})
	}
	kind := binding.ActorKind(actor)
	bs, err := b.svc.Prepare(ctx, p, kind, binding.OriginBinding, pointer,
		binding.Fields{Role: "roleID", Space: "spaceID"}, rs)
	if err != nil && pointer == "" {
		//nolint:wrapcheck // a refusal, answered as it is
		return nil, binding.Unprefixed(err, "The binding is refused.")
	}
	if err != nil {
		return nil, err //nolint:wrapcheck // a refusal, answered as it is
	}
	for i := range bs {
		bs[i].ActorID = reqs[i].Actor
	}
	ls, err := b.svc.Record(ctx, p.AccountID, bs)
	if d, ok := errors.AsType[*binding.Duplicate](err); ok {
		if pointer == "" {
			return nil, accessapi.ErrBindingHeld
		}
		return nil, fmt.Errorf("%w: %s/%d", accessapi.ErrBindingHeld, pointer, d.Index)
	}
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the write
	}
	return roleBindings(ls), nil
}

func (b bindings) Binding(
	ctx context.Context,
	acct kernel.AccountID,
	actor string,
	id kernel.ULID,
) (accessapi.RoleBinding, error) {
	l, err := b.svc.Binding(ctx, acct, binding.ActorKind(actor), id.String())
	if err != nil {
		return accessapi.RoleBinding{}, noBinding(err, id)
	}
	return roleBinding(l), nil
}

func (b bindings) Rebind(
	ctx context.Context,
	p kernel.Principal,
	actor string,
	id kernel.ULID,
	expect kernel.Expect,
	req accessapi.BindingRequest,
	check func(accessapi.RoleBinding) error,
) (accessapi.RoleBinding, error) {
	l, err := b.svc.Update(ctx, p, binding.ActorKind(actor), id.String(), expect,
		binding.Fields{Role: "roleID", Space: "spaceID"},
		binding.Request{Role: req.Role, Space: req.Space}, checked(check))
	if _, ok := errors.AsType[*binding.Duplicate](err); ok {
		return accessapi.RoleBinding{}, fmt.Errorf("%w: %s", accessapi.ErrBindingHeld, id)
	}
	if err != nil {
		return accessapi.RoleBinding{}, noBinding(err, id)
	}
	return roleBinding(l), nil
}

func (b bindings) Unbind(
	ctx context.Context,
	acct kernel.AccountID,
	actor string,
	id kernel.ULID,
	expect kernel.Expect,
	check func(accessapi.RoleBinding) error,
) (accessapi.RoleBinding, error) {
	l, err := b.svc.Delete(ctx, acct, binding.ActorKind(actor), id.String(), expect, checked(check))
	if err != nil {
		return accessapi.RoleBinding{}, noBinding(err, id)
	}
	return roleBinding(l), nil
}

// checked is check, nil for none, over the binding service's bindings.
func checked(check func(accessapi.RoleBinding) error) func(binding.Listed) error {
	if check == nil {
		return nil
	}
	return func(l binding.Listed) error { return check(roleBinding(l)) }
}

// noBinding is err with a binding the account lacks as ErrNoBinding, and
// err itself otherwise, a refusal answered as it is.
func noBinding(err error, id kernel.ULID) error {
	if errors.Is(err, port.ErrNotFound) {
		return fmt.Errorf("%w: %s", accessapi.ErrNoBinding, id)
	}
	return err
}

func (b bindings) UnbindActor(
	ctx context.Context,
	acct kernel.AccountID,
	actor string,
	id kernel.ULID,
) error {
	return b.svc.Unbind(ctx, acct, binding.ActorKind(actor), id) //nolint:wrapcheck // as above
}

func (b bindings) ByActor(
	ctx context.Context,
	acct kernel.AccountID,
	actor string,
	id kernel.ULID,
) ([]accessapi.RoleBinding, error) {
	ls, err := b.svc.Held(ctx, acct, binding.ActorKind(actor), id)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	return roleBindings(ls), nil
}

func (b bindings) BySpace(
	ctx context.Context,
	acct kernel.AccountID,
	space kernel.ULID,
) ([]accessapi.RoleBinding, error) {
	ls, err := b.svc.InSpace(ctx, acct, space)
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	return roleBindings(ls), nil
}

func roleBinding(l binding.Listed) accessapi.RoleBinding {
	return accessapi.RoleBinding{
		ID: l.ID, Actor: string(l.ActorKind), ActorID: l.ActorID, Role: l.Role,
		SpaceID: l.SpaceID, Space: l.Space, CreatedAt: l.CreatedAt, CreatedBy: l.CreatedBy,
		Version: l.Version,
	}
}

func roleBindings(ls []binding.Listed) []accessapi.RoleBinding {
	out := make([]accessapi.RoleBinding, 0, len(ls))
	for _, l := range ls {
		out = append(out, roleBinding(l))
	}
	return out
}
