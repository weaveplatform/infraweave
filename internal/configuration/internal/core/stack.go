package core

import (
	"context"
	"errors"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// StackElement reads the element name of acct's stack slug. The route has
// decided SPACE_READ on the stack's space.
func (s *Service) StackElement(
	ctx context.Context,
	acct kernel.AccountID,
	slug, name string,
) (Element, error) {
	st, err := s.stack(ctx, acct, slug)
	if err != nil {
		return Element{}, err
	}
	e, err := s.store.Element(ctx, acct, Owner{Stack: st.ID}, name)
	return e, err //nolint:wrapcheck // the store names the read
}

// SetStackElement creates or replaces the element in.Name of p's
// account's stack slug and reports whether it created it, holding the
// stack so a delete of it waits. The route has decided STACK_ADD_CONFIG
// on the stack's space. Refusals point under at, the element's place in
// the request body, and its name at at+name.
func (s *Service) SetStackElement(
	ctx context.Context,
	p kernel.Principal,
	slug string,
	in ElementInput,
	at, name string,
) (Element, bool, error) {
	if errs := checkElement(at, name, in); len(errs) > 0 {
		return Element{}, false, &kernel.Invalid{
			Detail: "The config element is refused.",
			Errors: errs,
		}
	}
	var e Element
	var created bool
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		owner, err := s.stackOwner(ctx, p.AccountID, slug)
		if err != nil {
			return err
		}
		e, created, err = s.set(ctx, p.AccountID, owner, in, at)
		return err
	})
	return e, created, err //nolint:wrapcheck // each step in the transaction names itself
}

// DeleteStackElement deletes the element name of p's account's stack slug
// and answers it as it was. The route has decided STACK_DELETE_CONFIG on
// the stack's space.
func (s *Service) DeleteStackElement(
	ctx context.Context,
	p kernel.Principal,
	slug, name string,
) (Element, error) {
	var e Element
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		owner, err := s.stackOwner(ctx, p.AccountID, slug)
		if err != nil {
			return err
		}
		if e, err = s.store.Element(ctx, p.AccountID, owner, name); err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		//nolint:wrapcheck // as above
		return s.store.DeleteElements(ctx, p.AccountID, owner, []string{name})
	})
	return e, err //nolint:wrapcheck // each step in the transaction names itself
}

// StackAttachable reads a page of the contexts p's account's stack slug
// may attach that p may read: those of its space and of each space it
// reaches while each inherits. The route has decided SPACE_READ on the
// stack's space.
func (s *Service) StackAttachable(
	ctx context.Context,
	p kernel.Principal,
	slug string,
	in search.Input,
) (search.Result[Context], error) {
	st, err := s.stack(ctx, p.AccountID, slug)
	if err != nil {
		return search.Result[Context]{}, err
	}
	scope, err := s.reachScope(ctx, p, st.Space)
	if err != nil {
		return search.Result[Context]{}, err
	}
	res, err := s.store.Search(ctx, p.AccountID, in, scope)
	return res, err //nolint:wrapcheck // the store names the read
}

// stack reads acct's stack slug; a *Missing when acct lacks it.
func (s *Service) stack(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
) (stacksapi.Stack, error) {
	st, err := s.stacks.BySlug(ctx, acct, slug)
	if errors.Is(err, stacksapi.ErrNotFound) {
		return stacksapi.Stack{}, &Missing{Kind: "stack", ID: slug}
	}
	return st, err //nolint:wrapcheck // the stacks domain names the read
}

// stackOwner is the owner of acct's stack slug's elements, with the stack
// held and its elements locked for the caller's transaction: a stack's
// row is the stacks domain's, so no row lock orders two writers of one
// element as a context's row does.
func (s *Service) stackOwner(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
) (Owner, error) {
	st, err := s.stack(ctx, acct, slug)
	if err != nil {
		return Owner{}, err
	}
	if err := s.stacks.Hold(ctx, acct, st.ID); errors.Is(err, stacksapi.ErrNotFound) {
		return Owner{}, &Missing{Kind: "stack", ID: slug}
	} else if err != nil {
		return Owner{}, err //nolint:wrapcheck // the stacks domain names the lock
	}
	owner := Owner{Stack: st.ID}
	if err := s.store.LockElements(ctx, acct, owner); err != nil {
		return Owner{}, err //nolint:wrapcheck // the store names the lock
	}
	return owner, nil
}
