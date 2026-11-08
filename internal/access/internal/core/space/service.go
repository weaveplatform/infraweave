package space

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ErrName is a space created without a name.
var ErrName = errors.New("a space needs a name")

// Store keeps the spaces and their closure rows. Every change locks the
// account's tree first, so two changes of one tree never interleave and a
// move can never close a cycle another move opened. A space or account
// that does not exist is the store's not found error.
type Store interface {
	// Spaces lists the account's spaces, top space first.
	Spaces(ctx context.Context, acct kernel.AccountID) ([]Space, error)
	// Space reads the account's space slug.
	Space(ctx context.Context, acct kernel.AccountID, slug string) (Space, error)
	// CreateSpace records s below its parent, with its closure rows.
	CreateSpace(ctx context.Context, s Space) error
	// MoveSpace moves the account's space slug below parentSlug, with
	// its subtree. A parent inside the moved subtree is a *Refusal;
	// otherwise check sees both spaces and the move and may refuse it,
	// before anything is written.
	MoveSpace(
		ctx context.Context,
		acct kernel.AccountID,
		slug, parentSlug string,
		check func(ctx context.Context, moved, parent Space, m accessapi.SpaceMove) error,
	) error
	// DeleteSpace deletes the account's space slug. check sees it first
	// and may refuse; a space with children is a *Refusal.
	DeleteSpace(
		ctx context.Context,
		acct kernel.AccountID,
		slug string,
		check func(ctx context.Context, s Space) error,
	) error
}

// Service keeps each account's tree. Who may change it is decided by
// decision 0007's authorization before a call reaches it.
type Service struct {
	store Store
	now   func() time.Time

	mu     sync.RWMutex
	guards []accessapi.SpaceGuard
}

// NewService returns the service over store.
func NewService(store Store, now func() time.Time) *Service {
	return &Service{store: store, now: now}
}

// Guard registers g, which every later delete and move consults.
func (s *Service) Guard(g accessapi.SpaceGuard) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.guards = append(s.guards, g)
}

func (s *Service) registered() []accessapi.SpaceGuard {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.guards)
}

// Spaces lists acct's spaces, top space first.
func (s *Service) Spaces(ctx context.Context, acct kernel.AccountID) ([]Space, error) {
	return s.store.Spaces(ctx, acct) //nolint:wrapcheck // the store names the read
}

// Create creates a space named name below acct's space parent. Its slug
// is SlugOf the name and a new ULID.
func (s *Service) Create(
	ctx context.Context,
	acct kernel.AccountID,
	parent string,
	in Space,
) (Space, error) {
	if in.Name == "" {
		return Space{}, ErrName
	}
	p, err := s.store.Space(ctx, acct, parent)
	if err != nil {
		return Space{}, err //nolint:wrapcheck // the store names the read
	}
	in.AccountID, in.ID, in.ParentID, in.CreatedAt = acct, kernel.NewULID(), p.ID, s.now()
	in.Slug = SlugOf(in.Name, in.ID)
	if err := s.store.CreateSpace(ctx, in); err != nil {
		return Space{}, err //nolint:wrapcheck // the store names the write
	}
	return in, nil
}

// Move moves acct's space slug, with its subtree, below parent. The top
// space never moves, and each registered guard may refuse.
func (s *Service) Move(ctx context.Context, acct kernel.AccountID, slug, parent string) error {
	guards := s.registered()
	//nolint:wrapcheck // the store names the write
	return s.store.MoveSpace(ctx, acct, slug, parent,
		func(ctx context.Context, moved, _ Space, m accessapi.SpaceMove) error {
			if moved.Top() {
				return refuse("the top space %s cannot move", moved.Slug)
			}
			var r Refusal
			for _, g := range guards {
				lost, err := g.LostOnMove(ctx, m)
				if err != nil {
					return fmt.Errorf("check moving space %s: %w", moved.Slug, err)
				}
				for _, l := range lost {
					r.Reasons = append(r.Reasons,
						fmt.Sprintf("stack %s would no longer reach %s", l.Stack, l.Entity))
				}
			}
			if len(r.Reasons) > 0 {
				return &r
			}
			return nil
		})
}

// Delete deletes acct's space slug. The top space and legacy are never
// deleted, and a space a registered guard says is in use is refused.
func (s *Service) Delete(ctx context.Context, acct kernel.AccountID, slug string) error {
	guards := s.registered()
	//nolint:wrapcheck // the store names the write
	return s.store.DeleteSpace(ctx, acct, slug, func(ctx context.Context, sp Space) error {
		switch {
		case sp.Top():
			return refuse("the top space %s cannot be deleted", sp.Slug)
		case sp.Slug == LegacySlug:
			return refuse("the %s space cannot be deleted", LegacySlug)
		}
		for _, g := range guards {
			used, err := g.SpaceInUse(ctx, acct, sp.ID)
			if err != nil {
				return fmt.Errorf("check deleting space %s: %w", sp.Slug, err)
			}
			if used {
				return refuse("space %s still holds entities", sp.Slug)
			}
		}
		return nil
	})
}
