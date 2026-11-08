package stacks_test

import (
	"context"
	"errors"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/stacks"
)

// store is an in-memory Store of stacks.
type store struct {
	stacks []stacksapi.Stack
	err    error
}

func (s *store) Get(
	_ context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) (stacksapi.Stack, error) {
	for _, st := range s.stacks {
		if st.Account == acct && st.ID == id {
			return st, s.err
		}
	}
	return stacksapi.Stack{}, errors.Join(stacksapi.ErrNotFound, s.err)
}

func (s *store) SpaceInUse(
	_ context.Context,
	acct kernel.AccountID,
	space kernel.ULID,
) (bool, error) {
	for _, st := range s.stacks {
		if st.Account == acct && st.Space == space {
			return true, s.err
		}
	}
	return false, s.err
}

// TestUnitService checks that the service reads through its store, guards
// a space a stack lives in, and refuses no move.
func TestUnitService(t *testing.T) {
	ctx := t.Context()
	st := stacksapi.Stack{
		ID:      kernel.NewULID(),
		Account: kernel.NewAccountID(),
		Slug:    "infra",
		Space:   kernel.NewULID(),
	}
	f := &store{stacks: []stacksapi.Stack{st}}
	svc := stacks.New(f)
	if got, err := svc.Get(ctx, st.Account, st.ID); err != nil || got != st {
		t.Errorf("Get = %+v, %v", got, err)
	}
	if _, err := svc.Get(
		ctx,
		st.Account,
		kernel.NewULID(),
	); !errors.Is(
		err,
		stacksapi.ErrNotFound,
	) {
		t.Errorf("Get of a missing stack = %v", err)
	}
	if used, err := svc.SpaceInUse(ctx, st.Account, st.Space); err != nil || !used {
		t.Errorf("SpaceInUse of the stack's space = %v, %v", used, err)
	}
	if used, err := svc.SpaceInUse(ctx, st.Account, kernel.NewULID()); err != nil || used {
		t.Errorf("SpaceInUse of an empty space = %v, %v", used, err)
	}
	lost, err := svc.LostOnMove(ctx, accessapi.SpaceMove{Account: st.Account, Space: st.Space})
	if err != nil || len(lost) != 0 {
		t.Errorf("LostOnMove = %v, %v", lost, err)
	}
	f.err = errors.New("unavailable")
	if _, err := svc.SpaceInUse(ctx, st.Account, st.Space); !errors.Is(err, f.err) {
		t.Errorf("SpaceInUse on a failing store = %v", err)
	}
}
