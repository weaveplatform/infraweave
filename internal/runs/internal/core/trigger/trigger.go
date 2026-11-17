package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/runtimeconfig"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// ErrNoCommit reports a trigger with no commit named on a stack that
// tracks none and whose branch head is not known yet.
var ErrNoCommit = errors.New("runs: the stack tracks no commit")

// RuntimeConfigDenied names the additional action a custom configuration needs.
type RuntimeConfigDenied struct{ Action accessapi.Action }

func (e *RuntimeConfigDenied) Error() string {
	return fmt.Sprintf("Triggering with a runtime configuration needs %s.", e.Action)
}

// Deps are what the trigger reads and writes through, the authorizer a
// batch and custom-runtime trigger decide each stack's actions with, and the objects local
// workspaces are uploaded to.
type Deps struct {
	DB        tx.Beginner
	Stacks    stacksapi.Stacks
	Authz     accessapi.Authorizer
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
// locked. A custom runtime configuration also needs
// RUN_TRIGGER_WITH_CUSTOM_RUNTIME_CONFIG on the stack's space and always
// creates a fresh run; its variables join the sealed run-local overrides.
//
// ref: decision 0008 § "Who drives each transition"
// ref: decision 0007 "Authorization"
func (s *Service) Trigger(ctx context.Context, p kernel.Principal, slug string, in Input) (read.View, error) {
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
	return s.create(ctx, p, slug, in.CommitSHA, lifecycle.NewRun{
		Type: typ, RuntimeConfig: in.RuntimeConfig, ForceApply: in.ForceApply,
		Overrides: in.Overrides, AlwaysCreate: len(in.RuntimeConfig) > 0,
	})
}

// Task creates p's TASK run of t on its account's stack slug in one
// transaction, on the stack's tracked commit, else its branch head, else
// ErrNoCommit. Every task creates a run. A command t.Check refuses is a
// *kernel.Invalid at /command; the lifecycle refuses a disabled stack and
// one another caller locked.
//
// ref: decision 0008 § "Who drives each transition"
func (s *Service) Task(ctx context.Context, p kernel.Principal, slug string, t kernel.Task) (read.View, error) {
	if errs := t.Check("/command"); errs != nil {
		return read.View{}, &kernel.Invalid{Detail: "The task's command is refused.", Errors: errs}
	}
	return s.create(ctx, p, slug, "", lifecycle.NewRun{Type: kernel.RunTypeTask, Task: &t})
}

// create creates n's run as p on p's account's stack slug, on the commit
// sha, in one transaction, and answers its view.
func (s *Service) create(ctx context.Context, p kernel.Principal, slug, sha string, n lifecycle.NewRun) (read.View, error) {
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
		if len(n.RuntimeConfig) > 0 {
			const action = accessapi.ActionRunTriggerWithCustomRuntimeConfig
			allowed, err := s.d.Authz.Authorize(ctx, p, action, st.Space)
			if err != nil {
				return err //nolint:wrapcheck // the authorizer names the decision
			}
			if !allowed {
				return &RuntimeConfigDenied{Action: action}
			}
			configuration, err := runtimeconfig.Parse(n.RuntimeConfig, settings.Vendor)
			if err != nil {
				return err //nolint:wrapcheck // the parser names each refused setting
			}
			n.RuntimeConfig = configuration.Stored()
			for _, v := range configuration.Environment {
				n.Overrides = append(n.Overrides, lifecycle.Override{Name: v.Key, Value: v.Value})
			}
		}
		if n.Commit, err = s.commit(ctx, p.AccountID, settings, sha, "/commitSha"); err != nil {
			return err
		}
		n.Commit.Branch = settings.Branch
		n.Account, n.Stack, n.Actor = p.AccountID, st.ID, lifecycle.ActorOf(p)
		run, _, err := s.d.Lifecycle.Create(ctx, n)
		if err != nil {
			return err //nolint:wrapcheck // the lifecycle names the refusal
		}
		v, err = s.d.Views.One(ctx, p, run)
		return err //nolint:wrapcheck // the read names itself
	})
	return v, err //nolint:wrapcheck // each step names itself
}

// commit is the commit a run of st works on: sha resolved on its
// repository, a commit it lacks refused at at, or its tracked commit, or
// its branch head.
func (s *Service) commit(ctx context.Context, acct kernel.AccountID, st stacksapi.Settings, sha, at string) (stacksapi.Commit, error) {
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
				Pointer: at, Reason: kernel.ReasonNotFound,
				Message: fmt.Sprintf("No commit %s in the stack's repository.", sha),
			}},
		}
	}
	return c, err //nolint:wrapcheck // the vcs domain names the resolve
}
