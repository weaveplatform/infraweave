package core

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Tree is an account's space tree by space ID.
type Tree map[kernel.ULID]accessapi.SpaceNode

func index(nodes []accessapi.SpaceNode) Tree {
	t := make(Tree, len(nodes))
	for _, n := range nodes {
		t[n.ID] = n
	}
	return t
}

// Reach is the spaces whose integrations a stack in space may attach:
// space, then each ancestor reached through an unbroken chain of spaces
// that inherit their parent's entities, nearest first.
//
// ref: decision 0007 § "Evaluation"
func Reach(t Tree, space kernel.ULID) []kernel.ULID {
	out := []kernel.ULID{space}
	for x := t[space]; x.Inherit && !x.Parent.IsZero(); x = t[x.Parent] {
		out = append(out, x.Parent)
	}
	return out
}

// Reached are the spaces of t whose stacks reach space: space itself and
// each space below it whose chain up to space inherits at every step.
func Reached(t Tree, space kernel.ULID) []kernel.ULID {
	var out []kernel.ULID
	for id := range t {
		if slices.Contains(Reach(t, id), space) {
			out = append(out, id)
		}
	}
	slices.SortFunc(out, byID)
	return out
}

func byID(a, b kernel.ULID) int { return bytes.Compare(a[:], b[:]) }

// SpaceGuard is the domain's say in the access domain's space deletes and
// moves.
type SpaceGuard struct{ s *Service }

// StackGuard is the domain's say in the stacks domain's stack deletes and
// moves.
type StackGuard struct{ s *Service }

var (
	_ accessapi.SpaceGuard = SpaceGuard{}
	_ stacksapi.StackGuard = StackGuard{}
)

// SpaceGuard is the service as an accessapi.SpaceGuard.
func (s *Service) SpaceGuard() SpaceGuard { return SpaceGuard{s} }

// StackGuard is the service as a stacksapi.StackGuard.
func (s *Service) StackGuard() StackGuard { return StackGuard{s} }

// SpaceInUse reports whether an integration lives in space, which refuses
// its deletion.
func (g SpaceGuard) SpaceInUse(ctx context.Context, acct kernel.AccountID, space kernel.ULID) (bool, error) {
	return g.s.store.InSpace(ctx, acct, space) //nolint:wrapcheck // the store names the read
}

