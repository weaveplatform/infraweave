package policytrigger

import (
	"context"
	"fmt"
	"slices"
	"strings"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

type ownerSnapshot struct {
	Stacks   []stacksapi.Stack
	Settings map[kernel.ULID]stacksapi.Settings
	Tree     []accessapi.SpaceNode
}

func (s *Service) owners(ctx context.Context, account kernel.AccountID) (ownerSnapshot, error) {
	if err := s.d.Store.HoldPolicyTriggers(ctx, account); err != nil {
		return ownerSnapshot{}, fmt.Errorf("runs: fence trigger account: %w", err)
	}
	// Tree mutations first lock the root FOR UPDATE. Its account-owned key
	// freezes inheritance before the inventory's owner locks are acquired.
	if err := s.d.Spaces.Hold(ctx, account, account.ULID); err != nil {
		return ownerSnapshot{}, fmt.Errorf("runs: hold trigger tree: %w", err)
	}
	tree, err := s.d.Spaces.Tree(ctx, account)
	if err != nil {
		return ownerSnapshot{}, fmt.Errorf("runs: read trigger tree: %w", err)
	}
	spaces := make([]kernel.ULID, 0, len(tree))
	for _, node := range tree {
		spaces = append(spaces, node.ID)
	}
	owners, err := s.d.Stacks.InSpaces(ctx, account, spaces)
	if err != nil {
		return ownerSnapshot{}, fmt.Errorf("runs: read trigger inventory: %w", err)
	}
	slices.SortFunc(owners, func(a, b stacksapi.Stack) int { return strings.Compare(a.ID.String(), b.ID.String()) })
	settings := make(map[kernel.ULID]stacksapi.Settings, len(owners))
	for _, owner := range owners {
		if err := s.d.Spaces.Hold(ctx, account, owner.Space); err != nil {
			return ownerSnapshot{}, fmt.Errorf("runs: hold trigger stack space: %w", err)
		}
		st, err := s.d.Stacks.LockForRun(ctx, account, owner.ID)
		if err != nil {
			return ownerSnapshot{}, fmt.Errorf("runs: lock trigger stack: %w", err)
		}
		if st.Space != owner.Space || st.Account != account {
			return ownerSnapshot{}, kernel.ErrUnavailable
		}
		settings[owner.ID] = st
	}
	return ownerSnapshot{Stacks: owners, Settings: settings, Tree: tree}, nil
}
