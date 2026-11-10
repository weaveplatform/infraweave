package pg_test

import (
	"context"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/http"
)

// status is the runs domain's answer of the run blocking each stack,
// failing with err.
type status struct {
	blockers map[kernel.ULID]kernel.ULID
	err      error
}

func (s status) Blockers(
	context.Context,
	kernel.AccountID,
	[]kernel.ULID,
) (map[kernel.ULID]kernel.ULID, error) {
	return s.blockers, s.err
}

// TestAccHandlersStatus checks that a stack's view shows its state and
// blocked from the projection, NONE and unblocked with no run, its
// blocker from the bound status, none before one is bound, and that it
// fails when the runs domain does.
func TestAccHandlersStatus(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	s := http.New(f.svc, f.authz, f.spaces)
	handed := s
	admin := kernel.WithPrincipal(ctx, f.admin)
	infra, err := f.svc.Create(ctx, f.admin, "", false, input("Infra"))
	if err != nil {
		t.Fatal(err)
	}
	read := func() (api.Stack, error) {
		got, err := handed.Stack(admin, api.StackRequestObject{StackId: "infra"})
		if err != nil {
			return api.Stack{}, err
		}
		return got.(api.Stack200JSONResponse).Body, nil
	}
	v, err := read()
	if err != nil || v.State != api.StackStateNONE || !v.StateIsTerminal || v.Blocked ||
		v.Blocker != nil {
		t.Errorf("a stack with no run: %s, terminal %v, blocked %v, blocker %v, %v",
			v.State, v.StateIsTerminal, v.Blocked, v.Blocker, err)
	}

	run := kernel.NewULID()
	if err := f.svc.SetRunState(
		ctx, f.acct, infra.ID, kernel.StackState(kernel.RunStateApplying), true,
	); err != nil {
		t.Fatal(err)
	}
	s.SetStackStatus(status{blockers: map[kernel.ULID]kernel.ULID{infra.ID: run}})
	v, err = read()
	if err != nil || v.State != api.StackStateAPPLYING || v.StateIsTerminal || !v.Blocked ||
		v.Blocker == nil || v.Blocker.Id != run.String() {
		t.Errorf("a stack an applying run blocks: %s, terminal %v, blocked %v, blocker %v, %v",
			v.State, v.StateIsTerminal, v.Blocked, v.Blocker, err)
	}

	down := errors.New("runs down")
	s.SetStackStatus(status{err: down})
	if _, err := read(); !errors.Is(err, down) {
		t.Errorf("a view whose blocker fails = %v", err)
	}
}
