package stacks

import (
	"context"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Store is what the service needs of the stacks' storage.
type Store interface {
	// Get reads acct's stack id; stacksapi.ErrNotFound when acct lacks it.
	Get(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (stacksapi.Stack, error)
	// SpaceInUse reports whether a stack of acct lives in space.
	SpaceInUse(ctx context.Context, acct kernel.AccountID, space kernel.ULID) (bool, error)
}

// Service is the stacks domain's logic.
type Service struct{ store Store }

// New returns the service over store.
func New(store Store) *Service { return &Service{store: store} }

var (
	_ stacksapi.Stacks     = (*Service)(nil)
	_ accessapi.SpaceGuard = (*Service)(nil)
)

// Get reads acct's stack id.
func (s *Service) Get(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) (stacksapi.Stack, error) {
	return s.store.Get(ctx, acct, id) //nolint:wrapcheck // the store names the read
}

// SpaceInUse reports whether a stack lives in space, which refuses its
// deletion.
func (s *Service) SpaceInUse(
	ctx context.Context,
	acct kernel.AccountID,
	space kernel.ULID,
) (bool, error) {
	return s.store.SpaceInUse(ctx, acct, space) //nolint:wrapcheck // as above
}

// LostOnMove refuses no move: a stack keeps its space through a move, and
// nothing it reaches through its space is attached to it yet.
func (*Service) LostOnMove(context.Context, accessapi.SpaceMove) ([]accessapi.LostReach, error) {
	return nil, nil
}
