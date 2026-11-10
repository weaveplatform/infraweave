package core

import (
	"context"
	"errors"
	"fmt"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Binding is a stack's role binding with the stack it binds.
type Binding struct {
	accessapi.RoleBinding
	Stack stacksapi.Stack
}

// BindRequest is a stack binding as an input names it: the stack by its
// slug, and its role and space by theirs.
type BindRequest struct {
	Stack string
	Role  string
	Space string
}

// The modes of the stacks a space's access view lists: the stacks that
// live in it, or every stack a grant reaches it for.
const (
	Residing      = "RESIDING"
	AllWithAccess = "ALL_WITH_ACCESS"
)

// Holding is a stack a space's grants reach, with those grants.
type Holding struct {
	Stack
	Grants []accessapi.StackGrant
}

// BindingSpaceOf is the space of the stack acct's stack binding raw
// binds, which the access domain's middleware decides the binding's
// operations on; accessapi.ErrNoSubject when acct lacks either.
func (s *Service) BindingSpaceOf(
	ctx context.Context,
	acct kernel.AccountID,
	raw string,
) (kernel.ULID, error) {
	b, err := s.Binding(ctx, acct, raw)
	if errors.Is(err, accessapi.ErrNoBinding) {
		return kernel.ULID{}, accessapi.ErrNoSubject
	}
	return b.Stack.Space, err
}

// Binding reads acct's stack binding raw with its stack;
// accessapi.ErrNoBinding when acct lacks it or its stack.
func (s *Service) Binding(ctx context.Context, acct kernel.AccountID, raw string) (Binding, error) {
	id, err := kernel.ParseULID(raw)
	if err != nil {
		return Binding{}, fmt.Errorf("%w: %q", accessapi.ErrNoBinding, raw)
	}
	b, err := s.bindings.Binding(ctx, acct, accessapi.ActorStack, id)
	if err != nil {
		return Binding{}, err //nolint:wrapcheck // the access domain names the read
	}
	st, err := s.store.Get(ctx, acct, b.ActorID)
	if errors.Is(err, stacksapi.ErrNotFound) {
		return Binding{}, fmt.Errorf("%w: %s", accessapi.ErrNoBinding, id)
	}
	if err != nil {
		return Binding{}, err //nolint:wrapcheck // the store names the read
	}
	return Binding{RoleBinding: b, Stack: st}, nil
}

// Bindings reads the bindings of acct's stack slug, oldest first.
func (s *Service) Bindings(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
) ([]Binding, error) {
	st, err := s.store.BySlug(ctx, acct, slug, false)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	bs, err := s.bindings.ByActor(ctx, acct, accessapi.ActorStack, st.ID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the access domain names the read
	}
	out := make([]Binding, 0, len(bs))
	for _, b := range bs {
		out = append(out, Binding{RoleBinding: b, Stack: st.Stack})
	}
	return out, nil
}

// Bind binds the stack r names to its role in its space, as p. The route
// has decided STACK_MANAGE on the stack's space; the space bound in needs
// SPACE_ADMIN, refused as a *Denied, and one p cannot read is a
// *SpaceError. A binding on the top space needs the stack to live there.
//
// ref: decision 0007 § "Bindings"
func (s *Service) Bind(ctx context.Context, p kernel.Principal, r BindRequest) (Binding, error) {
	var out Binding
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		st, err := s.held(ctx, p.AccountID, r.Stack)
		if err != nil {
			return err
		}
		if err := s.target(ctx, p, st, r.Space, "/spaceID"); err != nil {
			return err
		}
		bs, err := s.bindings.Bind(ctx, p, accessapi.ActorStack, "", []accessapi.BindingRequest{
			{Actor: st.ID, Role: r.Role, Space: r.Space},
		})
		if err != nil {
			return err //nolint:wrapcheck // a refusal, answered as it is
		}
		out = Binding{RoleBinding: bs[0], Stack: st}
		return nil
	})
	return out, err //nolint:wrapcheck // each step in the transaction names itself
}

