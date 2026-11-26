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

// candidate is an integration with auto-attachment enabled as effective
// matches it against a stack: its ID, its space and its labels.
type candidate struct {
	ID, Space kernel.ULID
	Labels    []string
}

// kind is one kind of integration as the attachment rules read it: noun
// names one in a refusal, stored reads the stored attachments of
// integrations, or of stacks when byStack is set, keyed by that ID,
// oldest first, and labelled reads the candidates in spaces that carry
// any of labels, by name. AWS and Azure integrations attach to a stack
// apart, each kind keeping its own read and write attachment.
type kind struct {
	noun     string
	stored   func(ctx context.Context, acct kernel.AccountID, ids []kernel.ULID, byStack bool) (map[kernel.ULID][]Attachment, error)
	labelled func(ctx context.Context, acct kernel.AccountID, spaces []kernel.ULID, labels []string) ([]candidate, error)
}

// aws is AWS integrations as a kind.
func (s *Service) aws() kind {
	return kind{
		noun:   "AWS integration",
		stored: s.store.Attachments,
		labelled: func(ctx context.Context, acct kernel.AccountID, spaces []kernel.ULID, labels []string) ([]candidate, error) {
			all, err := s.store.Labelled(ctx, acct, spaces, labels)
			out := make([]candidate, len(all))
			for n, i := range all {
				out[n] = candidate{ID: i.ID, Space: i.Space, Labels: i.Labels}
			}
			return out, err //nolint:wrapcheck // the store names the read
		},
	}
}

// Effective is the attachments that serve st, whose labels are labels,
// in the account's tree: its stored ones, then one computed for each side
// they leave free from the first integration in its reach, by name, whose
// autoattach labels name st for that side and that is not stored as
// attached to it. A stored attachment outranks a computed one, and a
// stack keeps one read and one write attachment either way.
func (s *Service) Effective(ctx context.Context, acct kernel.AccountID, st stacksapi.Stack, labels []string, tree Tree) ([]Attachment, error) {
	out, err := s.effective(ctx, s.aws(), acct, []stacksapi.Stack{st}, map[kernel.ULID][]string{st.ID: labels}, tree)
	return out[st.ID], err
}

// effective is Effective for each of stacks of the kind k, whose labels
// are keyed by stack ID, keyed by stack ID, from one read of their stored
// attachments and one of the integrations that may attach to any of
// them, so a page of stacks costs two queries however long it is.
func (s *Service) effective(
	ctx context.Context, k kind, acct kernel.AccountID, stacks []stacksapi.Stack, labels map[kernel.ULID][]string, tree Tree,
) (map[kernel.ULID][]Attachment, error) {
	if len(stacks) == 0 {
		return map[kernel.ULID][]Attachment{}, nil
	}
	ids := make([]kernel.ULID, len(stacks))
	for n, st := range stacks {
		ids[n] = st.ID
	}
	stored, err := k.stored(ctx, acct, ids, true)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	// free are the stacks a stored attachment leaves a side free on, with
	// the sides taken and the integrations stored as attached.
	type free struct {
		st          stacksapi.Stack
		reach       []kernel.ULID
		read, write bool
		attached    map[kernel.ULID]bool
	}
	out := make(map[kernel.ULID][]Attachment, len(stacks))
	var open []free
	var spaces []kernel.ULID
	var names []string
	for _, st := range stacks {
		out[st.ID] = stored[st.ID]
		f := free{st: st, attached: map[kernel.ULID]bool{}}
		for _, a := range stored[st.ID] {
			f.read, f.write = f.read || a.Read, f.write || a.Write
			f.attached[a.Integration] = true
		}
		if f.read && f.write {
			continue
		}
		f.reach = Reach(tree, st.Space)
		spaces = append(spaces, f.reach...)
		names = append(names, autoNames(labels[st.ID])...)
		open = append(open, f)
	}
	if len(open) == 0 {
		return out, nil
	}
	slices.SortFunc(spaces, byID)
	slices.Sort(names)
	candidates, err := k.labelled(ctx, acct, slices.Compact(spaces), slices.Compact(names))
	if err != nil {
		return nil, err
	}
	for _, f := range open {
		for _, i := range candidates {
			if f.attached[i.ID] || !slices.Contains(f.reach, i.Space) {
				continue
			}
			r, w := autoSides(i.Labels, labels[f.st.ID])
			r, w = r && !f.read, w && !f.write
			if !r && !w {
				continue
			}
			f.read, f.write = f.read || r, f.write || w
			out[f.st.ID] = append(out[f.st.ID], Attachment{
				Account: acct, Integration: i.ID, Stack: f.st.ID, Read: r, Write: w, Auto: true,
			})
		}
	}
	return out, nil
}

