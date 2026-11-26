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

// SpaceInUse reports whether an AWS or an Azure integration lives in
// space, which refuses its deletion.
func (g SpaceGuard) SpaceInUse(ctx context.Context, acct kernel.AccountID, space kernel.ULID) (bool, error) {
	in, err := g.s.store.InSpace(ctx, acct, space)
	if err != nil || in {
		return in, err //nolint:wrapcheck // the store names the read
	}
	return g.s.azure.InSpace(ctx, acct, space) //nolint:wrapcheck // as above
}

// LostOnMove lists each stored attachment m would cut off, as "aws
// integration <name>" or "azure integration <name>". A move changes the
// ancestors of the spaces it moves alone, so only a stack in one of them
// can lose an integration. A legacy integration is bound to its stack,
// not reached through its space.
func (g SpaceGuard) LostOnMove(ctx context.Context, m accessapi.SpaceMove) ([]accessapi.LostReach, error) {
	s := g.s
	aws, err := s.store.AttachedStacks(ctx, m.Account)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	azure, err := s.azure.AttachedStacks(ctx, m.Account)
	if err != nil || len(aws)+len(azure) == 0 {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	stacks, err := s.stacks.ByIDs(ctx, m.Account, append(aws, azure...))
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
	}, func(st kernel.ULID, entity string) {
		lost = append(lost, accessapi.LostReach{Stack: stacks[st].Slug, Entity: entity})
	})
	slices.SortFunc(lost, func(a, b accessapi.LostReach) int {
		return cmp.Or(strings.Compare(a.Stack, b.Stack), strings.Compare(a.Entity, b.Entity))
	})
	return lost, err
}

// held is an integration an attachment holds, as a guard reads it: its
// space, and the entity a refusal names it as.
type held struct {
	space  kernel.ULID
	entity string
}

// lost calls each with every AWS and Azure integration stored as attached
// to one of stacks, legacy ones left out, whose space cut, given the
// stack and the integration's space, reports the stack cut off from,
// named as a guard's refusal names it.
func (s *Service) lost(
	ctx context.Context,
	acct kernel.AccountID,
	stacks []kernel.ULID,
	cut func(stack, space kernel.ULID) bool,
	each func(stack kernel.ULID, entity string),
) error {
	aws := func(ctx context.Context, ids []kernel.ULID) (map[kernel.ULID]held, error) {
		all, err := s.store.ByIDs(ctx, acct, ids)
		out := map[kernel.ULID]held{}
		for id, i := range all {
			if !i.Legacy {
				out[id] = held{i.Space, "aws integration " + i.Name}
			}
		}
		return out, err //nolint:wrapcheck // the store names the read
	}
	azure := func(ctx context.Context, ids []kernel.ULID) (map[kernel.ULID]held, error) {
		all, err := s.azure.ByIDs(ctx, acct, ids)
		out := map[kernel.ULID]held{}
		for id, i := range all {
			out[id] = held{i.Space, "azure integration " + i.Name}
		}
		return out, err //nolint:wrapcheck // the store names the read
	}
	for _, k := range []struct {
		kind
		held func(ctx context.Context, ids []kernel.ULID) (map[kernel.ULID]held, error)
	}{{s.aws(), aws}, {s.azureKind(), azure}} {
		attached, err := k.stored(ctx, acct, stacks, true)
		if err != nil {
			return err
		}
		var ids []kernel.ULID
		for _, as := range attached {
			for _, a := range as {
				ids = append(ids, a.Integration)
			}
		}
		integrations, err := k.held(ctx, ids)
		if err != nil {
			return err
		}
		for stack, as := range attached {
			for _, a := range as {
				if i, ok := integrations[a.Integration]; ok && cut(stack, i.space) {
					each(stack, i.entity)
				}
			}
		}
	}
	return nil
}

// LostOnMove names each integration stored as attached to the stack that
// m would leave outside its reach, as "aws integration <name>" or "azure
// integration <name>", in order. An automatic attachment follows the
// move instead.
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
	}, func(_ kernel.ULID, entity string) {
		lost = append(lost, entity)
	})
	slices.Sort(lost)
	return lost, err
}

// Deleting deletes the stack's AWS and Azure attachments and its legacy
// AWS integration, which mean nothing without it.
func (g StackGuard) Deleting(ctx context.Context, st stacksapi.Stack) error {
	if err := g.s.store.DeleteStack(ctx, st.Account, st.ID); err != nil {
		return err //nolint:wrapcheck // the store names the write
	}
	return g.s.azure.DeleteStack(ctx, st.Account, st.ID) //nolint:wrapcheck // as above
}

// integrationMove refuses moving i to space when a stack i is stored as
// attached to could no longer reach it, naming each stack. i's row is
// locked FOR UPDATE, so no attachment is made meanwhile.
func (s *Service) integrationMove(ctx context.Context, i Integration, space kernel.ULID) error {
	if i.Legacy {
		return nil
	}
	return s.moveFrom(ctx, s.aws(), i.Account, i.ID, i.Name, space)
}

// moveFrom refuses moving acct's integration id of the kind k, named
// name, to space when a stack it is stored as attached to could no longer
// reach it, naming each stack.
func (s *Service) moveFrom(ctx context.Context, k kind, acct kernel.AccountID, id kernel.ULID, name string, space kernel.ULID) error {
	attached, err := k.stored(ctx, acct, []kernel.ULID{id}, false)
	if err != nil || len(attached[id]) == 0 {
		return err
	}
	ids := make([]kernel.ULID, 0, len(attached[id]))
	for _, a := range attached[id] {
		ids = append(ids, a.Stack)
	}
	stacks, err := s.stacks.ByIDs(ctx, acct, ids)
	if err != nil {
		return err //nolint:wrapcheck // the stacks domain names the read
	}
	nodes, err := s.spaces.Tree(ctx, acct)
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
		reason := fmt.Sprintf("Stack %s would no longer reach %s %s.", l, k.noun, name)
		reasons = append(reasons, reason)
		inv.Errors = append(inv.Errors, kernel.FieldError{
			Pointer: "/space", Reason: kernel.ReasonRule, Message: reason,
		})
	}
	inv.Detail = "The move is refused: " + strings.Join(reasons, " ")
	return inv
}
