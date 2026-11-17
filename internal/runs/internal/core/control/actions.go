package control

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// MaxComment is the most characters a comment's body holds, which the
// API document's RunCommentRequest.body and run_comments' CHECK bound
// too.
const MaxComment = 10000

// needsSource is what a retry needs of a run whose commit its stack's
// repository no longer has.
const needsSource = "a retry needs the run's commit, which the stack's repository no longer has"

// Comment records p's comment body on p's run raw of stack slug, and
// answers it. A body of no character, or of more than MaxComment, is a
// *kernel.Invalid at /body. The route has decided RUN_COMMENT on the
// stack's space.
func (s *Service) Comment(ctx context.Context, p kernel.Principal, slug, raw, body string) (read.Comment, error) {
	n := utf8.RuneCountInString(body)
	if n == 0 || n > MaxComment {
		reason := kernel.ReasonMaxLength
		if n == 0 {
			reason = kernel.ReasonMinLength
		}
		return read.Comment{}, &kernel.Invalid{
			Detail: "The comment is refused.",
			Errors: []kernel.FieldError{{
				Pointer: "/body", Reason: reason,
				Message: fmt.Sprintf("A comment holds 1 to %d characters.", MaxComment),
			}},
		}
	}
	c := read.Comment{
		ID: kernel.NewULID(), Username: p.Login, Body: body,
		At: s.d.Now().UTC().Truncate(time.Microsecond),
	}
	err := s.in(ctx, p, slug, raw, func(ctx context.Context, _ stacksapi.Stack, r lifecycle.Run) error {
		return s.d.Store.Comment(ctx, r, c) //nolint:wrapcheck // the store names the write
	})
	if err != nil {
		return read.Comment{}, err
	}
	return c, nil
}

// Discarded are the runs a DiscardAll discarded, and those another change
// moved on before it could, each oldest first.
type Discarded struct {
	Runs   []kernel.ULID
	Failed []kernel.ULID
}

// DiscardAll discards each of p's stack slug's runs that waits, oldest
// first: T3 from QUEUED, T8 from READY and T27 from UNCONFIRMED, with
// note. Each discard is a transaction of its own, so a run another change
// moved on first leaves the others discarded and is in Failed. The route
// has decided RUN_DISCARD on the stack's space.
//
// ref: decision 0008 § "Who drives each transition"
func (s *Service) DiscardAll(ctx context.Context, p kernel.Principal, slug, note string) (Discarded, error) {
	var waiting []lifecycle.Run
	err := tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		st, err := s.d.Stacks.BySlug(ctx, p.AccountID, slug)
		if err != nil {
			return err //nolint:wrapcheck // the stacks domain names the read
		}
		waiting, err = s.d.Store.Discardable(ctx, p.AccountID, st.ID)
		return err //nolint:wrapcheck // the store names the read
	})
	if err != nil {
		return Discarded{}, err //nolint:wrapcheck // each step names itself
	}
	out := Discarded{Runs: []kernel.ULID{}, Failed: []kernel.ULID{}}
	for _, w := range waiting {
		err := tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
			r, err := s.d.Store.Lock(ctx, p.AccountID, w.ID)
			if err != nil {
				return err //nolint:wrapcheck // the store names the read
			}
			_, err = s.d.Lifecycle.Transition(ctx, lifecycle.Request{
				Account: p.AccountID, Run: r.ID, Version: r.Version,
				To: kernel.RunStateDiscarded, Driver: machine.DriverAPI,
				Actor: lifecycle.ActorOf(p), Note: note,
			})
			return err //nolint:wrapcheck // the lifecycle names the move
		})
		switch {
		case errors.Is(err, lifecycle.ErrRefused), errors.Is(err, lifecycle.ErrNotFound):
			out.Failed = append(out.Failed, w.ID)
		case err != nil:
			return Discarded{}, err //nolint:wrapcheck // as above
		default:
			out.Runs = append(out.Runs, w.ID)
		}
	}
	return out, nil
}

