package core

import (
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

// Reach is the spaces whose contexts a stack in space may attach: space,
// then each ancestor reached through an unbroken chain of spaces that
// inherit their parent's entities, nearest first.
//
// ref: decision 0007 § "Evaluation"
func Reach(t Tree, space kernel.ULID) []kernel.ULID {
	out := []kernel.ULID{space}
	for x := t[space]; x.Inherit && !x.Parent.IsZero(); x = t[x.Parent] {
		out = append(out, x.Parent)
	}
	return out
}

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

// SpaceInUse reports whether a context lives in space, which refuses its
// deletion.
func (g SpaceGuard) SpaceInUse(
	ctx context.Context,
	acct kernel.AccountID,
	space kernel.ULID,
) (bool, error) {
	return g.s.store.InSpace(ctx, acct, space) //nolint:wrapcheck // the store names the read
}

// LostOnMove lists each attachment m would cut off. A move changes the
// ancestors of the spaces it moves alone, so only a stack in one of them
// can lose a context, and only one in an ancestor it reaches today.
func (g SpaceGuard) LostOnMove(
	ctx context.Context,
	m accessapi.SpaceMove,
) ([]accessapi.LostReach, error) {
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
	attached, err := s.store.Attachments(ctx, m.Account, inside, true)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	var lost []accessapi.LostReach
	err = s.lost(ctx, m.Account, attached, func(stack kernel.ULID, space kernel.ULID) bool {
		st := stacks[stack]
		return slices.Contains(Reach(before, st.Space), space) &&
			!slices.Contains(Reach(after, st.Space), space)
	}, func(stack kernel.ULID, c Context) {
		lost = append(
			lost,
			accessapi.LostReach{Stack: stacks[stack].Slug, Entity: "context " + c.Slug},
		)
	})
	slices.SortFunc(lost, func(a, b accessapi.LostReach) int {
		return cmp.Or(strings.Compare(a.Stack, b.Stack), strings.Compare(a.Entity, b.Entity))
	})
	return lost, err
}

// lost calls each with every attached context whose space cut, given the
// stack and the context's space, reports the stack cut off from.
func (s *Service) lost(
	ctx context.Context,
	acct kernel.AccountID,
	attached map[kernel.ULID][]Attachment,
	cut func(stack, space kernel.ULID) bool,
	each func(stack kernel.ULID, c Context),
) error {
	var ids []kernel.ULID
	for _, as := range attached {
		for _, a := range as {
			ids = append(ids, a.Context)
		}
	}
	contexts, err := s.store.ByIDs(ctx, acct, ids)
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	for stack, as := range attached {
		for _, a := range as {
			if c := contexts[a.Context]; cut(stack, c.Space) {
				each(stack, c)
			}
		}
	}
	return nil
}

// LostOnMove names each context attached to the stack by hand that m
// would leave outside its reach, as "context <slug>", by slug. An
// automatic attachment follows the move instead, once its event is
// processed.
func (g StackGuard) LostOnMove(ctx context.Context, m stacksapi.StackMove) ([]string, error) {
	s := g.s
	attached, err := s.store.Attachments(ctx, m.Account, []kernel.ULID{m.ID}, true)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	attached[m.ID] = manual(attached[m.ID])
	if len(attached[m.ID]) == 0 {
		return nil, nil
	}
	nodes, err := s.spaces.Tree(ctx, m.Account)
	if err != nil {
		return nil, err //nolint:wrapcheck // the access domain names the read
	}
	reach := Reach(index(nodes), m.To)
	var lost []string
	err = s.lost(ctx, m.Account, attached, func(_, space kernel.ULID) bool {
		return !slices.Contains(reach, space)
	}, func(_ kernel.ULID, c Context) {
		lost = append(lost, "context "+c.Slug)
	})
	slices.Sort(lost)
	return lost, err
}

// Deleting deletes the stack's attachments and config elements, which
// mean nothing without it.
func (g StackGuard) Deleting(ctx context.Context, st stacksapi.Stack) error {
	err := g.s.store.DeleteStack(ctx, st.Account, st.ID)
	return err //nolint:wrapcheck // the store names the write
}

// contextMove refuses moving c to space when a stack attached to it by
// hand could no longer reach it, naming each stack; the move recomputes
// its automatic attachments instead. c's row is locked FOR UPDATE, so no
// attachment is made meanwhile.
func (s *Service) contextMove(ctx context.Context, c Context, space kernel.ULID) error {
	attached, err := s.store.Attachments(ctx, c.Account, []kernel.ULID{c.ID}, false)
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	as := manual(attached[c.ID])
	if len(as) == 0 {
		return nil
	}
	ids := make([]kernel.ULID, 0, len(as))
	for _, a := range as {
		ids = append(ids, a.Stack)
	}
	stacks, err := s.stacks.ByIDs(ctx, c.Account, ids)
	if err != nil {
		return err //nolint:wrapcheck // the stacks domain names the read
	}
	nodes, err := s.spaces.Tree(ctx, c.Account)
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
	inv := &kernel.Invalid{Detail: fmt.Sprintf(
		"The move is refused: stacks %s would no longer reach context %s.",
		strings.Join(lost, ", "), c.Slug)}
	for _, l := range lost {
		inv.Errors = append(inv.Errors, kernel.FieldError{
			Pointer: "/input/space", Reason: kernel.ReasonRule,
			Message: fmt.Sprintf("Stack %s would no longer reach context %s.", l, c.Slug),
		})
	}
	return inv
}

// manual are those of as attached by hand.
func manual(as []Attachment) []Attachment {
	return slices.DeleteFunc(slices.Clone(as), func(a Attachment) bool { return a.Autoattached })
}

// AttachedContexts reads the contexts attached to each of stacks, with
// their hooks and elements: those attached by hand by priority, lowest
// first, then by slug, and then those attached automatically, by name; a
// write-only element's value is left out.
func (s *Service) AttachedContexts(
	ctx context.Context,
	acct kernel.AccountID,
	stacks []kernel.ULID,
) (map[kernel.ULID][]stacksapi.AttachedContext, error) {
	out := map[kernel.ULID][]stacksapi.AttachedContext{}
	if len(stacks) == 0 {
		return out, nil
	}
	attached, err := s.store.Attachments(ctx, acct, stacks, true)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	var ids []kernel.ULID
	for _, as := range attached {
		for _, a := range as {
			ids = append(ids, a.Context)
		}
	}
	contexts, err := s.store.ByIDs(ctx, acct, ids)
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	owners := make([]Owner, 0, len(contexts))
	for id := range contexts {
		owners = append(owners, Owner{Context: id})
	}
	elements, err := s.store.Elements(ctx, acct, owners)
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	for stack, as := range attached {
		for _, a := range as {
			c := contexts[a.Context]
			out[stack] = append(out[stack], stacksapi.AttachedContext{
				ID: a.ID, Context: c.Slug, Name: c.Name, Description: c.Description,
				Labels: c.Labels, Hooks: c.Hooks, Config: ConfigOf(elements[c.ID]),
				Priority: a.Priority, Autoattached: a.Autoattached,
			})
		}
		slices.SortStableFunc(out[stack], func(a, b stacksapi.AttachedContext) int {
			switch {
			case a.Autoattached && !b.Autoattached:
				return 1
			case !a.Autoattached && b.Autoattached:
				return -1
			case a.Autoattached:
				return cmp.Or(
					strings.Compare(a.Name, b.Name),
					strings.Compare(a.Context, b.Context),
				)
			}
			return cmp.Or(
				cmp.Compare(a.Priority, b.Priority),
				strings.Compare(a.Context, b.Context),
			)
		})
	}
	return out, nil
}

// Config reads each of stacks' own config elements, by ID.
func (s *Service) Config(
	ctx context.Context,
	acct kernel.AccountID,
	stacks []kernel.ULID,
) (map[kernel.ULID][]stacksapi.ConfigElement, error) {
	out := map[kernel.ULID][]stacksapi.ConfigElement{}
	if len(stacks) == 0 {
		return out, nil
	}
	owners := make([]Owner, len(stacks))
	for i, id := range stacks {
		owners[i] = Owner{Stack: id}
	}
	elements, err := s.store.Elements(ctx, acct, owners)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	for id, es := range elements {
		out[id] = ConfigOf(es)
	}
	return out, nil
}

// ConfigOf is es as a view shows them, a write-only value left out.
func ConfigOf(es []Element) []stacksapi.ConfigElement {
	out := make([]stacksapi.ConfigElement, len(es))
	for i, e := range es {
		out[i] = stacksapi.ConfigElement{
			ID: e.Name, FileMount: e.FileMount, WriteOnly: e.WriteOnly, Checksum: e.Checksum,
			Description: e.Description, FileMode: e.FileMode, CreatedAt: e.CreatedAt,
		}
		if !e.WriteOnly {
			out[i].Value = e.Value
		}
	}
	return out
}
