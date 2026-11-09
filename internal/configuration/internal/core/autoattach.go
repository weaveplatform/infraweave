package core

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"slices"
	"strings"

	configapi "github.com/weaveplatform/infraweave/internal/configuration/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// A context labelled Autoattach+label attaches itself to every stack in
// its reach that carries label, and one labelled Autoattach+AnyStack to
// every stack in its reach.
//
// ref: report 13 §5.4
const (
	Autoattach = "autoattach:"
	AnyStack   = "*"
)

// autoLabels are the stack labels labels' autoattach labels name, and
// whether one of them names every stack.
func autoLabels(labels []string) ([]string, bool) {
	var out []string
	for _, l := range labels {
		if name, ok := strings.CutPrefix(l, Autoattach); ok {
			if name == AnyStack {
				return nil, true
			}
			out = append(out, name)
		}
	}
	return out, false
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

// autoattach brings c's auto-attachments in line with its labels and
// space, in c's transaction: it attaches c to each stack in its reach
// that its autoattach labels name and is not attached to already, and
// detaches each auto-attachment that no longer matches. The caller holds
// LockAutoattach, taken before c's row, so a stack's change waits.
func (s *Service) autoattach(ctx context.Context, c Context) error {
	want := map[kernel.ULID]bool{}
	names, all := autoLabels(c.Labels)
	if all || len(names) > 0 {
		tree, err := s.spaces.Tree(ctx, c.Account)
		if err != nil {
			return err //nolint:wrapcheck // the access domain names the read
		}
		spaces := Reached(index(tree), c.Space)
		var stacks []stacksapi.Stack
		if all {
			stacks, err = s.stacks.InSpaces(ctx, c.Account, spaces)
		} else {
			stacks, err = s.stacks.Find(ctx, c.Account, spaces, names)
		}
		if err != nil {
			return err //nolint:wrapcheck // the stacks domain names the read
		}
		for _, st := range stacks {
			want[st.ID] = true
		}
	}
	have, err := s.store.Attachments(ctx, c.Account, []kernel.ULID{c.ID}, false)
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	for _, a := range have[c.ID] {
		if a.Autoattached && !want[a.Stack] {
			if err := s.store.Detach(ctx, c.Account, a.ID); err != nil {
				return err //nolint:wrapcheck // the store names the write
			}
		}
		delete(want, a.Stack)
	}
	for _, stack := range slices.SortedFunc(maps.Keys(want), byID) {
		if _, err := s.autoAttach(ctx, c, stack); err != nil {
			return err
		}
	}
	return nil
}

// autoAttach attaches c to stack automatically, holding the stack, and
// reports whether it did: a stack deleted meanwhile is skipped.
func (s *Service) autoAttach(ctx context.Context, c Context, stack kernel.ULID) (bool, error) {
	if err := s.stacks.Hold(ctx, c.Account, stack); errors.Is(err, stacksapi.ErrNotFound) {
		return false, nil
	} else if err != nil {
		return false, err //nolint:wrapcheck // the stacks domain names the lock
	}
	ok, err := s.store.AutoAttach(ctx, Attachment{
		ID: kernel.NewULID(), Account: c.Account, Context: c.ID, Stack: stack, Autoattached: true,
		CreatedAt: s.now(),
	})
	return ok, err //nolint:wrapcheck // the store names the write
}

// StackChanged brings the auto-attachments of acct's stack id in line with
// its labels and space: it attaches each context in the stack's reach
// whose autoattach labels name it and detaches each that no longer does,
// each a change of that context. A deleted stack has none left to match.
func (s *Service) StackChanged(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error {
	//nolint:wrapcheck // each step in the transaction names itself
	return tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		if err := s.store.LockAutoattach(ctx, acct); err != nil {
			return err //nolint:wrapcheck // the store names the lock
		}
		st, err := s.stacks.Get(ctx, acct, id)
		if errors.Is(err, stacksapi.ErrNotFound) {
			return nil
		} else if err != nil {
			return err //nolint:wrapcheck // the stacks domain names the read
		}
		labels, err := s.stacks.Labels(ctx, acct, []kernel.ULID{id})
		if err != nil {
			return err //nolint:wrapcheck // the stacks domain names the read
		}
		tree, err := s.spaces.Tree(ctx, acct)
		if err != nil {
			return err //nolint:wrapcheck // the access domain names the read
		}
		names := []string{Autoattach + AnyStack}
		for _, l := range labels[id] {
			names = append(names, Autoattach+l)
		}
		matched, err := s.store.Labelled(ctx, acct, Reach(index(tree), st.Space), names)
		if err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		have, err := s.store.Attachments(ctx, acct, []kernel.ULID{id}, true)
		if err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		attached := map[kernel.ULID]Attachment{}
		for _, a := range have[id] {
			attached[a.Context] = a
		}
		for _, c := range matched {
			if _, ok := attached[c.ID]; ok {
				delete(attached, c.ID)
				continue
			}
			if err := s.restack(
				ctx,
				c,
				configapi.ChangeAttached,
				id,
				func(ctx context.Context, c Context) (bool, error) {
					return s.autoAttach(ctx, c, id)
				},
			); err != nil {
				return err
			}
		}
		for _, a := range attached {
			if !a.Autoattached {
				continue
			}
			c, err := s.store.ByIDs(ctx, acct, []kernel.ULID{a.Context})
			if err != nil {
				return err //nolint:wrapcheck // the store names the read
			}
			if err := s.restack(
				ctx,
				c[a.Context],
				configapi.ChangeDetached,
				id,
				func(ctx context.Context, c Context) (bool, error) {
					return true, s.store.Detach(ctx, acct, a.ID)
				},
			); err != nil {
				return err
			}
		}
		return nil
	})
}

// restack applies change to c, locked, as a change of what, naming stack,
// when change reports it changed something.
func (s *Service) restack(
	ctx context.Context,
	c Context,
	what string,
	stack kernel.ULID,
	change func(ctx context.Context, c Context) (bool, error),
) error {
	var changed bool
	_, err := s.write(ctx, c.Account, c.Slug, kernel.Expect{}, what, &stack,
		func(ctx context.Context, c *Context) error {
			var err error
			changed, err = change(ctx, *c)
			if err == nil && !changed {
				err = errUnchanged
			}
			return err
		})
	if errors.Is(err, errUnchanged) || errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// errUnchanged ends a write whose change changed nothing before it spends
// a version and an event; nothing has been written when it is returned.
var errUnchanged = errors.New("configuration: unchanged")