// Retry creates a run of the type of p's run raw of stack slug on its
// commit, with the configuration it recorded at its first handover,
// runtime input, force-apply mode and environment variable overrides,
// and answers it: T1, through the
// lifecycle, which answers the run of the stack, type and commit that has
// not ended in its place when the retried run has no overrides. A retry
// never triggers dependants. A run read.RetryBlocker blocks, or whose
// commit the stack's repository no longer has, is a *Refused. The route
// has decided RUN_RETRY on the stack's space, and a tracked run also
// needs RUN_RETRY_BLOCKING there, else a *Blocked.
//
// ref: decision 0008 § "The state machine is the authority"
func (s *Service) Retry(ctx context.Context, p kernel.Principal, slug, raw string) (read.View, error) {
	return s.act(ctx, p, slug, raw, func(ctx context.Context, st stacksapi.Stack, r lifecycle.Run) (lifecycle.Run, error) {
		// The tracked commit cannot change between the guard and creation.
		settings, err := s.d.Stacks.LockForRun(ctx, p.AccountID, st.ID)
		if err != nil {
			return r, err //nolint:wrapcheck // the stacks domain names the read
		}
		if why := read.RetryBlocker(r, settings); why != "" {
			return r, &Refused{Run: r.ID, State: r.State, Needs: why}
		}
		if r.Type == kernel.RunTypeTracked {
			ok, err := s.d.Authz.Authorize(ctx, p, accessapi.ActionRunRetryBlocking, st.Space)
			if err != nil {
				return r, err //nolint:wrapcheck // the snapshot's read names itself
			}
			if !ok {
				return r, &Blocked{Run: r.ID, Action: accessapi.ActionRunRetryBlocking}
			}
		}
		if err := s.resolve(ctx, r); err != nil {
			return r, err
		}
		ovs, err := s.d.Store.Overrides(ctx, r)
		if err != nil {
			return r, err //nolint:wrapcheck // the store names the read
		}
		run, _, err := s.d.Lifecycle.Create(ctx, lifecycle.NewRun{
			Account: p.AccountID, Stack: st.ID, Type: r.Type, Commit: r.Commit,
			Actor: lifecycle.ActorOf(p), RuntimeConfig: r.RuntimeConfig, Resolved: r.ResolvedConfig,
			ForceApply: r.ForceApply, Overrides: ovs, Note: "retry of " + r.ID.String(),
		})
		return run, err //nolint:wrapcheck // the lifecycle names the refusal
	})
}

// resolve checks that the repository of r's stack still has r's commit;
// a *Refused when it does not.
func (s *Service) resolve(ctx context.Context, r lifecycle.Run) error {
	s.mu.RLock()
	src := s.sources
	s.mu.RUnlock()
	if src == nil {
		return fmt.Errorf("%w: no source is bound", kernel.ErrUnavailable)
	}
	_, err := src.Resolve(ctx, r.Account, r.Stack, r.Commit.Hash)
	if errors.Is(err, stacksapi.ErrRefNotFound) {
		return &Refused{Run: r.ID, State: r.State, Needs: needsSource}
	}
	return err //nolint:wrapcheck // the vcs domain names the resolve
}

// Promote creates a tracked run on the commit of p's proposed run raw of
// stack slug, which becomes the stack's tracked commit, and answers it:
// T1, through the lifecycle, which answers the tracked run on that commit
// that has not ended in its place. The stack's row is locked first, so
// two promotions compare their commits one after the other. A run
// read.PromoteBlocker blocks is a *Refused. The route has decided
// RUN_PROMOTE on the stack's space.
//
// ref: report 08 § "7. Promotion, autodeploy, autoretry and the PR loop"
func (s *Service) Promote(ctx context.Context, p kernel.Principal, slug, raw string) (read.View, error) {
	return s.act(ctx, p, slug, raw, func(ctx context.Context, st stacksapi.Stack, r lifecycle.Run) (lifecycle.Run, error) {
		settings, err := s.d.Stacks.LockForRun(ctx, p.AccountID, st.ID)
		if err != nil {
			return r, err //nolint:wrapcheck // the stacks domain names the read
		}
		if why := read.PromoteBlocker(r, settings); why != "" {
			return r, &Refused{Run: r.ID, State: r.State, Needs: why}
		}
		tracked := r.Commit
		tracked.Branch = ""
		if err := s.d.Stacks.SetTracked(ctx, p.AccountID, st.ID, nil, &tracked,
			p.Login); err != nil {
			return r, err //nolint:wrapcheck // the stacks domain names the write
		}
		commit := r.Commit
		commit.Branch = settings.Branch
		run, _, err := s.d.Lifecycle.Create(ctx, lifecycle.NewRun{
			Account: p.AccountID, Stack: st.ID, Type: kernel.RunTypeTracked, Commit: commit,
			Actor: lifecycle.ActorOf(p), Note: "promotion of " + r.ID.String(),
		})
		return run, err //nolint:wrapcheck // the lifecycle names the refusal
	})
}
