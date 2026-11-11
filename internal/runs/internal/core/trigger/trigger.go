package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// ErrNoCommit reports a trigger with no commit named on a stack that
// tracks none and whose branch head is not known yet.
var ErrNoCommit = errors.New("runs: the stack tracks no commit")

// Store reads what a triggered run is answered with. Each method joins the
// transaction its context carries.
type Store interface {
	// History reads acct's run's transitions, oldest first.
	History(ctx context.Context, acct kernel.AccountID, run kernel.ULID) ([]lifecycle.Step, error)
}

// Deps are what the trigger reads and writes through.
type Deps struct {
	DB        tx.Beginner
	Store     Store
	Stacks    stacksapi.Stacks
	Lifecycle *lifecycle.Lifecycle
}

// Service triggers runs on a stack.
type Service struct {
	d       Deps
	mu      sync.RWMutex
	sources stacksapi.StackSources
}

// New returns the service over d. A named commit is unavailable until
// BindSources binds the vcs domain's resolution.
func New(d Deps) *Service { return &Service{d: d} }

// BindSources binds the vcs domain's resolution of a named commit.
func (s *Service) BindSources(src stacksapi.StackSources) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sources = src
}

// Input is a trigger: the run's type, TRACKED when empty; the commit SHA
// it works on, the stack's tracked commit when empty; its runtime
// configuration, as JSON; and its force-apply mode.
type Input struct {
	Type          kernel.RunType
	CommitSHA     string
	RuntimeConfig json.RawMessage
	ForceApply    string
}

// View is a run as the API answers it: the run, its stack's branch and
// its transitions, oldest first.
type View struct {
	Run     lifecycle.Run
	Stack   stacksapi.Stack
	Branch  string
	History []lifecycle.Step
}

// Trigger creates p's run on its account's stack slug in one transaction,
// or answers the run of the stack, type and commit that has not ended. A
// type other than TRACKED or PROPOSED, and a commit the repository lacks,
// are a *kernel.Invalid; with no commit named, the stack's tracked commit
// is used, else its branch head, else ErrNoCommit. The lifecycle refuses
// a disabled stack and one another caller locked.
//
// ref: decision 0008 § "Who drives each transition"
func (s *Service) Trigger(
	ctx context.Context,
	p kernel.Principal,
	slug string,
	in Input,
) (View, error) {
	typ := in.Type
	if typ == "" {
		typ = kernel.RunTypeTracked
	}
	if typ != kernel.RunTypeTracked && typ != kernel.RunTypeProposed {
		return View{}, &kernel.Invalid{
			Detail: "A trigger creates a TRACKED or a PROPOSED run.",
			Errors: []kernel.FieldError{{
				Pointer: "/runType", Reason: kernel.ReasonEnum,
				Message: fmt.Sprintf("%s runs are not triggered here.", typ),
			}},
		}
	}
	var v View
	err := tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		st, err := s.d.Stacks.BySlug(ctx, p.AccountID, slug)
		if err != nil {
			return err //nolint:wrapcheck // the stacks domain names the read
		}
		settings, err := s.d.Stacks.Settings(ctx, p.AccountID, st.ID)
		if err != nil {
			return err //nolint:wrapcheck // as above
		}
		commit, err := s.commit(ctx, p.AccountID, settings, in.CommitSHA)
		if err != nil {
			return err
		}
		run, _, err := s.d.Lifecycle.Create(ctx, lifecycle.NewRun{
			Account: p.AccountID, Stack: st.ID, Type: typ, Commit: commit,
			Actor: lifecycle.ActorOf(p), RuntimeConfig: in.RuntimeConfig, ForceApply: in.ForceApply,
		})
		if err != nil {
			return err //nolint:wrapcheck // the lifecycle names the refusal
		}
		history, err := s.d.Store.History(ctx, p.AccountID, run.ID)
		if err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		v = View{Run: run, Stack: st, Branch: settings.Branch, History: history}
		return nil
	})
	return v, err //nolint:wrapcheck // each step names itself
}

// commit is the commit a run of st works on: sha resolved on its
// repository, or its tracked commit, or its branch head.
func (s *Service) commit(
	ctx context.Context,
	acct kernel.AccountID,
	st stacksapi.Settings,
	sha string,
) (stacksapi.Commit, error) {
	if sha == "" {
		switch {
		case st.TrackedCommit != nil:
			return *st.TrackedCommit, nil
		case st.TrackedBranchHead != nil:
			return *st.TrackedBranchHead, nil
		}
		return stacksapi.Commit{}, fmt.Errorf("%w: %s", ErrNoCommit, st.Slug)
	}
	s.mu.RLock()
	src := s.sources
	s.mu.RUnlock()
	if src == nil {
		return stacksapi.Commit{}, fmt.Errorf("%w: no source is bound", kernel.ErrUnavailable)
	}
	c, err := src.Resolve(ctx, acct, st.ID, sha)
	if errors.Is(err, stacksapi.ErrRefNotFound) {
		return stacksapi.Commit{}, &kernel.Invalid{
			Detail: "The stack's repository has no such commit.",
			Errors: []kernel.FieldError{{
				Pointer: "/commitSha", Reason: kernel.ReasonNotFound,
				Message: fmt.Sprintf("No commit %s in the stack's repository.", sha),
			}},
		}
	}
	return c, err //nolint:wrapcheck // the vcs domain names the resolve
}
