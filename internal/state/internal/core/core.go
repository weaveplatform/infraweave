package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
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
	// OpenWorkspace makes acct's stack's workspace name exist under a new
	// ID, leaving one that exists as it is.
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
	// BreakLocks records each of locks, released on t's workspace from
	// under its run, by its ID and run.
	BreakLocks(ctx context.Context, t Target, locks []Lock) error
	// Broken reports whether run's lock id on t's workspace was released
	// from under it.
	Broken(ctx context.Context, t Target, id string, run kernel.ULID) (bool, error)
	// ReleaseRun deletes every lock run holds in acct, and the record of
	// each lock released from under it.
	ReleaseRun(ctx context.Context, acct kernel.AccountID, run kernel.ULID) error
	// Audit records e on its account's trail.
	Audit(ctx context.Context, e kernel.AuditEntry) error
	// Current reads the current version of t's workspace; false when the
	// workspace has none or does not exist.
	Current(ctx context.Context, t Target) (Version, bool, error)
	// AddVersion records v in acct and makes it its workspace's current
	// version.
	AddVersion(ctx context.Context, acct kernel.AccountID, v Version) error
	// Purge deletes up to batch of acct's state rows, of stack alone when
	// it is set: versions first, then locks, then workspaces, so no
	// workspace goes before a row that names it. It answers the object
	// keys of the versions it deleted, and true once none was left.
	Purge(ctx context.Context, acct kernel.AccountID, stack *kernel.ULID, batch int) ([]string, bool, error)
	// WorkspaceByID reads acct's workspace id with whether a lock is
	// held on it and by whose run; false when acct lacks it.
	WorkspaceByID(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (Workspace, bool, error)
	// LockStackWorkspaces reads acct's stack's workspaces by name,
	// locking each row FOR UPDATE in that order until the transaction
	// ends.
	LockStackWorkspaces(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) ([]Workspace, error)
	// SearchWorkspaces reads a page of acct's workspaces in scope that in
	// keeps, by Fields; an input they cannot answer is a *search.Invalid.
	SearchWorkspaces(ctx context.Context, acct kernel.AccountID, in search.Input, scope WorkspaceScope) (search.Result[Workspace], error)
	// SuggestWorkspaces lists the values acct's workspaces in scope that
	// in keeps hold, of the fields in names.
	SuggestWorkspaces(ctx context.Context, acct kernel.AccountID, in search.SuggestionsInput, scope WorkspaceScope) (search.Suggestions, error)
}

// Stacks is what the state domain asks of the stacks domain;
// stacksapi.Stacks is one.
type Stacks interface {
	// Hold locks acct's stack id in the context's transaction until it
	// ends, so the stack's delete waits for the caller to commit;
	// stacksapi.ErrNotFound when acct lacks it.
	Hold(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error
	// Get reads acct's stack id; stacksapi.ErrNotFound when acct lacks
	// it.
	Get(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (stacksapi.Stack, error)
	// BySlug reads acct's stack slug; stacksapi.ErrNotFound when acct
	// lacks it.
	BySlug(ctx context.Context, acct kernel.AccountID, slug string) (stacksapi.Stack, error)
	// InSpaces lists every stack of acct in spaces.
	InSpaces(ctx context.Context, acct kernel.AccountID, spaces []kernel.ULID) ([]stacksapi.Stack, error)
}

// Objects are the objects of the states store, as blob_objects records
// them; blobs.Objects is one.
type Objects interface {
	// Put records key's object pending, outside any transaction, and
	// writes size bytes of r under it, sealed, answering the plaintext's
	// size and SHA-256.
	Put(ctx context.Context, store objectstore.Name, key string, r io.Reader, size int64) (objectstore.Object, error)
	// Commit makes key's object live in the transaction on ctx.
	Commit(ctx context.Context, store objectstore.Name, key string) error
	// Open streams key's live object, unsealed.
	Open(ctx context.Context, store objectstore.Name, key string) (io.ReadCloser, error)
	// Delete marks key's object deleting, in the transaction on ctx, so
	// it goes once that commits.
	Delete(ctx context.Context, store objectstore.Name, key string) error
}

// Jobs enqueues a job in the transaction its context carries; a
// queue.Queue is one.
type Jobs interface {
	EnqueueTx(ctx context.Context, k queue.Kind, payload any, opts ...queue.Option) (int64, error)
}

// Deps are what the service takes: DB, which it opens its transactions
// on; Store; Stacks, which holds a stack while its workspace is written
// and names the stacks a workspace search reads; Authz, which names the
// spaces a caller may read; Objects, which hold each version's object;
// and Jobs, which enqueues a deleted stack's purge.
type Deps struct {
	DB      tx.Beginner
	Store   Store
	Stacks  Stacks
	Authz   Authorizer
	Objects Objects
	Jobs    Jobs
}

// Service is the state domain's core.
type Service struct {
	db      tx.Beginner
	store   Store
	stacks  Stacks
	authz   Authorizer
	objects Objects
	jobs    Jobs
}

// New returns the service over d.
func New(d Deps) *Service {
	return &Service{
		db: d.DB, store: d.Store, stacks: d.Stacks, authz: d.Authz, objects: d.Objects,
		jobs: d.Jobs,
	}
}

// Workspace holds acct's stack and makes its workspace name exist, both
// in the context's transaction, so a lock or version the caller writes
// there names a stack whose delete waits for it to commit and then
// deletes them with it. It answers ErrNoTransaction outside a transaction,
// ErrWorkspaceName for a name the table refuses, and stacksapi.ErrNotFound
// when acct lacks the stack, writing nothing.
func (s *Service) Workspace(ctx context.Context, acct kernel.AccountID, stack kernel.ULID, name string) error {
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
