package schedule

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// PublicClaimStore keeps tenant-owned runs separate from the installation worker.
// Each operation joins the caller's claim transaction.
type PublicClaimStore interface {
	HoldingPublic(context.Context, kernel.ULID) (bool, error)
	ProbePublicCandidate(context.Context, kernel.ULID, []kernel.RunState, *ClaimCursor, []kernel.ULID, []kernel.OrganizationID, []kernel.AccountID) (lifecycle.Run, bool, error)
	LockPublicCandidate(context.Context, lifecycle.Run, kernel.ULID, []kernel.RunState) (lifecycle.Run, bool, error)
	PublicUsage(context.Context, kernel.OrganizationID, kernel.AccountID) (featuresapi.Usage, error)
	AssignPublic(context.Context, lifecycle.Run, kernel.ULID, kernel.ULID, time.Time) error
}

// PublicLimits serializes organization/account admission with actual usage.
type PublicLimits interface {
	CheckLimit(context.Context, kernel.OrganizationID, kernel.AccountID, string, func(context.Context) (featuresapi.Usage, error)) error
}

var errPublicScheduling = errors.New("runs: public scheduling dependencies are unavailable")

// WithPublic binds public assignment to the existing transactional quota owner.
func (s *Claims) WithPublic(store PublicClaimStore, limits PublicLimits) *Claims {
	s.publicStore, s.publicLimits = store, limits
	return s
}

func (s *Claims) claimPublic(ctx context.Context, c workersapi.Claim) (workersapi.Assignment, bool, error) {
	if !tx.In(ctx) {
		return workersapi.Assignment{}, false, lifecycle.ErrNoTransaction
	}
	if s.publicStore == nil || s.publicLimits == nil || s.fence == nil {
		return workersapi.Assignment{}, false, errPublicScheduling
	}
	holds, err := s.publicStore.HoldingPublic(ctx, c.Worker)
	if err != nil || holds {
		return workersapi.Assignment{}, false, err //nolint:wrapcheck // the store names the read
	}
	run, found, contended, err := s.publicCandidate(ctx, c)
	if err != nil {
		return workersapi.Assignment{}, false, err
	}
	if !found {
		if contended {
			delay := contentionRetryDelay + rand.N(contentionRetryDelay) //nolint:gosec // contention jitter, not a credential
			return workersapi.Assignment{Retry: true, RetryAfter: delay}, false, nil
		}
		return workersapi.Assignment{}, false, nil
	}
	if run.State == kernel.RunStateReady && s.initialization != nil {
		refused, err := s.initialize(ctx, run)
		if err != nil || refused {
			return workersapi.Assignment{Retry: refused && err == nil}, false, err
		}
	}
	to, _ := machine.Claimed(run.Type, run.State)
	moved, err := s.lifecycle.Transition(ctx, lifecycle.Request{
		Account: run.Account, Run: run.ID, Version: run.Version, To: to, Driver: machine.DriverWorkerClaim,
	})
	if err != nil {
		return workersapi.Assignment{}, false, err //nolint:wrapcheck // the lifecycle names the move
	}
	deadline := PreparingDeadline(moved, c.Preparing)
	if err := s.publicStore.AssignPublic(ctx, moved, c.Worker, c.Pool, deadline); err != nil {
		return workersapi.Assignment{}, false, err //nolint:wrapcheck // the store names the write
	}
	return workersapi.Assignment{
		Run: moved.ID, State: moved.State, StateVersion: moved.Version, PreparingDeadline: deadline,
	}, true, nil
}

func (s *Claims) publicCandidate(ctx context.Context, c workersapi.Claim) (lifecycle.Run, bool, bool, error) {
	var after *ClaimCursor
	var stacks []kernel.ULID
	var organizations []kernel.OrganizationID
	var accounts []kernel.AccountID
	var contended bool
	for {
		probe, found, err := s.publicStore.ProbePublicCandidate(ctx, c.Pool, machine.Claimable(), after, stacks, organizations, accounts)
		if err != nil || !found {
			return lifecycle.Run{}, false, contended, err //nolint:wrapcheck // the store names the read
		}
		if probe.Organization == nil || probe.Organization.IsZero() {
			return lifecycle.Run{}, false, contended, lifecycle.ErrAccountMissing
		}
		var run lifecycle.Run
		var quota *featuresapi.LimitError
		var fenced bool
		held, err := s.store.CandidateAttempt(ctx, func(ctx context.Context) (bool, error) {
			err := s.publicLimits.CheckLimit(ctx, *probe.Organization, probe.Account, featuresapi.PublicWorkerSlots,
				func(ctx context.Context) (featuresapi.Usage, error) {
					return s.publicStore.PublicUsage(ctx, *probe.Organization, probe.Account)
				})
			if err != nil {
				if denied, ok := errors.AsType[*featuresapi.LimitError](err); ok && denied.Limit == featuresapi.PublicWorkerSlots &&
					(denied.Scope == featuresapi.ScopeOrganization || denied.Scope == featuresapi.ScopeAccount) {
					quota = denied
					return false, nil
				}
				return false, err //nolint:wrapcheck // the feature owner names the failure
			}
			fenced, err = s.fence.HoldClaim(ctx, probe.Account, probe.Stack)
			if err != nil || !fenced {
				return false, err //nolint:wrapcheck // the owner names the fence
			}
			var locked bool
			run, locked, err = s.publicStore.LockPublicCandidate(ctx, probe, c.Pool, machine.Claimable())
			return locked, err //nolint:wrapcheck // the store names exact revalidation
		})
		if err != nil || held {
			return run, held, contended, err //nolint:wrapcheck // the attempt names its transaction failure
		}
		switch {
		case quota != nil && quota.Scope == featuresapi.ScopeOrganization:
			organizations = append(organizations, *probe.Organization)
		case quota != nil:
			accounts = append(accounts, probe.Account)
		default:
			contended = true
			if !fenced {
				stacks = append(stacks, probe.Stack)
			}
		}
		after = &ClaimCursor{CreatedAt: probe.CreatedAt, ID: probe.ID}
	}
}
