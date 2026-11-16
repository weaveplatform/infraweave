package core

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
)

// VersionFields are the fields a version search filters, and the
// orderable one orders: when the version was written, the run it names,
// a rollback's copy of its target's among them, and whether it is a
// rollback or an import.
//
// ref: decision 0010 § "Rollback, searches and retention"
var VersionFields = []search.Field{
	{Name: "createdAt", Type: search.Time, Orderable: true},
	{Name: "run", Type: search.String},
	{Name: "rollback", Type: search.Boolean},
	{Name: "imported", Type: search.Boolean},
}

// VersionScope is what a version search sees: the versions of Target's
// workspace, whose stack lives in Space.
type VersionScope struct {
	Target
	Space kernel.ULID
}

// SearchVersions reads a page of the versions of the default workspace
// of p's account's stack slug, by VersionFields, newest first unless the
// input names an order. The route has decided SPACE_READ on the stack's
// space; stacksapi.ErrNotFound when p's account lacks the stack.
//
// ref: decision 0010 § "Rollback, searches and retention"
func (s *Service) SearchVersions(ctx context.Context, p kernel.Principal, slug string, in search.Input) (search.Result[Version], error) {
	st, err := s.stacks.BySlug(ctx, p.AccountID, slug)
	if err != nil {
		return search.Result[Version]{}, err //nolint:wrapcheck // the stacks domain names the read
	}
	scope := VersionScope{Target: Target{Account: st.Account, Stack: st.ID}, Space: st.Space}
	//nolint:wrapcheck // the store names the read; a refusal is answered as it is
	return s.store.SearchVersions(ctx, in, scope)
}

// RolledBack reports which of acct's stacks are rolled back: those whose
// default workspace's current version is a rollback, until a run or an
// import writes a newer one. It is the stacksapi.StateRollbacks the
// stack view reads isStateRollback through.
//
// ref: decision 0010 § "Rollback, searches and retention"
func (s *Service) RolledBack(ctx context.Context, acct kernel.AccountID, stacks []kernel.ULID) (map[kernel.ULID]bool, error) {
	ids, err := s.store.RolledBack(ctx, acct, stacks)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	out := make(map[kernel.ULID]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}
