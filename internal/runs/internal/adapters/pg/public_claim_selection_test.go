package pg_test

import (
	"context"
	"errors"
	"testing"
	"time"

	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/schedule"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// Quota failures are injected at the entitlement port; queue traversal, row
// locks, lifecycle transitions, assignments and rollbacks use PostgreSQL.
type publicAdmission func(context.Context, kernel.OrganizationID, kernel.AccountID, string, func(context.Context) (featuresapi.Usage, error)) error

func (f publicAdmission) CheckLimit(
	ctx context.Context,
	org kernel.OrganizationID,
	account kernel.AccountID,
	name string,
	count func(context.Context) (featuresapi.Usage, error),
) error {
	return f(ctx, org, account, name, count)
}

func takePublic(t *testing.T, w *world, claims *schedule.Claims, pool, worker kernel.ULID) (workersapi.Assignment, bool, error) {
	t.Helper()
	var a workersapi.Assignment
	var ok bool
	err := w.in(t, func(ctx context.Context) error {
		var err error
		a, ok, err = claims.Claim(ctx, workersapi.Claim{Public: true, Pool: pool, Worker: worker})
		return err
	})
	return a, ok, err
}

func TestAccPublicClaimSkipsFullQuotaOwnerWithoutStarvingEligibleAccount(t *testing.T) {
	for _, scope := range []featuresapi.Scope{featuresapi.ScopeAccount, featuresapi.ScopeOrganization} {
		t.Run(string(scope), func(t *testing.T) {
			w := newWorld(t)
			org := kernel.NewOrganizationID()
			otherOrg := org
			if scope == featuresapi.ScopeOrganization {
				otherOrg = kernel.NewOrganizationID()
			}
			blocked := publicWaiting(t, w, w.acct, org, w.now.Add(-2*time.Minute))
			sibling := publicWaiting(t, w, w.acct, org, w.now.Add(-time.Minute))
			otherAccount := kernel.NewAccountID()
			eligible := publicWaiting(t, w, otherAccount, otherOrg, w.now)
			checked := map[kernel.AccountID]int{}
			limits := publicAdmission(
				func(ctx context.Context, gotOrg kernel.OrganizationID, account kernel.AccountID, name string, count func(context.Context) (featuresapi.Usage, error)) error {
					checked[account]++
					if name != featuresapi.PublicWorkerSlots {
						t.Fatal(name)
					}
					if _, err := count(ctx); err != nil {
						return err
					}
					if account == w.acct {
						if gotOrg != org {
							t.Fatal("wrong organization quota owner")
						}
						return &featuresapi.LimitError{Limit: name, Scope: scope, Value: 0}
					}
					if account != otherAccount || gotOrg != otherOrg {
						t.Fatal("wrong eligible owner")
					}
					return nil
				},
			)
			claims := schedule.NewClaims(w.store, w.lc, w).WithPublic(w.store, limits)
			pool, worker := kernel.NewULID(), kernel.NewULID()
			a, ok, err := takePublic(t, w, claims, pool, worker)
			if err != nil || !ok || a.Run != eligible {
				t.Fatalf("quota owner starved available account: %+v %t %v", a, ok, err)
			}
			if checked[w.acct] != 1 || checked[otherAccount] != 1 {
				t.Fatalf("full quota siblings were reconsidered: %v", checked)
			}
			for _, id := range []kernel.ULID{blocked, sibling} {
				state, assigned := w.held(t, id)
				if state != kernel.RunStateReady || assigned != nil {
					t.Fatalf("quota-refused run changed: %s %v", state, assigned)
				}
			}
			var storedPool kernel.ULID
			if err := w.db.QueryRow(t.Context(), `SELECT public_worker_pool_id FROM runs WHERE id=$1`, eligible).Scan(&storedPool); err != nil || storedPool != pool {
				t.Fatalf("wrong immutable public pool: %s %v", storedPool, err)
			}
		})
	}
}

func TestAccPublicClaimSkipsLockedRunAndRetriesAfterRelease(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "retry", true: "other available run"}[fallback], func(t *testing.T) {
			w := newWorld(t)
			org := kernel.NewOrganizationID()
			first := publicWaiting(t, w, w.acct, org, w.now.Add(-time.Minute))
			var second kernel.ULID
			if fallback {
				second = publicWaiting(t, w, w.acct, org, w.now)
			}
			holder, err := w.db.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = holder.Rollback(context.WithoutCancel(t.Context())) }()
			if _, err := holder.Exec(t.Context(), `SELECT id FROM runs WHERE id=$1 FOR UPDATE`, first); err != nil {
				t.Fatal(err)
			}
			limits := publicAdmission(
				func(ctx context.Context, _ kernel.OrganizationID, _ kernel.AccountID, _ string, count func(context.Context) (featuresapi.Usage, error)) error {
					_, err := count(ctx)
					return err
				},
			)
			claims := schedule.NewClaims(w.store, w.lc, w).WithPublic(w.store, limits)
			pool, worker := kernel.NewULID(), kernel.NewULID()
			a, ok, err := takePublic(t, w, claims, pool, worker)
			if err != nil {
				t.Fatal(err)
			}
			if fallback {
				if !ok || a.Run != second || a.Retry {
					t.Fatalf("locked head starved available public work: %+v %t", a, ok)
				}
			} else if ok || !a.Retry || a.RetryAfter < 50*time.Millisecond || a.RetryAfter >= 100*time.Millisecond {
				t.Fatalf("locked head did not request bounded retry: %+v %t", a, ok)
			}
			state, assigned := w.held(t, first)
			if state != kernel.RunStateReady || assigned != nil {
				t.Fatal("contended run mutated")
			}
			if err := holder.Rollback(t.Context()); err != nil {
				t.Fatal(err)
			}
			a, ok, err = takePublic(t, w, claims, pool, kernel.NewULID())
			if err != nil || !ok || a.Run != first {
				t.Fatalf("released public head was not selected: %+v %t %v", a, ok, err)
			}
		})
	}
}

