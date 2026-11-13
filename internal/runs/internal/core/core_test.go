package core

import (
	"context"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// store answers blockers, runs' stacks and runs and records each read and
// delete, failing with err.
type store struct {
	blockers map[kernel.ULID]kernel.ULID
	stacks   map[kernel.ULID]kernel.ULID
	runs     map[kernel.ULID]Run
	err      error
	reads    int
	deleted  []kernel.ULID
}

func (s *store) RunOf(_ context.Context, run kernel.ULID) (Run, bool, error) {
	r, ok := s.runs[run]
	return r, ok, s.err
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

// TestUnitStateAccessTable checks each run state's grant on its stack's
// state, the run's account on every grant, one granting nothing for every
// terminal state and another stack, and the zero grant for an unknown run
// and a failing store. The grant is the state's alone, so every run type that
// reaches a state gets the same grant.
//
// ref: decision 0010 § "Who may call what"
func TestUnitStateAccessTable(t *testing.T) {
	ctx := t.Context()
	acct, stack := kernel.NewAccountID(), kernel.NewULID()
	shared := runsapi.StateGrant{Account: acct, Read: true, Lock: runsapi.StateLockShared}
	exclusive := runsapi.StateGrant{
		Account: acct, Read: true, Lock: runsapi.StateLockExclusive, Write: true,
	}
	none := runsapi.StateGrant{Account: acct}
	want := map[kernel.RunState]runsapi.StateGrant{
		kernel.RunStateInitializing: shared,
		kernel.RunStatePlanning:     shared,
		kernel.RunStateApplying:     exclusive,
		kernel.RunStateDestroying:   exclusive,
		kernel.RunStatePerforming:   exclusive,
	}
	st := &store{runs: map[kernel.ULID]Run{}}
	s := New(st)
	for _, state := range kernel.RunStates() {
		run := kernel.NewULID()
		st.runs[run] = Run{Account: acct, Stack: stack, State: state}
		w, ok := want[state]
		if !ok {
			w = none
		}
		if got, err := s.Access(ctx, run, stack); err != nil || got != w {
			t.Errorf("Access in %s = %+v, %v; want %+v", state, got, err, w)
		}
		if state.Terminal() && w != none {
			t.Errorf("terminal %s grants %+v", state, w)
		}
		if got, err := s.Access(ctx, run, kernel.NewULID()); err != nil || got != none {
			t.Errorf("Access in %s to another stack = %+v, %v", state, got, err)
		}
	}
	if got, err := s.Access(ctx, kernel.NewULID(), stack); err != nil ||
		got != (runsapi.StateGrant{}) {
		t.Errorf("Access of an unknown run = %+v, %v", got, err)
	}
	st.err = errors.New("down")
	if got, err := s.Access(ctx, kernel.NewULID(), stack); !errors.Is(err, st.err) ||
		got != (runsapi.StateGrant{}) {
		t.Errorf("Access on a failing store = %+v, %v", got, err)
	}
}
