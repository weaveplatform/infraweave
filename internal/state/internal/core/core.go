package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// MaxWorkspaceName is the longest workspace name in bytes; it matches the
// CHECK on state_workspaces.name in the pg adapter's migration.
const MaxWorkspaceName = 512

var (
	// ErrNoTransaction reports a workspace opened outside a transaction,
	// whose stack's hold would end before the caller's writes.
	ErrNoTransaction = errors.New("state: workspace opened outside a transaction")
	// ErrWorkspaceName reports a workspace name longer than
	// MaxWorkspaceName, not UTF-8, or holding a NUL, which PostgreSQL's
	// text refuses.
	ErrWorkspaceName = errors.New("state: invalid workspace name")
)

// Store is the state domain's storage port.
type Store interface {
	// OpenWorkspace makes acct's stack's workspace name exist, in the
	// context's transaction, leaving one that exists as it is.
	OpenWorkspace(ctx context.Context, acct kernel.AccountID, stack kernel.ULID, name string) error
}

// Stacks is what the state domain asks of the stacks domain;
// stacksapi.Stacks is one.
type Stacks interface {
	// Hold locks acct's stack id in the context's transaction until it
	// ends, so the stack's delete waits for the caller to commit;
	// stacksapi.ErrNotFound when acct lacks it.
	Hold(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error
}

// Service is the state domain's core.
type Service struct {
	store  Store
	stacks Stacks
}

// New returns the service over store, holding stacks through stacks.
func New(store Store, stacks Stacks) *Service {
	return &Service{store: store, stacks: stacks}
}

// Workspace holds acct's stack and makes its workspace name exist, both
// in the context's transaction, so a lock or version the caller writes
// there names a stack whose delete waits for it to commit and then
// deletes them with it. It answers ErrNoTransaction outside a transaction,
// ErrWorkspaceName for a name the table refuses, and stacksapi.ErrNotFound
// when acct lacks the stack, writing nothing.
func (s *Service) Workspace(
	ctx context.Context,
	acct kernel.AccountID,
	stack kernel.ULID,
	name string,
) error {
	if !tx.In(ctx) {
		return ErrNoTransaction
	}
	if len(name) > MaxWorkspaceName || !utf8.ValidString(name) || strings.ContainsRune(name, 0) {
		return fmt.Errorf("%w: %d bytes", ErrWorkspaceName, len(name))
	}
	if err := s.stacks.Hold(ctx, acct, stack); err != nil {
		return err //nolint:wrapcheck // the stacks domain names the hold
	}
	return s.store.OpenWorkspace(ctx, acct, stack, name) //nolint:wrapcheck // the store names it
}