// LostOnMove lists each stored attachment m would cut off, as "aws
// integration <name>". A move changes the ancestors of the spaces it moves
// alone, so only a stack in one of them can lose an integration. A legacy
// integration is bound to its stack, not reached through its space.
func (g SpaceGuard) LostOnMove(ctx context.Context, m accessapi.SpaceMove) ([]accessapi.LostReach, error) {
	s := g.s
	ids, err := s.store.AttachedStacks(ctx, m.Account)
	if err != nil || len(ids) == 0 {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	stacks, err := s.stacks.ByIDs(ctx, m.Account, ids)
	if err != nil {
		return nil, err //nolint:wrapcheck // the stacks domain names the read
	}
	moved := append([]kernel.ULID{m.Space}, m.Subtree...)
	var inside []kernel.ULID
	for id, st := range stacks {
		if slices.Contains(moved, st.Space) {
			inside = append(inside, id)
		}
	}
	if len(inside) == 0 {
		return nil, nil
	}
	nodes, err := s.spaces.Tree(ctx, m.Account)
	if err != nil {
		return nil, err //nolint:wrapcheck // the access domain names the read
	}
	before := index(nodes)
	after := maps.Clone(before)
	if n, ok := after[m.Space]; ok && len(m.To) > 0 {
		n.Parent = m.To[0]
		after[m.Space] = n
	}
	var lost []accessapi.LostReach
	err = s.lost(ctx, m.Account, inside, func(st kernel.ULID, space kernel.ULID) bool {
		home := stacks[st].Space
		return slices.Contains(Reach(before, home), space) &&
			!slices.Contains(Reach(after, home), space)
	}, func(st kernel.ULID, i Integration) {
		lost = append(lost, accessapi.LostReach{Stack: stacks[st].Slug, Entity: entity(i)})
	})
	slices.SortFunc(lost, func(a, b accessapi.LostReach) int {
		return cmp.Or(strings.Compare(a.Stack, b.Stack), strings.Compare(a.Entity, b.Entity))
	})
	return lost, err
}

// entity names i as a guard's refusal does.
func entity(i Integration) string { return "aws integration " + i.Name }

// lost calls each with every integration stored as attached to one of
// stacks, legacy ones left out, whose space cut, given the stack and the
// integration's space, reports the stack cut off from.
func (s *Service) lost(
	ctx context.Context,
	acct kernel.AccountID,
	stacks []kernel.ULID,
	cut func(stack, space kernel.ULID) bool,
	each func(stack kernel.ULID, i Integration),
) error {
	attached, err := s.store.Attachments(ctx, acct, stacks, true)
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	var ids []kernel.ULID
	for _, as := range attached {
		for _, a := range as {
			ids = append(ids, a.Integration)
		}
	}
	integrations, err := s.store.ByIDs(ctx, acct, ids)
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	for stack, as := range attached {
		for _, a := range as {
			if i := integrations[a.Integration]; !i.Legacy && cut(stack, i.Space) {
				each(stack, i)
			}
		}
	}
	return nil
}

// LostOnMove names each integration stored as attached to the stack that
// m would leave outside its reach, as "aws integration <name>", by name.
// An automatic attachment follows the move instead.
func (g StackGuard) LostOnMove(ctx context.Context, m stacksapi.StackMove) ([]string, error) {
	s := g.s
	nodes, err := s.spaces.Tree(ctx, m.Account)
	if err != nil {
		return nil, err //nolint:wrapcheck // the access domain names the read
	}
	reach := Reach(index(nodes), m.To)
	var lost []string
	err = s.lost(ctx, m.Account, []kernel.ULID{m.ID}, func(_, space kernel.ULID) bool {
		return !slices.Contains(reach, space)
	}, func(_ kernel.ULID, i Integration) {
		lost = append(lost, entity(i))
	})
	slices.Sort(lost)
	return lost, err
}

// Deleting deletes the stack's attachments and its legacy integration,
// which mean nothing without it.
func (g StackGuard) Deleting(ctx context.Context, st stacksapi.Stack) error {
	return g.s.store.DeleteStack(ctx, st.Account, st.ID) //nolint:wrapcheck // the store names the write
}

// integrationMove refuses moving i to space when a stack i is stored as
// attached to could no longer reach it, naming each stack. i's row is
// locked FOR UPDATE, so no attachment is made meanwhile.
func (s *Service) integrationMove(ctx context.Context, i Integration, space kernel.ULID) error {
	if i.Legacy {
		return nil
	}
	attached, err := s.store.Attachments(ctx, i.Account, []kernel.ULID{i.ID}, false)
	if err != nil || len(attached[i.ID]) == 0 {
		return err //nolint:wrapcheck // the store names the read
	}
	ids := make([]kernel.ULID, 0, len(attached[i.ID]))
	for _, a := range attached[i.ID] {
		ids = append(ids, a.Stack)
	}
	stacks, err := s.stacks.ByIDs(ctx, i.Account, ids)
	if err != nil {
		return err //nolint:wrapcheck // the stacks domain names the read
	}
	nodes, err := s.spaces.Tree(ctx, i.Account)
	if err != nil {
		return err //nolint:wrapcheck // the access domain names the read
	}
	t := index(nodes)
	var lost []string
	for _, st := range stacks {
		if !slices.Contains(Reach(t, st.Space), space) {
			lost = append(lost, st.Slug)
		}
	}
	if len(lost) == 0 {
		return nil
	}
	slices.Sort(lost)
	inv := &kernel.Invalid{}
	reasons := make([]string, 0, len(lost))
	for _, l := range lost {
		reason := fmt.Sprintf("Stack %s would no longer reach AWS integration %s.", l, i.Name)
		reasons = append(reasons, reason)
		inv.Errors = append(inv.Errors, kernel.FieldError{
			Pointer: "/space", Reason: kernel.ReasonRule, Message: reason,
		})
	}
	inv.Detail = "The move is refused: " + strings.Join(reasons, " ")
	return inv
}
