package core

import (
	"context"
	"fmt"
	"slices"
	"strings"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Reconcile is keyed by the accepted space version. Distinct changes cannot
// coalesce into an already-running job which read the old committed tree.
var Reconcile = queue.Kind{Name: "policies.reconcile", Queue: queue.Default, Policy: queue.Ignore}

// SpaceChanging validates explicit relations in the proposed tree, and commits
// a durable request for automatic selection with the access-owned update.
func (s *Service) SpaceChanging(ctx context.Context, ch accessapi.SpaceChange) error {
	tree, err := s.deps.Spaces.Tree(ctx, ch.Account)
	if err != nil {
		return fmt.Errorf("policies: read prospective tree: %w", err)
	}
	for i := range tree {
		if tree[i].ID == ch.Space {
			tree[i].Parent, tree[i].Inherit = ch.Parent, ch.Inherit
		}
	}
	all, err := s.deps.Store.AccountAttachments(ctx, ch.Account)
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	var policies, stacks []kernel.ULID
	for _, a := range all {
		if !a.Autoattached {
			policies = append(policies, a.Policy)
			stacks = append(stacks, a.Stack)
		}
	}
	rows, err := s.deps.Store.ByIDs(ctx, ch.Account, policies)
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	targets, err := s.deps.Stacks.ByIDs(ctx, ch.Account, stacks)
	if err != nil {
		return fmt.Errorf("policies: read prospective targets: %w", err)
	}
	var lost []string
	for _, a := range all {
		if !a.Autoattached {
			p, hasPolicy := rows[a.Policy]
			st, hasStack := targets[a.Stack]
			if hasPolicy && hasStack && !reachable(tree, st.Space, p.Space) {
				lost = append(lost, fmt.Sprintf("stack %s would no longer reach policy %s", st.Slug, p.Name))
			}
		}
	}
	if len(lost) > 0 {
		return &kernel.Invalid{Detail: strings.Join(lost, "; ")}
	}
	_, err = s.deps.Queue.EnqueueTx(ctx, Reconcile, struct{}{}, queue.Account(ch.Account.ULID), queue.UniqueKey(fmt.Sprintf("%s/%s/%d", ch.Account, ch.Space, ch.Version)))
	if err != nil {
		return fmt.Errorf("policies: queue space reconciliation: %w", err)
	}
	return nil
}

// ReconcileAccount consumes committed space changes using the current tree.
func (s *Service) ReconcileAccount(ctx context.Context, acct kernel.AccountID) error {
	return tx.Do(ctx, s.deps.DB, tx.Options{}, func(ctx context.Context) error { //nolint:wrapcheck // each step names the failure
		if err := s.deps.Store.LockAutoattach(ctx, acct); err != nil {
			return err //nolint:wrapcheck // the store names the lock
		}
		rows, err := s.deps.Store.AutomaticPolicies(ctx, acct)
		if err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		for _, row := range rows {
			if err := s.reconcilePolicy(ctx, acct, row.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) policyMoving(ctx context.Context, p Policy, space kernel.ULID) error {
	all, err := s.deps.Store.Attachments(ctx, p.Account, []kernel.ULID{p.ID}, false)
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	for _, a := range all[p.ID] {
		if a.Autoattached {
			continue
		}
		if err := s.deps.Stacks.Hold(ctx, p.Account, a.Stack); err != nil {
			return fmt.Errorf("policies: hold policy move target: %w", err)
		}
		st, err := s.deps.Stacks.Get(ctx, p.Account, a.Stack)
		if err != nil {
			return fmt.Errorf("policies: read policy move target: %w", err)
		}
		if err := s.deps.Spaces.Hold(ctx, p.Account, st.Space); err != nil {
			return fmt.Errorf("policies: hold policy move target space: %w", err)
		}
		tree, err := s.deps.Spaces.Tree(ctx, p.Account)
		if err != nil {
			return fmt.Errorf("policies: read policy move reach: %w", err)
		}
		if !reachable(tree, st.Space, space) {
			return &kernel.Invalid{Detail: fmt.Sprintf("Stack %s would no longer reach policy %s.", st.Slug, p.Name)}
		}
	}
	return nil
}

// StackGuard is the stack-owned move/delete back edge.
type StackGuard struct{ s *Service }

// StackGuard returns the service's owned attachment guard.
func (s *Service) StackGuard() stacksapi.StackGuard { return StackGuard{s} }

// LostOnMove refuses an explicit relation outside the target space's reach.
func (g StackGuard) LostOnMove(ctx context.Context, m stacksapi.StackMove) ([]string, error) {
	all, err := g.s.deps.Store.Attachments(ctx, m.Account, []kernel.ULID{m.ID}, true)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	var ids []kernel.ULID
	for _, a := range all[m.ID] {
		if !a.Autoattached {
			ids = append(ids, a.Policy)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := g.s.deps.Store.ByIDs(ctx, m.Account, ids)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	tree, err := g.s.deps.Spaces.Tree(ctx, m.Account)
	if err != nil {
		return nil, fmt.Errorf("policies: read stack move reach: %w", err)
	}
	var lost []string
	for _, row := range rows {
		if !reachable(tree, m.To, row.Space) {
			lost = append(lost, "policy "+row.Name)
		}
	}
	slices.Sort(lost)
	return lost, nil
}

// Deleting removes the policy-owned rows in the stack's transaction.
func (g StackGuard) Deleting(ctx context.Context, st stacksapi.Stack) error {
	return g.s.deps.Store.DeleteStack(ctx, st.Account, st.ID) //nolint:wrapcheck // the store names the deletion
}
