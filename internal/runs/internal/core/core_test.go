package core

import (
	"context"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// store answers blockers and runs' stacks and records each read and
// delete, failing with err.
type store struct {
	blockers map[kernel.ULID]kernel.ULID
	stacks   map[kernel.ULID]kernel.ULID
	err      error
	reads    int
	deleted  []kernel.ULID
}

func (s *store) StackOf(
	_ context.Context,
	_ kernel.AccountID,
	run kernel.ULID,
) (kernel.ULID, bool, error) {
	stack, ok := s.stacks[run]
	return stack, ok, s.err
}

func (s *store) Blockers(
	context.Context,
	kernel.AccountID,
	[]kernel.ULID,
) (map[kernel.ULID]kernel.ULID, error) {
	s.reads++
	return s.blockers, s.err
}

func (s *store) DeleteStack(_ context.Context, _ kernel.AccountID, stack kernel.ULID) error {
	s.deleted = append(s.deleted, stack)
	return s.err
}

// TestUnitService checks that the service answers the store's blockers,
// reading nothing for no stacks, and runs' stacks, that a move loses
// nothing, and that a delete deletes the stack's runs.
func TestUnitService(t *testing.T) {
	ctx := t.Context()
	stack, run := kernel.NewULID(), kernel.NewULID()
	st := &store{
		blockers: map[kernel.ULID]kernel.ULID{stack: run},
		stacks:   map[kernel.ULID]kernel.ULID{run: stack},
	}
	s := New(st)
	acct := kernel.NewAccountID()
	if got, err := s.Blockers(ctx, acct, nil); err != nil || got == nil || len(got) != 0 ||
		st.reads != 0 {
		t.Errorf("Blockers of no stacks = %v, %v after %d reads", got, err, st.reads)
	}
	if got, err := s.Blockers(ctx, acct, []kernel.ULID{stack}); err != nil || got[stack] != run {
		t.Errorf("Blockers = %v, %v", got, err)
	}
	if got, err := s.RunStack(ctx, acct, run); err != nil || got != stack {
		t.Errorf("RunStack = %v, %v", got, err)
	}
	if _, err := s.RunStack(ctx, acct, stack); !errors.Is(err, stacksapi.ErrNotFound) {
		t.Errorf("RunStack of no run = %v", err)
	}
	if lost, err := s.LostOnMove(ctx, stacksapi.StackMove{}); err != nil || lost != nil {
		t.Errorf("LostOnMove = %v, %v", lost, err)
	}
	gone := stacksapi.Stack{ID: stack, Account: acct}
	if err := s.Deleting(ctx, gone); err != nil || len(st.deleted) != 1 || st.deleted[0] != stack {
		t.Errorf("Deleting = %v, deleting %v", err, st.deleted)
	}
	st.err = errors.New("down")
	if err := s.Deleting(ctx, gone); !errors.Is(err, st.err) {
		t.Errorf("Deleting on a failing store = %v", err)
	}
	if _, err := s.RunStack(ctx, acct, run); !errors.Is(err, st.err) {
		t.Errorf("RunStack on a failing store = %v", err)
	}
}
