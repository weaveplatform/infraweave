package authz

import (
	"context"
	"fmt"
	"strings"
	"sync"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Store reads what snapshots are built from. An account with no tree is
// the store's not found error.
type Store interface {
	// AuthzData reads acct's authorization rows at one version.
	AuthzData(ctx context.Context, acct kernel.AccountID) (Data, error)
	// AuthzVersions reads each account's authz_version; an account that
	// no longer exists is absent.
	AuthzVersions(ctx context.Context, accts []kernel.AccountID) (map[kernel.AccountID]int64, error)
}

var _ accessapi.Authorizer = (*Service)(nil)

// Service decides from per-account snapshots, each built on first use and
// kept until the account's authz:<account> message drops it or Refresh
// finds its version moved.
type Service struct {
	store Store

	mu    sync.Mutex
	snaps map[kernel.AccountID]*Snapshot
	// drops counts each account's drops, so a load that began before a
	// drop does not keep what it read.
	drops map[kernel.AccountID]uint64
}

// NewService returns the service over store.
func NewService(store Store) *Service {
	return &Service{
		store: store,
		snaps: map[kernel.AccountID]*Snapshot{},
		drops: map[kernel.AccountID]uint64{},
	}
}

// Snapshot is acct's snapshot, built from the store when the service holds
// none.
func (s *Service) Snapshot(ctx context.Context, acct kernel.AccountID) (*Snapshot, error) {
	s.mu.Lock()
	snap, drops := s.snaps[acct], s.drops[acct]
	s.mu.Unlock()
	if snap != nil {
		return snap, nil
	}
	d, err := s.store.AuthzData(ctx, acct)
	if err != nil {
		return nil, fmt.Errorf("authorization of account %s: %w", acct, err)
	}
	snap = Build(d)
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur := s.snaps[acct]; s.drops[acct] == drops && (cur == nil || cur.version < snap.version) {
		s.snaps[acct] = snap
	}
	return snap, nil
}

// whoOf is p as the evaluator sees it.
func whoOf(p kernel.Principal) Who {
	return Who{
		Account: p.AccountID,
		Kind:    p.Kind,
		Actor:   p.ActorID,
		Member:  p.MemberID,
		Teams:   p.Teams,
	}
}

// decide is f of p's account's snapshot, or the zero T, which every check
// reads as refused, for a principal with no account.
func decide[T any](
	ctx context.Context,
	s *Service,
	p kernel.Principal,
	f func(*Snapshot, Who) T,
) (T, error) {
	var zero T
	if p.AccountID.IsZero() {
		return zero, nil
	}
	snap, err := s.Snapshot(ctx, p.AccountID)
	if err != nil {
		return zero, err
	}
	return f(snap, whoOf(p)), nil
}

// Authorize decides whether p may perform action in space.
func (s *Service) Authorize(
	ctx context.Context,
	p kernel.Principal,
	action accessapi.Action,
	space kernel.ULID,
) (bool, error) {
	return decide(ctx, s, p, func(snap *Snapshot, w Who) bool {
		return snap.Authorize(w, action, space)
	})
}

// Reachable is every space of p's account in which p may perform action.
func (s *Service) Reachable(
	ctx context.Context,
	p kernel.Principal,
	action accessapi.Action,
) ([]kernel.ULID, error) {
	return decide(ctx, s, p, func(snap *Snapshot, w Who) []kernel.ULID {
		return snap.Reachable(w, action)
	})
}

// Explain is every grant that reaches space for p.
func (s *Service) Explain(
	ctx context.Context,
	p kernel.Principal,
	space kernel.ULID,
) ([]Grant, error) {
	return decide(ctx, s, p, func(snap *Snapshot, w Who) []Grant {
		return snap.Explain(w, space)
	})
}

// Admin reports whether p is an account admin of its account.
func (s *Service) Admin(ctx context.Context, p kernel.Principal) (bool, error) {
	return decide(ctx, s, p, (*Snapshot).Admin)
}

// Drop forgets acct's snapshot, so the next check rebuilds it.
func (s *Service) Drop(acct kernel.AccountID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.snaps, acct)
	s.drops[acct]++
}

// Flush forgets every snapshot, after messages may have been lost.
func (s *Service) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for acct := range s.snaps {
		s.drops[acct]++
	}
	clear(s.snaps)
}

// Refresh drops each held snapshot whose account's authz_version has
// moved, or whose account is gone, in case its message was lost.
func (s *Service) Refresh(ctx context.Context) error {
	s.mu.Lock()
	held := make(map[kernel.AccountID]int64, len(s.snaps))
	accts := make([]kernel.AccountID, 0, len(s.snaps))
	for acct, snap := range s.snaps {
		held[acct] = snap.version
		accts = append(accts, acct)
	}
	s.mu.Unlock()
	if len(accts) == 0 {
		return nil
	}
	versions, err := s.store.AuthzVersions(ctx, accts)
	if err != nil {
		return fmt.Errorf("refresh authorization: %w", err)
	}
	for acct, v := range held {
		if now, ok := versions[acct]; !ok || now != v {
			s.Drop(acct)
		}
	}
	return nil
}

// keyPrefix starts an account's message on infraweave_invalidate.
const keyPrefix = "authz:"

// Key is the message a change of acct's authorization sends on
// infraweave_invalidate, so every replica rebuilds its snapshot.
func Key(acct kernel.AccountID) string { return keyPrefix + acct.String() }

// ParseKey reads the account a message names, and false for any other
// message.
func ParseKey(payload string) (kernel.AccountID, bool) {
	rest, ok := strings.CutPrefix(payload, keyPrefix)
	if !ok {
		return kernel.AccountID{}, false
	}
	id, err := kernel.ParseULID(rest)
	if err != nil {
		return kernel.AccountID{}, false
	}
	return kernel.AccountID{ULID: id}, true
}
