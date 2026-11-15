package trigger

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// MaxBatch bounds the stacks of one batch: runTriggerBatch's stacks have
// it as their maxItems.
//
// ref: decision 0003 § "Bounds in the domains"
const MaxBatch = 50

// BatchStack is one stack of a batch: its slug, and the commit SHA its
// run works on, the stack's tracked commit, else its branch head, when
// empty.
type BatchStack struct {
	Stack     string
	CommitSHA string
}

// entry is a batch's stack as Batch checks it: the stack, its settings
// under its row's lock, and the commit its run works on.
type entry struct {
	stack    stacksapi.Stack
	settings stacksapi.Settings
	commit   stacksapi.Commit
}

// Batch creates p's TRACKED run, with forceApply, on each stack of
// stacks in one transaction, and answers the runs in stacks' order; a
// stack's run of the same commit that has not ended is answered in place
// of a new one. A named commit becomes its stack's tracked commit, set by
// p, as stackSetCurrentCommit sets it. The stacks' rows are locked in ID
// order, so two batches sharing stacks wait on each other rather than
// deadlock.
//
// Every entry the rules refuse is refused together, as one
// *kernel.Invalid, at /stacks/<i>/stack: a stack p cannot read, as one
// the account lacks; one whose space p may not trigger runs in; one named
// twice; a disabled stack, a deleting one, one another caller locked, and
// one with no commit; and at /stacks/<i>/commitSha a commit the stack's
// repository lacks. No stacks, or more than MaxBatch, is refused at /stacks.
//
// ref: decision 0008 § "Who drives each transition"
// ref: decision 0022
func (s *Service) Batch(
	ctx context.Context,
	p kernel.Principal,
	stacks []BatchStack,
	forceApply string,
) ([]read.View, error) {
	if len(stacks) == 0 || len(stacks) > MaxBatch {
		reason := kernel.ReasonMaxLength
		if len(stacks) == 0 {
			reason = kernel.ReasonMinLength
		}
		return nil, &kernel.Invalid{
			Detail: "The batch is refused.",
			Errors: []kernel.FieldError{{
				Pointer: "/stacks", Reason: reason,
				Message: fmt.Sprintf("A batch names 1 to %d stacks.", MaxBatch),
			}},
		}
	}
	var views []read.View
	err := tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		entries, errs, err := s.entries(ctx, p, stacks)
		if err != nil {
			return err
		}
		if len(errs) > 0 {
			return &kernel.Invalid{Detail: "The batch is refused.", Errors: errs}
		}
		views = make([]read.View, len(entries))
		for i, e := range entries {
			if stacks[i].CommitSHA != "" {
				if err := s.d.Stacks.SetTracked(ctx, p.AccountID, e.stack.ID, nil, &e.commit,
					p.Login); err != nil {
					return err //nolint:wrapcheck // the stacks domain names the write
				}
			}
			commit := e.commit
			commit.Branch = e.settings.Branch
			run, _, err := s.d.Lifecycle.Create(ctx, lifecycle.NewRun{
				Account: p.AccountID, Stack: e.stack.ID, Type: kernel.RunTypeTracked,
				Commit: commit, Actor: lifecycle.ActorOf(p), ForceApply: forceApply,
			})
			if err != nil {
				return err //nolint:wrapcheck // the lifecycle names the refusal
			}
			if views[i], err = s.d.Views.One(ctx, p, run); err != nil {
				return err //nolint:wrapcheck // the read names itself
			}
		}
		return nil
	})
	return views, err //nolint:wrapcheck // each step names itself
}