// BindAll binds each stack rs names, all or none, as p, refusing every
// element it must at /bindings/<index>: a stack p cannot read is one the
// account lacks, and one whose space p may not perform STACK_MANAGE in,
// a top-space binding of a stack below the top space, and a space p does
// not administer are refused by rule.
//
// ref: decision 0007 § "Bindings"
func (s *Service) BindAll(
	ctx context.Context,
	p kernel.Principal,
	rs []BindRequest,
) ([]Binding, error) {
	const pointer = "/bindings"
	var out []Binding
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		stacks := make([]stacksapi.Stack, len(rs))
		reqs := make([]accessapi.BindingRequest, len(rs))
		var errs []kernel.FieldError
		for i, r := range rs {
			at := func(field string) string { return fmt.Sprintf("%s/%d/%s", pointer, i, field) }
			refused, err := s.managed(ctx, p, r.Stack, at("stackID"), &stacks[i])
			if err != nil {
				return err
			}
			if len(refused) == 0 {
				if refused, err = s.topSpace(
					ctx, p.AccountID, stacks[i], r.Space, at("spaceID"),
				); err != nil {
					return err
				}
			}
			errs = append(errs, refused...)
			reqs[i] = accessapi.BindingRequest{Actor: stacks[i].ID, Role: r.Role, Space: r.Space}
		}
		if len(errs) > 0 {
			return &kernel.Invalid{Detail: "The bindings are refused.", Errors: errs}
		}
		bs, err := s.bindings.Bind(ctx, p, accessapi.ActorStack, pointer, reqs)
		if err != nil {
			return err //nolint:wrapcheck // a refusal, answered as it is
		}
		out = make([]Binding, len(bs))
		for i, b := range bs {
			out[i] = Binding{RoleBinding: b, Stack: stacks[i]}
		}
		return nil
	})
	return out, err //nolint:wrapcheck // each step in the transaction names itself
}

// Rebind moves acct's stack binding raw to the role and space r names,
// when expect accepts its version, as p. The route has decided
// STACK_MANAGE on the stack's space; the space the binding is in and the
// one it moves to each need SPACE_ADMIN, the first decided under the
// binding's lock, and the second follows Bind's rules. r's stack is not
// read, since a binding's stack never changes.
func (s *Service) Rebind(
	ctx context.Context,
	p kernel.Principal,
	raw string,
	expect kernel.Expect,
	r BindRequest,
) (Binding, error) {
	var out Binding
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		cur, err := s.heldBinding(ctx, p.AccountID, raw)
		if err != nil {
			return err
		}
		if err := s.target(ctx, p, cur.Stack, r.Space, "/spaceID"); err != nil {
			return err
		}
		b, err := s.bindings.Rebind(ctx, p, accessapi.ActorStack, cur.ID, expect,
			accessapi.BindingRequest{Role: r.Role, Space: r.Space}, s.administers(ctx, p))
		if err != nil {
			return err //nolint:wrapcheck // a refusal, answered as it is
		}
		out = Binding{RoleBinding: b, Stack: cur.Stack}
		return nil
	})
	return out, err //nolint:wrapcheck // each step in the transaction names itself
}

// Unbind deletes acct's stack binding raw, when expect accepts its
// version, as p, and answers it as it was. The route has decided
// STACK_MANAGE on the stack's space; the space the binding is in needs
// SPACE_ADMIN, decided under the binding's lock.
func (s *Service) Unbind(
	ctx context.Context,
	p kernel.Principal,
	raw string,
	expect kernel.Expect,
) (Binding, error) {
	var out Binding
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		cur, err := s.heldBinding(ctx, p.AccountID, raw)
		if err != nil {
			return err
		}
		b, err := s.bindings.Unbind(ctx, p.AccountID, accessapi.ActorStack, cur.ID, expect,
			s.administers(ctx, p))
		if err != nil {
			return err //nolint:wrapcheck // a refusal, answered as it is
		}
		out = Binding{RoleBinding: b, Stack: cur.Stack}
		return nil
	})
	return out, err //nolint:wrapcheck // each step in the transaction names itself
}

// held reads acct's stack slug and holds it in the context's transaction,
// so its delete waits for the binding the caller writes and removes it.
func (s *Service) held(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
) (stacksapi.Stack, error) {
	st, err := s.store.BySlug(ctx, acct, slug, false)
	if err != nil {
		return stacksapi.Stack{}, err //nolint:wrapcheck // the store names the read
	}
	if err := s.store.Hold(ctx, acct, st.ID); err != nil {
		return stacksapi.Stack{}, err //nolint:wrapcheck // the store names the lock
	}
	return st.Stack, nil
}

