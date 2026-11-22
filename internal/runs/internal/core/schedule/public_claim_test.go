package schedule_test

import (
	"context"
	"errors"
	"testing"
	"time"

	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/schedule"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

var errPublicPort = errors.New("public claim dependency failed")

type publicStore struct {
	*store
	failure string
	held    bool
	calls   []string
}

func (s *publicStore) stage(name string) error {
	s.calls = append(s.calls, name)
	if s.failure == name {
		return errPublicPort
	}
	return nil
}

func (s *publicStore) HoldingPublic(context.Context, kernel.ULID) (bool, error) {
	return s.held, s.stage("holding")
}

func (s *publicStore) ProbePublicCandidate(
	_ context.Context,
	_ kernel.ULID,
	_ []kernel.RunState,
	after *schedule.ClaimCursor,
	_ []kernel.ULID,
	_ []kernel.OrganizationID,
	_ []kernel.AccountID,
) (lifecycle.Run, bool, error) {
	if err := s.stage("probe"); err != nil {
		return lifecycle.Run{}, false, err
	}
	if after != nil || len(s.runs) == 0 {
		return lifecycle.Run{}, false, nil
	}
	return s.runs[0], true, nil
}

func (s *publicStore) LockPublicCandidate(_ context.Context, r lifecycle.Run, _ kernel.ULID, _ []kernel.RunState) (lifecycle.Run, bool, error) {
	err := s.stage("lock")
	return r, err == nil, err
}

func (s *publicStore) PublicUsage(context.Context, kernel.OrganizationID, kernel.AccountID) (featuresapi.Usage, error) {
	return featuresapi.Usage{}, s.stage("usage")
}

func (s *publicStore) AssignPublic(context.Context, lifecycle.Run, kernel.ULID, kernel.ULID) error {
	return s.stage("assign")
}

func (s *publicStore) CandidateAttempt(ctx context.Context, fn func(context.Context) (bool, error)) (bool, error) {
	if err := s.stage("attempt"); err != nil {
		return false, err
	}
	return fn(ctx)
}

func (s *publicStore) Lock(ctx context.Context, account kernel.AccountID, id kernel.ULID) (lifecycle.Run, error) {
	if err := s.stage("transition"); err != nil {
		return lifecycle.Run{}, err
	}
	return s.store.Lock(ctx, account, id)
}

type publicLimitFunc func(context.Context, kernel.OrganizationID, kernel.AccountID, string, func(context.Context) (featuresapi.Usage, error)) error

func (f publicLimitFunc) CheckLimit(
	ctx context.Context,
	org kernel.OrganizationID,
	account kernel.AccountID,
	name string,
	count func(context.Context) (featuresapi.Usage, error),
) error {
	return f(ctx, org, account, name, count)
}

type publicFenceFunc func(context.Context, kernel.AccountID, kernel.ULID) (bool, error)

func (f publicFenceFunc) HoldClaim(ctx context.Context, account kernel.AccountID, stack kernel.ULID) (bool, error) {
	return f(ctx, account, stack)
}

type publicInitializationFunc func(context.Context, lifecycle.Run) (policyapi.Evaluation, error)

func (f publicInitializationFunc) Evaluate(ctx context.Context, run lifecycle.Run) (policyapi.Evaluation, error) {
	return f(ctx, run)
}

func publicFixture() (*publicStore, *schedule.Claims, workersapi.Claim) {
	org := kernel.NewOrganizationID()
	s := &publicStore{
		store: &store{
			runs: []lifecycle.Run{
				{
					Account:      kernel.NewAccountID(),
					Organization: &org,
					ID:           kernel.NewULID(),
					Stack:        kernel.NewULID(),
					State:        kernel.RunStateReady,
					Type:         kernel.RunTypeProposed,
					Version:      1,
				},
			},
		},
	}
	limits := publicLimitFunc(
		func(ctx context.Context, gotOrg kernel.OrganizationID, account kernel.AccountID, name string, count func(context.Context) (featuresapi.Usage, error)) error {
			if gotOrg != org || account != s.runs[0].Account || name != featuresapi.PublicWorkerSlots {
				return errors.New("incorrect quota owner")
			}
			if err := s.stage("limit"); err != nil {
				return err
			}
			_, err := count(ctx)
			return err
		},
	)
	fence := publicFenceFunc(func(context.Context, kernel.AccountID, kernel.ULID) (bool, error) {
		err := s.stage("fence")
		return err == nil, err
	})
	lc := lifecycle.New(lifecycle.Deps{Store: s, Stacks: stacks{}, Events: events{}})
	return s, schedule.NewClaims(s, lc, fence).WithPublic(s, limits), workersapi.Claim{Public: true, Pool: kernel.NewULID(), Worker: kernel.NewULID()}
}

func TestUnitPublicClaimRequiresTransactionAndAllAdmissionOwners(t *testing.T) {
	_, claim, c := publicFixture()
	if _, ok, err := claim.Claim(t.Context(), c); ok || !errors.Is(err, lifecycle.ErrNoTransaction) {
		t.Fatalf("unfenced claim: %t %v", ok, err)
	}
	for _, missing := range []string{"store", "limits", "fence"} {
		t.Run(missing, func(t *testing.T) {
			s, _, c := publicFixture()
			var public schedule.PublicClaimStore = s
			var limits schedule.PublicLimits = publicLimitFunc(func(context.Context, kernel.OrganizationID, kernel.AccountID, string, func(context.Context) (featuresapi.Usage, error)) error {
				return nil
			})
			var fence schedule.ClaimFence = s
			switch missing {
			case "store":
				public = nil
			case "limits":
				limits = nil
			case "fence":
				fence = nil
			}
			claims := schedule.NewClaims(s, nil, fence).WithPublic(public, limits)
			err := tx.Do(t.Context(), db{}, tx.Options{}, func(ctx context.Context) error {
				_, ok, err := claims.Claim(ctx, c)
				if ok || err == nil {
					t.Fatalf("missing %s admitted a claim", missing)
				}
				return nil
			})
			if err != nil || len(s.calls) != 0 {
				t.Fatalf("missing admission owner accessed storage: %v %v", s.calls, err)
			}
		})
	}
}

func TestUnitPublicClaimDependencyFailuresStopBeforeAssignment(t *testing.T) {
	for _, stage := range []string{"holding", "probe", "attempt", "limit", "usage", "fence", "lock", "transition", "assign", "initialization"} {
		t.Run(stage, func(t *testing.T) {
			s, claims, c := publicFixture()
			s.failure = stage
			if stage == "initialization" {
				claims.WithInitialization(publicInitializationFunc(func(context.Context, lifecycle.Run) (policyapi.Evaluation, error) {
					return policyapi.Evaluation{}, errPublicPort
				}))
			}
			var assignment workersapi.Assignment
			var ok bool
			err := tx.Do(t.Context(), db{}, tx.Options{}, func(ctx context.Context) error { var err error; assignment, ok, err = claims.Claim(ctx, c); return err })
			if !errors.Is(err, errPublicPort) || ok || assignment.Run != (kernel.ULID{}) || assignment.Retry {
				t.Fatalf("dependency failure admitted work: %+v %t %v", assignment, ok, err)
			}
			if stage != "assign" {
				for _, call := range s.calls {
					if call == "assign" {
						t.Fatalf("assigned after %s failed: %v", stage, s.calls)
					}
				}
			}
		})
	}
}

func TestUnitPublicClaimRejectsUnownedRunsAndUnrelatedLimitErrors(t *testing.T) {
	for _, zero := range []bool{false, true} {
		s, claims, c := publicFixture()
		s.runs[0].Organization = nil
		if zero {
			s.runs[0].Organization = new(kernel.OrganizationID)
		}
		err := tx.Do(t.Context(), db{}, tx.Options{}, func(ctx context.Context) error {
			_, ok, err := claims.Claim(ctx, c)
			if ok {
				t.Fatal("unowned run assigned")
			}
			return err
		})
		if !errors.Is(err, lifecycle.ErrAccountMissing) {
			t.Fatal(err)
		}
	}
	for _, failure := range []*featuresapi.LimitError{{Limit: featuresapi.PrivateWorkers, Scope: featuresapi.ScopeOrganization}, {Limit: featuresapi.PublicWorkerSlots, Scope: featuresapi.Scope("unexpected")}} {
		s, claims, c := publicFixture()
		claims.WithPublic(s, publicLimitFunc(func(context.Context, kernel.OrganizationID, kernel.AccountID, string, func(context.Context) (featuresapi.Usage, error)) error {
			return failure
		}))
		err := tx.Do(t.Context(), db{}, tx.Options{}, func(ctx context.Context) error {
			a, ok, err := claims.Claim(ctx, c)
			if ok || a.Retry {
				t.Fatal("unrelated admission error became an idle retry")
			}
			return err
		})
		if !errors.Is(err, failure) {
			t.Fatalf("admission error hidden: %v", err)
		}
	}
}

func TestUnitPublicClaimHoldingWorkerDoesNotProbe(t *testing.T) {
	s, claims, c := publicFixture()
	s.held = true
	if err := tx.Do(t.Context(), db{}, tx.Options{}, func(ctx context.Context) error {
		a, ok, err := claims.Claim(ctx, c)
		if ok || a.Retry || err != nil {
			t.Fatalf("holding worker outcome: %+v %t %v", a, ok, err)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(s.calls) != 1 || s.calls[0] != "holding" {
		t.Fatalf("holding worker reached queue: %v", s.calls)
	}
}

func TestUnitPublicOwnerContentionRequestsBoundedRetry(t *testing.T) {
	s, _, c := publicFixture()
	claims := schedule.NewClaims(s, nil, publicFenceFunc(func(context.Context, kernel.AccountID, kernel.ULID) (bool, error) { return false, nil })).
		WithPublic(s, publicLimitFunc(func(context.Context, kernel.OrganizationID, kernel.AccountID, string, func(context.Context) (featuresapi.Usage, error)) error {
			return nil
		}))
	if err := tx.Do(t.Context(), db{}, tx.Options{}, func(ctx context.Context) error {
		a, ok, err := claims.Claim(ctx, c)
		if err != nil || ok || !a.Retry || a.RetryAfter < 50*time.Millisecond || a.RetryAfter >= 100*time.Millisecond {
			t.Fatalf("contention retry: %+v %t %v", a, ok, err)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
