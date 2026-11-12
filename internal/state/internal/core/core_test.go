package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// calls records the holds and opens of a fake Stacks and Store in order,
// each with whether its context carried a transaction; held is the stack
// the hold knows, and err fails the open.
type calls struct {
	log  []string
	held kernel.ULID
	err  error
}

func (c *calls) Hold(ctx context.Context, _ kernel.AccountID, id kernel.ULID) error {
	c.log = append(c.log, "hold "+id.String()+inTx(ctx))
	if id != c.held {
		return stacksapi.ErrNotFound
	}
	return nil
}

func (c *calls) OpenWorkspace(
	ctx context.Context,
	_ kernel.AccountID,
	stack kernel.ULID,
	name string,
) error {
	c.log = append(c.log, "open "+stack.String()+"/"+name+inTx(ctx))
	return c.err
}

func inTx(ctx context.Context) string {
	if tx.In(ctx) {
		return " in tx"
	}
	return ""
}

type (
	db     struct{}
	fakeTx struct{ pgx.Tx }
)

func (db) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) { return fakeTx{}, nil }
func (fakeTx) Commit(context.Context) error                       { return nil }
func (fakeTx) Rollback(context.Context) error                     { return nil }

// inTransaction runs f with a context that carries a transaction, which
// commits and rolls back nothing.
func inTransaction(t *testing.T, f func(ctx context.Context) error) error {
	t.Helper()
	return tx.Do(t.Context(), db{}, tx.Options{}, f)
}

// TestUnitWorkspace checks that a workspace is opened after its stack is
// held, both in the caller's transaction, and that nothing is written
// outside a transaction, for a name the table refuses, or for a stack the
// account lacks.
func TestUnitWorkspace(t *testing.T) {
	acct, stack := kernel.NewAccountID(), kernel.NewULID()
	c := &calls{held: stack}
	s := New(c, c)
	err := inTransaction(t, func(ctx context.Context) error {
		return s.Workspace(ctx, acct, stack, "")
	})
	want := []string{"hold " + stack.String() + " in tx", "open " + stack.String() + "/ in tx"}
	if err != nil || strings.Join(c.log, "; ") != strings.Join(want, "; ") {
		t.Errorf("Workspace = %v after %v, want %v", err, c.log, want)
	}

	c.log = nil
	if err := s.Workspace(t.Context(), acct, stack, ""); !errors.Is(err, ErrNoTransaction) ||
		len(c.log) != 0 {
		t.Errorf("outside a transaction = %v after %v", err, c.log)
	}
	for _, name := range []string{strings.Repeat("a", MaxWorkspaceName+1), "\xff", "a\x00b"} {
		err := inTransaction(t, func(ctx context.Context) error {
			return s.Workspace(ctx, acct, stack, name)
		})
		if !errors.Is(err, ErrWorkspaceName) || len(c.log) != 0 {
			t.Errorf("name %q = %v after %v", name, err, c.log)
		}
	}
	if err := inTransaction(t, func(ctx context.Context) error {
		return s.Workspace(ctx, acct, stack, strings.Repeat("a", MaxWorkspaceName))
	}); err != nil {
		t.Errorf("the longest name = %v", err)
	}

	c.log = nil
	other := kernel.NewULID()
	err = inTransaction(t, func(ctx context.Context) error {
		return s.Workspace(ctx, acct, other, "")
	})
	if !errors.Is(err, stacksapi.ErrNotFound) || len(c.log) != 1 {
		t.Errorf("a stack the account lacks = %v after %v", err, c.log)
	}
	c.err = errors.New("down")
	if err := inTransaction(t, func(ctx context.Context) error {
		return s.Workspace(ctx, acct, stack, "")
	}); !errors.Is(err, c.err) {
		t.Errorf("a failing store = %v", err)
	}
}
