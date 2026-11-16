package pg_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// managedState is a stacksapi.ManagedState that records each stack it is
// asked to open the default workspace of, and whether the call came in a
// transaction, failing with err.
type managedState struct {
	created []kernel.ULID
	inTx    []bool
	err     error
}

func (m *managedState) Created(ctx context.Context, st stacksapi.Stack) error {
	m.created, m.inTx = append(m.created, st.ID), append(m.inTx, tx.In(ctx))
	return m.err
}

// TestAccServiceManagedState checks that a stack whose state infraweave
// keeps is created whether or not the state domain is bound; that once it
// is, such a stack's creation asks it, in the creation's transaction, and
// a stack whose state is kept elsewhere's does not; and that a refusal
// fails the create, leaving no stack.
//
// ref: decision 0010 § "Storage"
func TestAccServiceManagedState(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	if _, err := f.svc.Create(ctx, f.admin, "", true, input("Unbound")); err != nil {
		t.Fatalf("a managed stack with no state domain bound = %v", err)
	}
	m := &managedState{}
	f.svc.BindManagedState(m)
	managed, err := f.svc.Create(ctx, f.admin, "", true, input("Managed"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Create(ctx, f.admin, "", false, input("External")); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(m.created, []kernel.ULID{managed.ID}) || !slices.Equal(m.inTx, []bool{true}) {
		t.Errorf("the state domain was asked for %v, in a transaction %v; want %s in one",
			m.created, m.inTx, managed.ID)
	}

	m.err = errors.New("down")
	if _, err := f.svc.Create(ctx, f.admin, "", true, input("Refused")); !errors.Is(err, m.err) {
		t.Errorf("a creation the state domain refuses = %v", err)
	}
	if _, err := f.svc.Stack(ctx, f.acct, "refused"); !errors.Is(err, stacksapi.ErrNotFound) {
		t.Errorf("the refused stack reads %v", err)
	}
}
