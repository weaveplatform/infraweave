package core

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// An integration with auto-attachment enabled and labelled Autoattach+l
// attaches to every stack in its reach that carries l, for read and
// write; AutoattachRead+l and AutoattachWrite+l attach it for that side
// alone. AnyStack in place of l names every stack in reach.
//
// ref: report 12 § "2.6 Attachment: read, write and scope"
const (
	Autoattach      = "autoattach:"
	AutoattachRead  = "autoattach_read:"
	AutoattachWrite = "autoattach_write:"
	AnyStack        = "*"
)

// autoNames are the integration labels that name a stack carrying any of
// labels, for any side.
func autoNames(labels []string) []string {
	out := make([]string, 0, 3*(len(labels)+1))
	for _, l := range append(slices.Clone(labels), AnyStack) {
		out = append(out, Autoattach+l, AutoattachRead+l, AutoattachWrite+l)
	}
	return out
}

// autoSides are the sides an integration labelled labels attaches to a
// stack labelled stack for.
func autoSides(labels, stack []string) (read, write bool) {
	for _, l := range labels {
		for prefix, side := range map[string]Side{Autoattach: "", AutoattachRead: Read, AutoattachWrite: Write} {
			name, ok := strings.CutPrefix(l, prefix)
			if !ok || (name != AnyStack && !slices.Contains(stack, name)) {
				continue
			}
			read = read || side != Write
			write = write || side != Read
		}
	}
	return read, write
}

// autoStackLabels are the stack labels an integration's autoattach labels
// name, and whether one of them names every stack.
func autoStackLabels(labels []string) ([]string, bool) {
	var out []string
	for _, l := range labels {
		for _, prefix := range []string{Autoattach, AutoattachRead, AutoattachWrite} {
			if name, ok := strings.CutPrefix(l, prefix); ok {
				if name == AnyStack {
					return nil, true
				}
				out = append(out, name)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out), false
}

// Effective is the attachments that serve st, whose labels are labels,
// in the account's tree: its stored ones, then one computed for each side
// they leave free from the first integration in its reach, by name, whose
// autoattach labels name st for that side and that is not stored as
// attached to it. A stored attachment outranks a computed one, and a
// stack keeps one read and one write attachment either way.
func (s *Service) Effective(ctx context.Context, acct kernel.AccountID, st stacksapi.Stack, labels []string, tree Tree) ([]Attachment, error) {
	stored, err := s.store.Attachments(ctx, acct, []kernel.ULID{st.ID}, true)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	out := stored[st.ID]
	var read, write bool
	attached := map[kernel.ULID]bool{}
	for _, a := range out {
		read, write = read || a.Read, write || a.Write
		attached[a.Integration] = true
	}
	if read && write {
		return out, nil
	}
	candidates, err := s.store.Labelled(ctx, acct, Reach(tree, st.Space), autoNames(labels))
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	for _, i := range candidates {
		if attached[i.ID] {
			continue
		}
		r, w := autoSides(i.Labels, labels)
		r, w = r && !read, w && !write
		if !r && !w {
			continue
		}
		read, write = read || r, write || w
		out = append(out, Attachment{
			Account: acct, Integration: i.ID, Stack: st.ID, Read: r, Write: w, Auto: true,
		})
	}
	return out, nil
}

// Attached are the attachments that serve each of stacks with an
// integration, stored or computed, keyed by stack ID; a stack the account
// lacks is left out.
func (s *Service) Attached(ctx context.Context, acct kernel.AccountID, stacks []kernel.ULID) (map[kernel.ULID][]Attachment, error) {
	found, err := s.stacks.ByIDs(ctx, acct, stacks)
	if err != nil {
		return nil, err //nolint:wrapcheck // the stacks domain names the read
	}
	labels, err := s.stacks.Labels(ctx, acct, slices.Collect(maps.Keys(found)))
	if err != nil {
		return nil, err //nolint:wrapcheck // the stacks domain names the read
	}
	nodes, err := s.spaces.Tree(ctx, acct)
	if err != nil {
		return nil, err //nolint:wrapcheck // the access domain names the read
	}
	tree := index(nodes)
	out := make(map[kernel.ULID][]Attachment, len(found))
	for id, st := range found {
		if out[id], err = s.Effective(ctx, acct, st, labels[id], tree); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// AttachedTo are the attachments of i, stored or computed, oldest stored
// first, then the computed ones by stack ID: a computed one exists where
// i's autoattach labels name a stack in its reach for a side that no
// other attachment of the stack serves first.
func (s *Service) AttachedTo(ctx context.Context, i Integration) ([]Attachment, error) {
	stored, err := s.store.Attachments(ctx, i.Account, []kernel.ULID{i.ID}, false)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	out := stored[i.ID]
	names, all := autoStackLabels(i.Labels)
	if !i.Autoattach || i.Legacy || (!all && len(names) == 0) {
		return out, nil
	}
	nodes, err := s.spaces.Tree(ctx, i.Account)
	if err != nil {
		return nil, err //nolint:wrapcheck // the access domain names the read
	}
	tree := index(nodes)
	spaces := Reached(tree, i.Space)
	var stacks []stacksapi.Stack
	if all {
		stacks, err = s.stacks.InSpaces(ctx, i.Account, spaces)
	} else {
		stacks, err = s.stacks.Find(ctx, i.Account, spaces, names)
	}
	if err != nil {
		return nil, err //nolint:wrapcheck // the stacks domain names the read
	}
	ids := make([]kernel.ULID, len(stacks))
	for n, st := range stacks {
		ids[n] = st.ID
	}
	labels, err := s.stacks.Labels(ctx, i.Account, ids)
	if err != nil {
		return nil, err //nolint:wrapcheck // the stacks domain names the read
	}
	slices.SortFunc(stacks, func(a, b stacksapi.Stack) int { return byID(a.ID, b.ID) })
	for _, st := range stacks {
		effective, err := s.Effective(ctx, i.Account, st, labels[st.ID], tree)
		if err != nil {
			return nil, err
		}
		for _, a := range effective {
			if a.Auto && a.Integration == i.ID {
				out = append(out, a)
			}
		}
	}
	return out, nil
}

// AttachedStack reads p's account's integration id's attachment
// attachment, stored or computed, with the stack it serves; a *Missing
// when the integration has none of that ID.
func (s *Service) AttachedStack(ctx context.Context, p kernel.Principal, id, attachment string) (Attachment, error) {
	i, err := s.Get(ctx, p.AccountID, id)
	if err != nil {
		return Attachment{}, err
	}
	all, err := s.AttachedTo(ctx, i)
	if err != nil {
		return Attachment{}, err
	}
	for _, a := range all {
		if a.APIID() == attachment {
			return a, nil
		}
	}
	return Attachment{}, &Missing{Kind: "attachment", ID: attachment}
}