type refusingPublicAssignment struct{ schedule.PublicClaimStore }

func (refusingPublicAssignment) AssignPublic(context.Context, lifecycle.Run, kernel.ULID, kernel.ULID) error {
	return errInjected
}

func TestAccPublicAssignmentFailureRollsBackTransition(t *testing.T) {
	w := newWorld(t)
	org := kernel.NewOrganizationID()
	run := publicWaiting(t, w, w.acct, org, w.now)
	limits := publicAdmission(func(context.Context, kernel.OrganizationID, kernel.AccountID, string, func(context.Context) (featuresapi.Usage, error)) error {
		return nil
	})
	claims := schedule.NewClaims(w.store, w.lc, w).WithPublic(refusingPublicAssignment{w.store}, limits)
	if a, ok, err := takePublic(t, w, claims, kernel.NewULID(), kernel.NewULID()); ok || !errors.Is(err, errInjected) || !a.Run.IsZero() {
		t.Fatalf("failed assignment outcome: %+v %t %v", a, ok, err)
	}
	var state string
	var version int64
	var worker, pool *kernel.ULID
	var transitions int
	err := w.db.QueryRow(t.Context(), `SELECT state,state_version,worker_id,public_worker_pool_id,
  (SELECT count(*) FROM run_state_transitions WHERE run_id=$1) FROM runs WHERE id=$1`, run).Scan(&state, &version, &worker, &pool, &transitions)
	if err != nil || state != "READY" || version != 1 || worker != nil || pool != nil || transitions != 0 {
		t.Fatalf("failed assignment committed partial lifecycle: %s %d %v %v %d %v", state, version, worker, pool, transitions, err)
	}
	// The rollback releases its run lock before a later worker tries again.
	claims.WithPublic(w.store, limits)
	if a, ok, err := takePublic(t, w, claims, kernel.NewULID(), kernel.NewULID()); err != nil || !ok || a.Run != run {
		t.Fatalf("rolled-back run was not retryable: %+v %t %v", a, ok, err)
	}
}