// heldBinding reads acct's stack binding raw and holds its stack, as held
// does.
func (s *Service) heldBinding(
	ctx context.Context,
	acct kernel.AccountID,
	raw string,
) (Binding, error) {
	b, err := s.Binding(ctx, acct, raw)
	if err != nil {
		return Binding{}, err
	}
	if err := s.store.Hold(ctx, acct, b.Stack.ID); errors.Is(err, stacksapi.ErrNotFound) {
		return Binding{}, fmt.Errorf("%w: %s", accessapi.ErrNoBinding, raw)
	} else if err != nil {
		return Binding{}, err //nolint:wrapcheck // the store names the lock
	}
	return b, nil
}

// administers refuses a binding in a space p does not administer, as a
// *Denied.
func (s *Service) administers(
	ctx context.Context,
	p kernel.Principal,
) func(accessapi.RoleBinding) error {
	return func(b accessapi.RoleBinding) error {
		return s.require(ctx, p, accessapi.ActionSpaceAdmin, b.SpaceID)
	}
}

// target refuses binding st in the space slug, as p, unless p reads it
// and administers it and, for the top space, st lives there; a refusal of
// the top space points at pointer.
func (s *Service) target(
	ctx context.Context,
	p kernel.Principal,
	st stacksapi.Stack,
	slug, pointer string,
) error {
	id, err := s.spaces.Find(ctx, p.AccountID, slug)
	if errors.Is(err, accessapi.ErrNoSpace) {
		return &SpaceError{Slug: slug}
	} else if err != nil {
		return err //nolint:wrapcheck // the access domain names the read
	}
	if err := s.require(ctx, p, accessapi.ActionSpaceRead, id); err != nil {
		if _, ok := errors.AsType[*Denied](err); ok {
			return &SpaceError{Slug: slug}
		}
		return err
	}
	if err := s.require(ctx, p, accessapi.ActionSpaceAdmin, id); err != nil {
		return err
	}
	if refused := topRefused(p.AccountID, st, id, pointer); refused != nil {
		return &kernel.Invalid{
			Detail: "The binding is refused.", Errors: []kernel.FieldError{*refused},
		}
	}
	return nil
}

// managed reads the stack slug into st and holds it, as held does,
// answering the refusal at pointer when p's account lacks it, p cannot
// read its space or may not perform STACK_MANAGE there, and none
// otherwise.
func (s *Service) managed(
	ctx context.Context,
	p kernel.Principal,
	slug, pointer string,
	st *stacksapi.Stack,
) ([]kernel.FieldError, error) {
	var err error
	*st, err = s.held(ctx, p.AccountID, slug)
	if err != nil && !errors.Is(err, stacksapi.ErrNotFound) {
		return nil, err
	}
	read := err == nil
	if read {
		if read, err = s.authz.Authorize(ctx, p, accessapi.ActionSpaceRead, st.Space); err != nil {
			return nil, err //nolint:wrapcheck // the snapshot's read names itself
		}
	}
	if !read {
		return []kernel.FieldError{{
			Pointer: pointer, Reason: kernel.ReasonNotFound,
			Message: fmt.Sprintf("The account has no stack %q.", slug),
		}}, nil
	}
	ok, err := s.authz.Authorize(ctx, p, accessapi.ActionStackManage, st.Space)
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	if !ok {
		return []kernel.FieldError{{
			Pointer: pointer, Reason: kernel.ReasonRule,
			Message: fmt.Sprintf("Binding the stack %q needs STACK_MANAGE in its space.", slug),
		}}, nil
	}
	return []kernel.FieldError{}, nil
}

// topSpace is the refusal at pointer of binding st in the space slug when
// it is acct's top space and st lives below it, and none otherwise; a
// space acct lacks is left to the binding's own refusal.
func (s *Service) topSpace(
	ctx context.Context,
	acct kernel.AccountID,
	st stacksapi.Stack,
	slug, pointer string,
) ([]kernel.FieldError, error) {
	id, err := s.spaces.Find(ctx, acct, slug)
	if errors.Is(err, accessapi.ErrNoSpace) {
		return []kernel.FieldError{}, nil
	} else if err != nil {
		return nil, err //nolint:wrapcheck // the access domain names the read
	}
	if refused := topRefused(acct, st, id, pointer); refused != nil {
		return []kernel.FieldError{*refused}, nil
	}
	return []kernel.FieldError{}, nil
}

