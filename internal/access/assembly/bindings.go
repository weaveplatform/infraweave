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
	id kernel.ULID,
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
	if err != nil {
		return nil, err //nolint:wrapcheck // a refusal, answered as it is
	}
	ls, err := b.svc.Bind(ctx, p.AccountID, kind, id, bs)
	if d, ok := errors.AsType[*binding.Duplicate](err); ok {
		return nil, fmt.Errorf("%w: %s/%d", accessapi.ErrBindingHeld, pointer, d.Index)
	}
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the write
	}
	return roleBindings(ls), nil
}

func (b bindings) Unbind(
	ctx context.Context,
	acct kernel.AccountID,
	actor string,
	id kernel.ULID,
) (accessapi.RoleBinding, error) {
	l, err := b.svc.Delete(ctx, acct, binding.ActorKind(actor), id.String(), kernel.Expect{})
	if errors.Is(err, port.ErrNotFound) {
		return accessapi.RoleBinding{}, fmt.Errorf("%w: %s", accessapi.ErrNoBinding, id)
	}
	if err != nil {
		return accessapi.RoleBinding{}, err //nolint:wrapcheck // the store names the write
	}
	return roleBinding(l), nil
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
	}
}

func roleBindings(ls []binding.Listed) []accessapi.RoleBinding {
	out := make([]accessapi.RoleBinding, 0, len(ls))
	for _, l := range ls {
		out = append(out, roleBinding(l))
	}
	return out
}
