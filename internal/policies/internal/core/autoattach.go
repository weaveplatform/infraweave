package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

const autoPrefix = "autoattach:"

func automaticLabels(labels []string) (names []string, all bool) {
	for _, l := range labels {
		if name, ok := strings.CutPrefix(l, autoPrefix); ok {
			if name == "*" {
				return nil, true
			}
			if name != "" {
				names = append(names, name)
			}
		}
	}
	return names, false
}

func automaticMatch(policy, target []string) bool {
	names, all := automaticLabels(policy)
	return all || slices.ContainsFunc(names, func(name string) bool { return slices.Contains(target, name) })
}

func reached(tree []accessapi.SpaceNode, source kernel.ULID) []kernel.ULID {
	var ids []kernel.ULID
	for _, node := range tree {
		if reachable(tree, node.ID, source) {
			ids = append(ids, node.ID)
		}
	}
	return ids
}

func byID(a, b kernel.ULID) int { return bytes.Compare(a[:], b[:]) }

// autoattach runs under the account lock and the policy write's transaction.
// Only automatic relations follow labels; an explicit promotion survives them.
func (s *Service) autoattach(ctx context.Context, p Policy) (bool, error) {
	want := map[kernel.ULID]bool{}
	names, all := automaticLabels(p.Labels)
	if attachable(p.Type) && (all || len(names) > 0) {
		tree, err := s.deps.Spaces.Tree(ctx, p.Account)
		if err != nil {
			return false, fmt.Errorf("policies: read automatic reach: %w", err)
		}
		var targets []stacksapi.Stack
		if all {
			targets, err = s.deps.Stacks.InSpaces(ctx, p.Account, reached(tree, p.Space))
		} else {
			targets, err = s.deps.Stacks.Find(ctx, p.Account, reached(tree, p.Space), names)
		}
		if err != nil {
			return false, fmt.Errorf("policies: find automatic targets: %w", err)
		}
		for _, st := range targets {
			want[st.ID] = true
		}
	}
	have, err := s.deps.Store.Attachments(ctx, p.Account, []kernel.ULID{p.ID}, false)
	if err != nil {
		return false, err //nolint:wrapcheck // the store names the read
	}
	changed := false
	for _, a := range have[p.ID] {
		if a.Autoattached && !want[a.Stack] {
			if err := s.deps.Store.Detach(ctx, p.Account, a.ID); err != nil {
				return false, err //nolint:wrapcheck // the store names the write
			}
			changed = true
		}
		delete(want, a.Stack)
	}
	for _, id := range slices.SortedFunc(maps.Keys(want), byID) {
		added, err := s.autoAttach(ctx, p, id)
		if err != nil {
			return false, err
		}
		changed = changed || added
	}
	return changed, nil
}

func (s *Service) autoAttach(ctx context.Context, p Policy, id kernel.ULID) (bool, error) {
	if err := s.deps.Stacks.Hold(ctx, p.Account, id); errors.Is(err, stacksapi.ErrNotFound) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("policies: hold automatic target: %w", err)
	}
	st, err := s.deps.Stacks.Get(ctx, p.Account, id)
	if err != nil {
		return false, fmt.Errorf("policies: read automatic target: %w", err)
	}
	for _, space := range []kernel.ULID{p.Space, st.Space} {
		if err := s.deps.Spaces.Hold(ctx, p.Account, space); err != nil {
			return false, fmt.Errorf("policies: hold automatic space: %w", err)
		}
	}
	tree, err := s.deps.Spaces.Tree(ctx, p.Account)
	if err != nil {
		return false, fmt.Errorf("policies: read held automatic reach: %w", err)
	}
	labels, err := s.deps.Stacks.Labels(ctx, p.Account, []kernel.ULID{id})
	if err != nil {
		return false, fmt.Errorf("policies: read held automatic labels: %w", err)
	}
	if !reachable(tree, st.Space, p.Space) || !automaticMatch(p.Labels, labels[id]) {
		return false, nil
	}
	return s.deps.Store.AutoAttach( //nolint:wrapcheck // the store names the write
		ctx,
		Attachment{Account: p.Account, ID: kernel.NewULID(), Policy: p.ID, Stack: id, Autoattached: true, CreatedAt: s.deps.Now()},
	)
}

// StackChanged re-reads current data, so delayed and repeated outbox deliveries
// converge without restoring attachments from an older label or space version.
func (s *Service) StackChanged(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error {
	return tx.Do(ctx, s.deps.DB, tx.Options{}, func(ctx context.Context) error { //nolint:wrapcheck // every transaction step names its failure
		if err := s.deps.Store.LockAutoattach(ctx, acct); err != nil {
			return err //nolint:wrapcheck // the store names the lock
		}
		if err := s.deps.Stacks.Hold(ctx, acct, id); errors.Is(err, stacksapi.ErrNotFound) {
			return nil
		} else if err != nil {
			return fmt.Errorf("policies: hold changed stack: %w", err)
		}
		st, err := s.deps.Stacks.Get(ctx, acct, id)
		if err != nil {
			return fmt.Errorf("policies: read changed stack: %w", err)
		}
		tree, err := s.deps.Spaces.Tree(ctx, acct)
		if err != nil {
			return fmt.Errorf("policies: changed stack reach: %w", err)
		}
		var sources []kernel.ULID
		for _, n := range tree {
			if reachable(tree, st.Space, n.ID) {
				sources = append(sources, n.ID)
			}
		}
		labels, err := s.deps.Stacks.Labels(ctx, acct, []kernel.ULID{id})
		if err != nil {
			return fmt.Errorf("policies: changed stack labels: %w", err)
		}
		names := []string{autoPrefix + "*"}
		for _, l := range labels[id] {
			names = append(names, autoPrefix+l)
		}
		matched, err := s.deps.Store.Labelled(ctx, acct, sources, names)
		if err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		have, err := s.deps.Store.Attachments(ctx, acct, []kernel.ULID{id}, true)
		if err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		ids := map[kernel.ULID]bool{}
		for _, row := range matched {
			ids[row.ID] = true
		}
		for _, a := range have[id] {
			ids[a.Policy] = true
		}
		for _, policy := range slices.SortedFunc(maps.Keys(ids), byID) {
			if err := s.reconcilePolicy(ctx, acct, policy); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) reconcilePolicy(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error {
	row, err := s.deps.Store.Get(ctx, acct, id, true)
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	changed, err := s.autoattach(ctx, row)
	if err != nil || !changed {
		return err
	}
	row.Version++
	row.UpdatedAt = s.deps.Now()
	if err := s.deps.Store.Update(ctx, row); err != nil {
		return err //nolint:wrapcheck // the store names the write
	}
	return s.changed(ctx, row, policyapi.ChangeUpdated)
}