// topRefused is the refusal at pointer of binding st in space when space
// is acct's top space, whose ID is the account's, and st lives below it.
func topRefused(
	acct kernel.AccountID,
	st stacksapi.Stack,
	space kernel.ULID,
	pointer string,
) *kernel.FieldError {
	if space != acct.ULID || st.Space == acct.ULID {
		return nil
	}
	return &kernel.FieldError{
		Pointer: pointer, Reason: kernel.ReasonRule,
		Message: fmt.Sprintf(
			"Binding the stack %q on the top space needs it to live in the top space.",
			st.Slug,
		),
	}
}

// Holding reads a page of the stacks a space's access view lists, with
// the grants that reach the space for each, as p. The route has decided
// SPACE_READ on the space; AllWithAccess needs SPACE_ADMIN there, refused
// as a *Denied. Each stack is listed whatever space it lives in, as the
// reference's narrow view of it does, so the space's admin sees every
// stack that can act in it.
func (s *Service) Holding(
	ctx context.Context,
	p kernel.Principal,
	slug, mode string,
	in search.Input,
) (search.Result[Holding], error) {
	scope, grants, err := s.holding(ctx, p, slug, mode)
	if err != nil {
		return search.Result[Holding]{}, err
	}
	res, err := s.store.Search(ctx, p.AccountID, in, scope)
	if err != nil {
		return search.Result[Holding]{}, err //nolint:wrapcheck // as the store's
	}
	out := search.Result[Holding]{
		HasNext: res.HasNext, HasPrevious: res.HasPrevious, Start: res.Start, End: res.End,
		Edges: make([]search.Edge[Holding], 0, len(res.Edges)),
	}
	for _, e := range res.Edges {
		out.Edges = append(out.Edges, search.Edge[Holding]{
			Cursor: e.Cursor, Node: Holding{Stack: e.Node, Grants: grants[e.Node.ID]},
		})
	}
	return out, nil
}

// SuggestHolding lists the values the stacks a space's access view lists
// hold, under in's predicates, as Holding decides them.
func (s *Service) SuggestHolding(
	ctx context.Context,
	p kernel.Principal,
	slug, mode string,
	in search.SuggestionsInput,
) (search.Suggestions, error) {
	scope, _, err := s.holding(ctx, p, slug, mode)
	if err != nil {
		return search.Suggestions{}, err
	}
	return s.store.Suggest(ctx, p.AccountID, in, scope) //nolint:wrapcheck // as the store's
}

// holding is the scope of the space slug's access view in mode, holding
// a search to the stacks it lists, and the grants that reach the space
// for each of them.
func (s *Service) holding(
	ctx context.Context,
	p kernel.Principal,
	slug, mode string,
) (Scope, map[kernel.ULID][]accessapi.StackGrant, error) {
	space, err := s.spaces.Find(ctx, p.AccountID, slug)
	if errors.Is(err, accessapi.ErrNoSpace) {
		return Scope{}, nil, &SpaceError{Slug: slug}
	} else if err != nil {
		return Scope{}, nil, err //nolint:wrapcheck // the access domain names the read
	}
	if mode == AllWithAccess {
		if err := s.require(ctx, p, accessapi.ActionSpaceAdmin, space); err != nil {
			return Scope{}, nil, err
		}
	}
	tree, err := s.spaces.Tree(ctx, p.AccountID)
	if err != nil {
		return Scope{}, nil, err //nolint:wrapcheck // as above
	}
	all := make([]kernel.ULID, len(tree))
	for i, n := range tree {
		all[i] = n.ID
	}
	candidates := []kernel.ULID{space}
	if mode == AllWithAccess {
		candidates = all
	}
	stacks, err := s.store.InSpaces(ctx, p.AccountID, candidates)
	if err != nil {
		return Scope{}, nil, err //nolint:wrapcheck // the store names the read
	}
	homes := make([]accessapi.StackHome, len(stacks))
	for i, st := range stacks {
		homes[i] = accessapi.StackHome{Stack: st.ID, Space: st.Space}
	}
	explained, err := s.grants.Stacks(ctx, p.AccountID, space, homes)
	if err != nil {
		return Scope{}, nil, err //nolint:wrapcheck // the access domain names the read
	}
	grants := make(map[kernel.ULID][]accessapi.StackGrant, len(stacks))
	ids := make([]kernel.ULID, 0, len(stacks))
	for i, gs := range explained {
		if len(gs) > 0 {
			grants[homes[i].Stack] = gs
			ids = append(ids, homes[i].Stack)
		}
	}
	return Scope{Reachable: all, Spaces: tree, Actor: ActorOf(p), Stacks: ids}, grants, nil
}
