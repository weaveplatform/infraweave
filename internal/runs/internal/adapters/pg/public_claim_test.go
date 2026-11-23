package pg_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
)

func TestAccPublicClaimStorage(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	org, pool, worker := kernel.NewOrganizationID(), kernel.NewULID(), kernel.NewULID()
	secondAccount := kernel.NewAccountID()
	first := publicWaiting(t, w, w.acct, org, w.now.Add(-time.Minute))
	second := publicWaiting(t, w, secondAccount, org, w.now)
	probe, found, err := w.store.ProbePublicCandidate(ctx, pool, machine.Claimable(), nil, nil, nil, nil)
	if err != nil || !found || probe.ID != first || probe.Organization == nil || *probe.Organization != org {
		t.Fatalf("FIFO public candidate: %+v %t %v", probe, found, err)
	}
	if err := w.in(t, func(ctx context.Context) error {
		stale := probe
		stale.Version++
		if _, found, err := w.store.LockPublicCandidate(ctx, stale, pool, machine.Claimable()); err != nil || found {
			t.Fatalf("stale candidate accepted: %t %v", found, err)
		}
		run, found, err := w.store.LockPublicCandidate(ctx, probe, pool, machine.Claimable())
		if err != nil || !found {
			t.Fatalf("lock candidate: %t %v", found, err)
		}
		if err := w.store.AssignPublic(ctx, run, worker, pool, w.now); err != nil {
			return err
		}
		if err := w.store.AssignPublic(ctx, run, worker, kernel.NewULID(), w.now); !errors.Is(err, lifecycle.ErrRefused) {
			t.Fatalf("rebound public identity: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if active, err := w.store.ActivePool(ctx, pool); err != nil || active {
		t.Fatalf("waiting run counted active: %t %v", active, err)
	}
	if deadline, _ := w.deadline(t, first); !deadline.Equal(w.now.Truncate(time.Microsecond)) {
		t.Errorf("the public assignment recorded the preparing deadline %v, want %v", deadline, w.now)
	}
	if _, err := w.db.Exec(ctx, `UPDATE runs SET state='PREPARING' WHERE id=$1`, first); err != nil {
		t.Fatal(err)
	}
	if active, err := w.store.ActivePool(ctx, pool); err != nil || !active {
		t.Fatalf("orphaned active assignment missed: %t %v", active, err)
	}
	if held, err := w.store.HoldingPublic(ctx, worker); err != nil || !held {
		t.Fatalf("public worker assignment missed: %t %v", held, err)
	}
	usage, err := w.store.PublicUsage(ctx, org, secondAccount)
	if err != nil || usage.Organization != 1 || usage.Account != 0 {
		t.Fatalf("cross-account usage: %+v %v", usage, err)
	}
	probe, found, err = w.store.ProbePublicCandidate(ctx, pool, machine.Claimable(), nil, nil, nil, nil)
	if err != nil || !found || probe.ID != second {
		t.Fatalf("cross-account candidate: %+v %t %v", probe, found, err)
	}
	for _, excluded := range []struct {
		stacks   []kernel.ULID
		orgs     []kernel.OrganizationID
		accounts []kernel.AccountID
	}{
		{stacks: []kernel.ULID{w.stack}}, {orgs: []kernel.OrganizationID{org}}, {accounts: []kernel.AccountID{secondAccount}},
	} {
		if _, found, err := w.store.ProbePublicCandidate(ctx, pool, machine.Claimable(), nil, excluded.stacks, excluded.orgs, excluded.accounts); err != nil || found {
			t.Fatalf("excluded candidate selected: %t %v", found, err)
		}
	}
	if _, err := w.db.Exec(ctx, `UPDATE runs SET state='FINISHED' WHERE id=$1`, first); err != nil {
		t.Fatal(err)
	}
	if active, err := w.store.ActivePool(ctx, pool); err != nil || active {
		t.Fatalf("terminal run retained public ownership: %t %v", active, err)
	}
}

func TestAccRunOrganizationSnapshot(t *testing.T) {
	for _, mode := range []string{"resolved", "missing", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			w := newWorld(t)
			org := kernel.NewOrganizationID()
			calls := 0
			w.lc = lifecycle.New(lifecycle.Deps{
				Store: w.store, Stacks: w.stacks, Events: w.events, Jobs: w.jobs, Evidence: w.store,
				Now: func() time.Time { return w.now },
				AccountOrganization: func(_ context.Context, account kernel.AccountID) (kernel.OrganizationID, error) {
					calls++
					if account != w.acct {
						t.Fatalf("resolved wrong account: %s", account)
					}
					if mode == "unavailable" {
						return kernel.OrganizationID{}, kernel.ErrUnavailable
					}
					if mode == "missing" {
						return kernel.OrganizationID{}, nil
					}
					return org, nil
				},
			})
			w.lc.BindSources(w.sources)
			run, created, err := w.create(t, kernel.RunTypeTracked, "abc")
			if calls != 1 {
				t.Fatalf("organization resolutions=%d", calls)
			}
			if mode != "resolved" {
				want := lifecycle.ErrAccountMissing
				if mode == "unavailable" {
					want = kernel.ErrUnavailable
				}
				if created || !errors.Is(err, want) {
					t.Fatalf("invalid organization created run: %t %v", created, err)
				}
				return
			}
			if err != nil || !created || run.Organization == nil || *run.Organization != org {
				t.Fatalf("organization snapshot: %+v %t %v", run, created, err)
			}
			got, err := w.store.Lock(t.Context(), w.acct, run.ID)
			if err != nil || got.Organization == nil || *got.Organization != org {
				t.Fatalf("persisted organization: %+v %v", got, err)
			}
		})
	}
}

func TestAccPublicClaimStorageUnavailable(t *testing.T) {
	w := newWorld(t)
	w.db.Close()
	ctx := t.Context()
	id := kernel.NewULID()
	run := lifecycle.Run{Account: w.acct, ID: id, Stack: w.stack, Version: 1}
	_, holding := w.store.HoldingPublic(ctx, id)
	_, active := w.store.ActivePool(ctx, id)
	_, usage := w.store.PublicUsage(ctx, kernel.NewOrganizationID(), w.acct)
	_, _, probe := w.store.ProbePublicCandidate(ctx, id, machine.Claimable(), nil, nil, nil, nil)
	_, _, lock := w.store.LockPublicCandidate(ctx, run, id, machine.Claimable())
	for name, err := range map[string]error{"holding": holding, "active": active, "usage": usage, "probe": probe, "lock": lock, "assign": w.store.AssignPublic(ctx, run, id, id, time.Now())} {
		if err == nil {
			t.Errorf("%s on a closed pool succeeded", name)
		}
	}
}

func publicWaiting(t *testing.T, w *world, account kernel.AccountID, org kernel.OrganizationID, at time.Time) kernel.ULID {
	t.Helper()
	id := w.waits(t, account, kernel.NewULID(), kernel.RunStateReady, kernel.RunTypeProposed, kernel.RunPriorityNormal, false, at)
	if _, err := w.db.Exec(t.Context(), `UPDATE runs SET organization_id=$2,worker_pool_id=NULL WHERE id=$1`, id, org); err != nil {
		t.Fatal(err)
	}
	return id
}
