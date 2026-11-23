package core

import (
	"context"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// UserFields are what a search of a pool's users filters and orders by,
// named as the reference's used-by search names them: whether the user is
// a module, which a stack never is; any of its labels; its name; its slug;
// and its space, its place in the tree. A search that names no order reads
// by slug.
var UserFields = []search.Field{
	{Name: "isModule", Type: search.Boolean, Orderable: true},
	{Name: "label", Type: search.String},
	{Name: "name", Type: search.String, Orderable: true},
	{Name: "slug", Type: search.String, Orderable: true},
	{Name: "space", Type: search.Hierarchy, Orderable: true},
}

// Users names a pool whose users a search reads: a private pool's ID, or
// nil for the public pool, which a stack naming no private pool selects.
type Users struct {
	Pool *kernel.ULID
}

// UserStore reads the stacks that select pools. Each method joins the
// transaction its context carries and reads only acct's stacks in the
// reachable spaces.
type UserStore interface {
	// SearchUsers reads a page of u's stacks that in keeps, by
	// UserFields; an input they cannot answer is a *search.Invalid.
	SearchUsers(ctx context.Context, acct kernel.AccountID, u Users, in search.Input, scope Scope) (search.Result[stacksapi.Stack], error)
	// SuggestUsers lists the values u's stacks that in keeps hold.
	SuggestUsers(ctx context.Context, acct kernel.AccountID, u Users, in search.SuggestionsInput, scope Scope) (search.Suggestions, error)
	// PoolUsers lists the stacks of each private pool of pools, by slug.
	PoolUsers(ctx context.Context, acct kernel.AccountID, pools, reachable []kernel.ULID) (map[kernel.ULID][]workersapi.PoolUser, error)
	// CountPublicUsers counts the stacks that name no private pool.
	CountPublicUsers(ctx context.Context, acct kernel.AccountID, reachable []kernel.ULID) (int, error)
}

// SearchPoolUsers reads a page of the stacks of p's account in the spaces
// it may read that select u's pool.
func (s *Service) SearchPoolUsers(ctx context.Context, p kernel.Principal, u Users, in search.Input) (search.Result[stacksapi.Stack], error) {
	scope, err := s.scope(ctx, p)
	if err != nil {
		return search.Result[stacksapi.Stack]{}, err
	}
	return s.users.SearchUsers(ctx, p.AccountID, u, in, scope) //nolint:wrapcheck // as the store's
}

// SuggestPoolUsers lists the values those stacks hold under in's
// predicates, and counts them.
func (s *Service) SuggestPoolUsers(ctx context.Context, p kernel.Principal, u Users, in search.SuggestionsInput) (search.Suggestions, error) {
	scope, err := s.scope(ctx, p)
	if err != nil {
		return search.Suggestions{}, err
	}
	return s.users.SuggestUsers(ctx, p.AccountID, u, in, scope) //nolint:wrapcheck // as the store's
}

// PoolUsers lists the stacks of p's account in the spaces it may read
// that select each of pools, as workersapi.Users says.
func (s *Service) PoolUsers(ctx context.Context, p kernel.Principal, pools []kernel.ULID) (map[kernel.ULID][]workersapi.PoolUser, error) {
	if len(pools) == 0 {
		return map[kernel.ULID][]workersapi.PoolUser{}, nil
	}
	reach, err := s.authz.Reachable(ctx, p, accessapi.ActionSpaceRead)
	if err != nil {
		return nil, err //nolint:wrapcheck // the snapshot's read names itself
	}
	return s.users.PoolUsers(ctx, p.AccountID, pools, reach) //nolint:wrapcheck // as the store's
}

// PublicUsers counts the stacks of p's account in the spaces it may read
// that select the public pool, as workersapi.Users says.
func (s *Service) PublicUsers(ctx context.Context, p kernel.Principal) (int, error) {
	reach, err := s.authz.Reachable(ctx, p, accessapi.ActionSpaceRead)
	if err != nil {
		return 0, err //nolint:wrapcheck // the snapshot's read names itself
	}
	return s.users.CountPublicUsers(ctx, p.AccountID, reach) //nolint:wrapcheck // as the store's
}

var _ workersapi.Users = (*Service)(nil)
