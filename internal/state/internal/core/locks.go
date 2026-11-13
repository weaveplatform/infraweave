package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// Mode is a lock's mode, as state_locks.mode holds it.
type Mode string

const (
	// Shared is the lock of a run that reads the state, which other
	// shared holders hold too.
	Shared Mode = "shared"
	// Exclusive is the lock of a run that writes the state, which no
	// other lock is held beside.
	Exclusive Mode = "exclusive"
)

// MaxLockInfo is the largest LockInfo in bytes; it matches the CHECK on
// state_locks.info in the pg adapter's migration.
const MaxLockInfo = 16 << 10

// Lock is a lock held on a workspace: ID is its LockInfo's ID, Run the
// run that took it, and Info the LockInfo as the client sent it, which a
// conflict answers.
type Lock struct {
	ID        string
	Mode      Mode
	Run       kernel.ULID
	Info      json.RawMessage
	CreatedAt time.Time
}

// Conflict is a lock that cannot be taken or released for Holder, whose
// LockInfo the backend answers with 423.
//
// ref: decision 0010 § "Locks"
type Conflict struct{ Holder Lock }

func (c *Conflict) Error() string {
	return fmt.Sprintf("state: the workspace is locked by %s for run %s", c.Holder.ID,
		c.Holder.Run)
}

var (
	// ErrNoWorkspace reports an unlock of a workspace no lock was ever
	// taken on.
	ErrNoWorkspace = errors.New("state: no such workspace")
	// ErrBreak reports a release of another run's lock, or of every lock,
	// by a caller not granted the exclusive lock.
	ErrBreak = errors.New("state: only a run that may write the state releases another's lock")
)

// Target is the workspace a request names, of a stack in an account.
type Target struct {
	Account   kernel.AccountID
	Stack     kernel.ULID
	Workspace string
}

// Lock takes want on t's workspace for want.Run, making the workspace
// exist under its stack's hold. It answers a *Conflict naming the lock
// that refuses it: an exclusive lock refuses every other, and a shared
// lock refuses an exclusive one. A lock the run already holds with the
// same ID and mode is granted again.
//
// ref: decision 0010 § "Locks"
func (s *Service) Lock(ctx context.Context, t Target, want Lock) error {
	//nolint:wrapcheck // each step in the transaction names itself
	return tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		if err := s.Workspace(ctx, t.Account, t.Stack, t.Workspace); err != nil {
			return err
		}
		if _, err := s.store.LockWorkspace(ctx, t); err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		held, err := s.store.Locks(ctx, t)
		if err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		holder, again := decide(held, want)
		switch {
		case holder != nil:
			return &Conflict{Holder: *holder}
		case again:
			return nil
		}
		return s.store.AddLock(ctx, t, want) //nolint:wrapcheck // the store names the write
	})
}

// Unlock is a release of a lock on a workspace: ID is the LockInfo's ID,
// empty for a force-unlock, which releases every lock held there. Break
// is set for a caller granted the exclusive lock, which alone may release
// another run's lock; RemoteIP is the caller's address, which an audit
// entry records.
type Unlock struct {
	Target
	Run      kernel.ULID
	ID       string
	Break    bool
	RemoteIP string
}

// Unlock releases u's lock, or with an empty ID every lock on its
// workspace. It answers ErrNoWorkspace for a workspace no lock was taken
// on; a *Conflict naming the holder when another lock is held and none
// has u's ID; and ErrBreak, releasing nothing, when u would release
// another run's lock without Break. Releasing another run's lock records
// an audit entry naming the caller, the workspace and each LockInfo
// released.
//
// ref: decision 0010 § "Locks"
func (s *Service) Unlock(ctx context.Context, u Unlock) error {
	//nolint:wrapcheck // each step in the transaction names itself
	return tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		ok, err := s.store.LockWorkspace(ctx, u.Target)
		switch {
		case err != nil:
			return err //nolint:wrapcheck // the store names the read
		case !ok:
			return ErrNoWorkspace
		}
		held, err := s.store.Locks(ctx, u.Target)
		if err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		release := held
		if u.ID != "" {
			release = nil
			for _, l := range held {
				if l.ID == u.ID {
					release = []Lock{l}
				}
			}
			if release == nil && len(held) > 0 {
				return &Conflict{Holder: holder(held)}
			}
		}
		ids, others := make([]string, 0, len(release)), make([]Lock, 0, len(release))
		for _, l := range release {
			ids = append(ids, l.ID)
			if l.Run != u.Run {
				others = append(others, l)
			}
		}
		if (u.ID == "" || len(others) > 0) && !u.Break {
			return ErrBreak
		}
		if len(ids) == 0 {
			return nil
		}
		if err := s.store.RemoveLocks(ctx, u.Target, ids); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		if len(others) == 0 {
			return nil
		}
		return s.store.Audit(ctx, broken(u, others)) //nolint:wrapcheck // the store names it
	})
}

// RunEnded releases every lock run holds in acct once it reaches state,
// when state is terminal: a run's transition writes only the runs
// domain's tables, so its locks go when its terminal run_updated arrives.
//
// ref: decision 0010 § "Locks"
func (s *Service) RunEnded(
	ctx context.Context,
	acct kernel.AccountID,
	run kernel.ULID,
	state kernel.RunState,
) error {
	if !state.Terminal() {
		return nil
	}
	return s.store.ReleaseRun(ctx, acct, run) //nolint:wrapcheck // the store names the write
}

// decide is the lock of held that refuses want, or whether want's run
// already holds it with the same ID and mode. A lock of the same ID that
// differs refuses it, so no run takes over another's lock by its ID.
func decide(held []Lock, want Lock) (*Lock, bool) {
	for _, l := range held {
		if l.ID == want.ID {
			if l.Mode == want.Mode && l.Run == want.Run {
				return nil, true
			}
			return &l, false
		}
	}
	if len(held) == 0 {
		return nil, false
	}
	if h := holder(held); h.Mode == Exclusive || want.Mode == Exclusive {
		return &h, false
	}
	return nil, false
}

// holder is the lock a conflict over held names: the exclusive lock when
// one is held, which no other lock is held beside, else the oldest.
func holder(held []Lock) Lock {
	for _, l := range held {
		if l.Mode == Exclusive {
			return l
		}
	}
	return held[0]
}

// Audit names of a broken lock's entry.
const (
	ActionBreakLock = "stack.state_lock_break"
	ResourceStack   = "STACK"
	ResourceRun     = "RUN"
	// Actor is the user name of an entry a run's call records; the entry's
	// Actor.Run names the run.
	Actor = "infraweave"
)

// broken is the audit entry of u releasing others, locks of other runs.
func broken(u Unlock, others []Lock) kernel.AuditEntry {
	released := make([]json.RawMessage, 0, len(others))
	for _, l := range others {
		released = append(released, l.Info)
	}
	// Each LockInfo was checked as JSON before it was stored, so the
	// metadata always encodes.
	meta, _ := json.Marshal(struct {
		Workspace string            `json:"workspace"`
		Released  []json.RawMessage `json:"released"`
	}{u.Workspace, released})
	return kernel.AuditEntry{
		AccountID: u.Account.ULID,
		Actor:     kernel.AuditActor{Username: Actor, Run: u.Run},
		Action:    ActionBreakLock,
		EventType: "DELETE",
		AffectedResource: kernel.AuditResource{
			Type: ResourceStack, ID: u.Stack.String(), Metadata: meta,
		},
		RelatedResource: &kernel.AuditResource{Type: ResourceRun, ID: u.Run.String()},
		RemoteIP:        u.RemoteIP,
	}
}
