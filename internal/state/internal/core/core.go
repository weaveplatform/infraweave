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

// Store is the state domain's storage port. Each method joins the
// transaction its context carries.
type Store interface {
	// OpenWorkspace makes acct's stack's workspace name exist, leaving one
	// that exists as it is.
	OpenWorkspace(ctx context.Context, acct kernel.AccountID, stack kernel.ULID, name string) error
	// LockWorkspace locks t's workspace's row until the transaction ends,
	// so takers and releasers of its locks go one at a time; false when
	// the workspace does not exist.
	LockWorkspace(ctx context.Context, t Target) (bool, error)
	// Locks are the locks held on t's workspace, oldest first.
	Locks(ctx context.Context, t Target) ([]Lock, error)
	// AddLock records l on t's workspace.
	AddLock(ctx context.Context, t Target, l Lock) error
	// RemoveLocks deletes the locks on t's workspace whose IDs ids holds.
	RemoveLocks(ctx context.Context, t Target, ids []string) error
	// ReleaseRun deletes every lock run holds in acct.
	ReleaseRun(ctx context.Context, acct kernel.AccountID, run kernel.ULID) error
	// Audit records e on its account's trail.
	Audit(ctx context.Context, e kernel.AuditEntry) error
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
	db     tx.Beginner
	store  Store
	stacks Stacks
}

// New returns the service over store, holding stacks through stacks and
// opening its transactions on db.
func New(db tx.Beginner, store Store, stacks Stacks) *Service {
	return &Service{db: db, store: store, stacks: stacks}
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
