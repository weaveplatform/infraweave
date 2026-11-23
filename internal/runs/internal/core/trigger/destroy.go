package trigger

import (
	"context"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

var _ stacksapi.StackDestroyer = (*Service)(nil)

// Destroys is what a destroy trigger reads of a stack's runs, in the
// transaction that holds the stack's row: the runs that have not ended,
// and whether a tracked run entered APPLYING since the stack was created
// or its last destroy run finished.
type Destroys interface {
	Unended(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) ([]runsapi.UnendedRun, error)
	Applied(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) (bool, error)
}

// Destroy creates p's DESTROY run on p's account's stack id in the
// transaction ctx carries, which the stacks domain's delete holds the
// stack's row in, as destroyRun does.
//
// ref: decision 0008 § "Destroy on delete"
func (s *Service) Destroy(ctx context.Context, p kernel.Principal, id kernel.ULID) error {
	settings, err := s.d.Stacks.Settings(ctx, p.AccountID, id)
	if err != nil {
		return err //nolint:wrapcheck // the stacks domain names the read
	}
	_, err = s.destroyRun(ctx, p, settings)
	return err
}

// DestroyTrigger creates p's DESTROY run on its account's stack slug in
// one transaction, through the creation a stack's delete with
// destroyResources takes, and answers it as a read. The run plans the
// destroy before it destroys, queues behind the stack's other runs, and
// leaves the stack disabled once it finishes. Before creating it, a stack
// that is being deleted, has a destroy run that has not ended, or manages
// no resources, as no tracked run entered APPLYING since it was created
// or its last destroy run finished, is refused with a
// *stacksapi.Undestroyable, and one protected from deletion with a
// *kernel.Invalid, as its delete is; destroyRun's refusals follow.
//
// ref: decision 0008 § "The destroy trigger"
func (s *Service) DestroyTrigger(ctx context.Context, p kernel.Principal, slug string) (read.View, error) {
	var v read.View
	err := tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		st, err := s.d.Stacks.BySlug(ctx, p.AccountID, slug)
		if err != nil {
			return err //nolint:wrapcheck // the stacks domain names the read
		}
		// The row lock orders these checks before any other creation on
		// the stack, so no destroy run starts between them and this one.
		settings, err := s.d.Stacks.LockForRun(ctx, p.AccountID, st.ID)
		if err != nil {
			return err //nolint:wrapcheck // as above
		}
		if err := s.destroyable(ctx, settings); err != nil {
			return err
		}
		run, err := s.destroyRun(ctx, p, settings)
		if err != nil {
			return err
		}
		v, err = s.d.Views.One(ctx, p, run)
		return err //nolint:wrapcheck // the read names itself
	})
	return v, err //nolint:wrapcheck // each step names itself
}

// destroyable refuses a destroy trigger on st: one being deleted, one
// protected from deletion, one with a destroy run that has not ended, and
// one that manages no resources.
func (s *Service) destroyable(ctx context.Context, st stacksapi.Settings) error {
	switch {
	case st.Deleting:
		return &stacksapi.Undestroyable{
			Detail: "The stack is being deleted; its destroy run is under way.",
		}
	case st.ProtectFromDeletion:
		return &kernel.Invalid{Detail: "The stack is protected from deletion; " +
			"turn protectFromDeletion off first."}
	}
	unended, err := s.d.Destroys.Unended(ctx, st.Account, st.ID)
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	for _, r := range unended {
		if r.Type == kernel.RunTypeDestroy {
			return &stacksapi.Undestroyable{
				Detail: fmt.Sprintf("The stack's destroy run %s has not ended yet.", r.ID),
			}
		}
	}
	applied, err := s.d.Destroys.Applied(ctx, st.Account, st.ID)
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	if !applied {
		return &stacksapi.Undestroyable{
			Detail: "The stack manages no resources: no run has applied changes to it " +
				"since it was created or since its last destroy run finished.",
		}
	}
	return nil
}

// destroyRun creates p's DESTROY run on st, on its tracked commit, else
// its branch head, in the transaction ctx carries, which holds the
// stack's row. A stack that is disabled, locked by another caller or
// tracks no commit is refused with a *stacksapi.Undestroyable that says
// so.
func (s *Service) destroyRun(ctx context.Context, p kernel.Principal, st stacksapi.Settings) (lifecycle.Run, error) {
	commit, err := s.commit(ctx, p.AccountID, st, "", "")
	if errors.Is(err, ErrNoCommit) {
		return lifecycle.Run{}, &stacksapi.Undestroyable{
			Detail: "The stack tracks no commit yet; sync the stack to destroy its resources.",
		}
	}
	if err != nil {
		return lifecycle.Run{}, err
	}
	commit.Branch = st.Branch
	run, _, err := s.d.Lifecycle.Create(ctx, lifecycle.NewRun{
		Account: p.AccountID, Stack: st.ID, Type: kernel.RunTypeDestroy, Commit: commit,
		Actor: lifecycle.ActorOf(p),
	})
	if l, ok := errors.AsType[*lifecycle.Locked](err); ok {
		return lifecycle.Run{}, &stacksapi.Undestroyable{
			Detail:   "The stack is locked by " + l.By + "; only its holder destroys its resources.",
			LockedBy: l.By,
		}
	}
	if errors.Is(err, lifecycle.ErrDisabled) {
		return lifecycle.Run{}, &stacksapi.Undestroyable{
			Detail: "The stack is disabled; enable it to destroy its resources.",
		}
	}
	return run, err //nolint:wrapcheck // the lifecycle names the refusal
}