// entries reads and checks each of stacks for p in the transaction ctx
// carries, locking the stacks' rows in ID order, and answers each
// entry's refusals beside the entries.
func (s *Service) entries(
	ctx context.Context,
	p kernel.Principal,
	stacks []BatchStack,
) ([]entry, []kernel.FieldError, error) {
	entries := make([]entry, len(stacks))
	var errs []kernel.FieldError
	seen := make(map[string]bool, len(stacks))
	for i, b := range stacks {
		at := fmt.Sprintf("/stacks/%d/stack", i)
		if seen[b.Stack] {
			errs = append(errs, kernel.FieldError{
				Pointer: at, Reason: kernel.ReasonDuplicate,
				Message: fmt.Sprintf("The batch names stack %q more than once.", b.Stack),
			})
			continue
		}
		seen[b.Stack] = true
		refused, err := s.triggerable(ctx, p, b.Stack, at, &entries[i].stack)
		if err != nil {
			return nil, nil, err
		}
		errs = append(errs, refused...)
	}
	if len(errs) > 0 {
		return nil, errs, nil
	}
	order := make([]int, len(entries))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(a, b int) int {
		return cmp.Compare(entries[a].stack.ID.String(), entries[b].stack.ID.String())
	})
	for _, i := range order {
		var err error
		if entries[i].settings, err = s.d.Stacks.LockForRun(ctx, p.AccountID,
			entries[i].stack.ID); err != nil {
			return nil, nil, err //nolint:wrapcheck // the stacks domain names the read
		}
	}
	for i := range entries {
		refused, err := s.runnable(ctx, p, &entries[i], stacks[i].CommitSHA, i)
		if err != nil {
			return nil, nil, err
		}
		errs = append(errs, refused...)
	}
	return entries, errs, nil
}

// triggerable reads p's stack slug into st, answering the refusal at at
// of a stack p cannot read, as one the account lacks, and of one whose
// space p may not trigger runs in.
func (s *Service) triggerable(
	ctx context.Context,
	p kernel.Principal,
	slug, at string,
	st *stacksapi.Stack,
) ([]kernel.FieldError, error) {
	var err error
	*st, err = s.d.Stacks.BySlug(ctx, p.AccountID, slug)
	if err != nil && !errors.Is(err, stacksapi.ErrNotFound) {
		return nil, err //nolint:wrapcheck // the stacks domain names the read
	}
	read := err == nil
	if read {
		if read, err = s.d.Authz.Authorize(
			ctx,
			p,
			accessapi.ActionSpaceRead,
			st.Space,
		); err != nil {
			return nil, err //nolint:wrapcheck // the access domain names the decision
		}
	}
	if !read {
		return []kernel.FieldError{{
			Pointer: at, Reason: kernel.ReasonNotFound,
			Message: fmt.Sprintf("The account has no stack %q.", slug),
		}}, nil
	}
	ok, err := s.d.Authz.Authorize(ctx, p, accessapi.ActionRunTrigger, st.Space)
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	if !ok {
		return []kernel.FieldError{{
			Pointer: at, Reason: kernel.ReasonRule,
			Message: fmt.Sprintf("Triggering a run on stack %q needs RUN_TRIGGER in its space.",
				slug),
		}}, nil
	}
	return nil, nil
}

// runnable answers the refusals of e, the batch's entry i, from its
// locked settings: a disabled stack, a deleting one, one another caller
// locked, and one with no commit, at its stack, and a commit sha its repository lacks, at
// its commitSha. It sets e's commit.
func (s *Service) runnable(
	ctx context.Context,
	p kernel.Principal,
	e *entry,
	sha string,
	i int,
) ([]kernel.FieldError, error) {
	at := fmt.Sprintf("/stacks/%d/stack", i)
	refuse := func(format string, args ...any) []kernel.FieldError {
		return []kernel.FieldError{{
			Pointer: at, Reason: kernel.ReasonRule, Message: fmt.Sprintf(format, args...),
		}}
	}
	switch st := e.settings; {
	case st.Disabled:
		return refuse("Stack %q is disabled.", st.Slug), nil
	case st.Deleting:
		return refuse("Stack %q is being deleted.", st.Slug), nil
	case st.Lock != nil && st.Lock.Holder != p.Caller():
		// Not resource-locked, which the run operations of one stack
		// answer: one problem names every entry a batch refuses, and a
		// 409 carries one lockedBy.
		// ref: decision 0003 § "API surfaces"
		return refuse("Stack %q is locked by %s.", st.Slug, st.Lock.By), nil
	}
	var err error
	e.commit, err = s.commit(ctx, p.AccountID, e.settings, sha,
		fmt.Sprintf("/stacks/%d/commitSha", i))
	if inv, ok := errors.AsType[*kernel.Invalid](err); ok {
		return inv.Errors, nil
	}
	if errors.Is(err, ErrNoCommit) {
		return refuse("Stack %q tracks no commit yet; name one with commitSha.", e.settings.Slug),
			nil
	}
	return nil, err
}