// Attached are the attachments that serve each of stacks with an
// integration, stored or computed, keyed by stack ID; a stack the account
// lacks is left out. It makes the same five reads however many stacks it
// is asked for: the stacks, their labels, the space tree, and effective's
// two.
func (s *Service) Attached(ctx context.Context, acct kernel.AccountID, stacks []kernel.ULID) (map[kernel.ULID][]Attachment, error) {
	_, out, err := s.attached(ctx, s.aws(), acct, stacks)
	return out, err
}

// attached is Attached of the kind k with the stacks it found, keyed by
// ID.
func (s *Service) attached(ctx context.Context, k kind, acct kernel.AccountID, stacks []kernel.ULID) (map[kernel.ULID]stacksapi.Stack, map[kernel.ULID][]Attachment, error) {
	found, err := s.stacks.ByIDs(ctx, acct, stacks)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // the stacks domain names the read
	}
	if len(found) == 0 {
		return found, map[kernel.ULID][]Attachment{}, nil
	}
	all := slices.SortedFunc(maps.Values(found), func(a, b stacksapi.Stack) int { return byID(a.ID, b.ID) })
	ids := make([]kernel.ULID, len(all))
	for n, st := range all {
		ids[n] = st.ID
	}
	labels, err := s.stacks.Labels(ctx, acct, ids)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // the stacks domain names the read
	}
	nodes, err := s.spaces.Tree(ctx, acct)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // the access domain names the read
	}
	out, err := s.effective(ctx, k, acct, all, labels, index(nodes))
	return found, out, err
}

// AttachedTo are the attachments of i, stored or computed, oldest stored
// first, then the computed ones by stack ID: a computed one exists where
// i's autoattach labels name a stack in its reach for a side that no
// other attachment of the stack serves first.
func (s *Service) AttachedTo(ctx context.Context, i Integration) ([]Attachment, error) {
	return s.attachedTo(ctx, s.aws(), i.Account, candidate{ID: i.ID, Space: i.Space, Labels: i.Labels},
		i.Autoattach && !i.Legacy)
}

// attachedTo is AttachedTo of the integration i of the kind k, which
// attaches by its labels when auto is set.
func (s *Service) attachedTo(ctx context.Context, k kind, acct kernel.AccountID, i candidate, auto bool) ([]Attachment, error) {
	stored, err := k.stored(ctx, acct, []kernel.ULID{i.ID}, false)
	if err != nil {
		return nil, err
	}
	out := stored[i.ID]
	names, all := autoStackLabels(i.Labels)
	if !auto || (!all && len(names) == 0) {
		return out, nil
	}
	nodes, err := s.spaces.Tree(ctx, acct)
	if err != nil {
		return nil, err //nolint:wrapcheck // the access domain names the read
	}
	tree := index(nodes)
	spaces := Reached(tree, i.Space)
	var stacks []stacksapi.Stack
	if all {
		stacks, err = s.stacks.InSpaces(ctx, acct, spaces)
	} else {
		stacks, err = s.stacks.Find(ctx, acct, spaces, names)
	}
	if err != nil {
		return nil, err //nolint:wrapcheck // the stacks domain names the read
	}
	ids := make([]kernel.ULID, len(stacks))
	for n, st := range stacks {
		ids[n] = st.ID
	}
	labels, err := s.stacks.Labels(ctx, acct, ids)
	if err != nil {
		return nil, err //nolint:wrapcheck // the stacks domain names the read
	}
	slices.SortFunc(stacks, func(a, b stacksapi.Stack) int { return byID(a.ID, b.ID) })
	effective, err := s.effective(ctx, k, acct, stacks, labels, tree)
	if err != nil {
		return nil, err
	}
	for _, st := range stacks {
		for _, a := range effective[st.ID] {
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
