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
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// ErrNoCommit reports a trigger with no commit named on a stack that
// tracks none and whose branch head is not known yet.
var ErrNoCommit = errors.New("runs: the stack tracks no commit")

// Deps are what the trigger reads and writes through, with the objects
// local workspaces are uploaded to.
type Deps struct {
	DB        tx.Beginner
	Stacks    stacksapi.Stacks
	Lifecycle *lifecycle.Lifecycle
	Views     *read.Service
	Objects   Objects
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
// configuration, as JSON; its force-apply mode; and the environment
// overrides of a PROPOSED run.
type Input struct {
	Type          kernel.RunType
	CommitSHA     string
	RuntimeConfig json.RawMessage
	ForceApply    string
	Overrides     []lifecycle.Override
}

// Trigger creates p's run on its account's stack slug in one transaction,
// or, with no overrides, answers the run of the stack, type and commit
// that has not ended. A type other than TRACKED or PROPOSED, overrides on
// another type or that CheckOverrides refuses, and a commit the
// repository lacks, are a *kernel.Invalid; with no commit named, the stack's tracked commit
// is used, else its branch head, else ErrNoCommit. A trigger names no
// branch, so the run is created for the stack's tracked branch whatever
// its type. The lifecycle refuses a disabled stack and one another caller
// locked.
//
// ref: decision 0008 § "Who drives each transition"
func (s *Service) Trigger(
	ctx context.Context,
	p kernel.Principal,
	slug string,
	in Input,
) (read.View, error) {
	typ := in.Type
	if typ == "" {
		typ = kernel.RunTypeTracked
	}
	if typ != kernel.RunTypeTracked && typ != kernel.RunTypeProposed {
		return read.View{}, &kernel.Invalid{
			Detail: "A trigger creates a TRACKED or a PROPOSED run.",
			Errors: []kernel.FieldError{{
				Pointer: "/runType", Reason: kernel.ReasonEnum,
				Message: fmt.Sprintf("%s runs are not triggered here.", typ),
			}},
		}
	}
	if len(in.Overrides) > 0 && typ != kernel.RunTypeProposed {
		return read.View{}, &kernel.Invalid{
			Detail: "Only a proposed run takes environment variable overrides.",
			Errors: []kernel.FieldError{{
				Pointer: "/environmentVarsOverrides", Reason: kernel.ReasonRule,
				Message: fmt.Sprintf("A %s run takes no overrides.", typ),
			}},
		}
	}
	if err := CheckOverrides(in.Overrides); err != nil {
		return read.View{}, err
	}
	var v read.View
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
		commit.Branch = settings.Branch
		run, _, err := s.d.Lifecycle.Create(ctx, lifecycle.NewRun{
			Account: p.AccountID, Stack: st.ID, Type: typ, Commit: commit,
			Actor: lifecycle.ActorOf(p), RuntimeConfig: in.RuntimeConfig, ForceApply: in.ForceApply,
			Overrides: in.Overrides,
		})
		if err != nil {
			return err //nolint:wrapcheck // the lifecycle names the refusal
		}
		v, err = s.d.Views.One(ctx, p, run)
		return err //nolint:wrapcheck // the read names itself
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
