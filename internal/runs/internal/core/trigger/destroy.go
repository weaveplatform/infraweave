package trigger

import (
	"context"
	"errors"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

var _ stacksapi.StackDestroyer = (*Service)(nil)

// Destroy creates p's DESTROY run on p's account's stack id, on its
// tracked commit, else its branch head, in the transaction ctx carries,
// which the stacks domain's delete holds the stack's row in. A stack that
// is disabled, locked by another caller or tracks no commit is refused
// with a *stacksapi.Undestroyable that says so.
//
// ref: decision 0008 § "Destroy on delete"
func (s *Service) Destroy(ctx context.Context, p kernel.Principal, id kernel.ULID) error {
	settings, err := s.d.Stacks.Settings(ctx, p.AccountID, id)
	if err != nil {
		return err //nolint:wrapcheck // the stacks domain names the read
	}
	commit, err := s.commit(ctx, p.AccountID, settings, "", "")
	if errors.Is(err, ErrNoCommit) {
		return &stacksapi.Undestroyable{
			Detail: "The stack tracks no commit yet; sync the stack to destroy its resources.",
		}
	}
	if err != nil {
		return err
	}
	commit.Branch = settings.Branch
	_, _, err = s.d.Lifecycle.Create(ctx, lifecycle.NewRun{
		Account: p.AccountID, Stack: id, Type: kernel.RunTypeDestroy, Commit: commit,
		Actor: lifecycle.ActorOf(p),
	})
	if l, ok := errors.AsType[*lifecycle.Locked](err); ok {
		return &stacksapi.Undestroyable{
			Detail:   "The stack is locked by " + l.By + "; only its holder destroys its resources.",
			LockedBy: l.By,
		}
	}
	if errors.Is(err, lifecycle.ErrDisabled) {
		return &stacksapi.Undestroyable{
			Detail: "The stack is disabled; enable it to destroy its resources.",
		}
	}
	return err //nolint:wrapcheck // the lifecycle names the refusal
}
