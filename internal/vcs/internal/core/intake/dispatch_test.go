package intake

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type preparedDispatch struct {
	commit func(context.Context) error
}

func (p preparedDispatch) Commit(ctx context.Context) error { return p.commit(ctx) }

type dispatcher struct {
	prepare func(context.Context, Delivery, []vcsapi.Event) (DispatchPlan, error)
}

func (d dispatcher) Prepare(ctx context.Context, delivery Delivery, events []vcsapi.Event) (DispatchPlan, error) {
	return d.prepare(ctx, delivery, events)
}

func TestUnitDispatchFenceAndPreparation(t *testing.T) {
	for _, fault := range []string{"ok", "lost claim", "commit", "prepare", "last prepare"} {
		t.Run(fault, func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.receive(t, []byte(`{"type":"push"}`)); err != nil {
				t.Fatal(err)
			}
			prepared, committed := 0, 0
			f.svc.d.Dispatcher = dispatcher{prepare: func(ctx context.Context, delivery Delivery, events []vcsapi.Event) (DispatchPlan, error) {
				prepared++
				if tx.In(ctx) || len(events) != 1 || events[0].Type != "push" || delivery.ID != f.store.d.ID {
					t.Fatal("preparation held a transaction or lost normalized delivery")
				}
				if fault == "prepare" || fault == "last prepare" {
					return nil, errors.New("current-secret provider body")
				}
				return preparedDispatch{commit: func(ctx context.Context) error {
					committed++
					if !tx.In(ctx) || f.jobs.held != 1 || f.jobs.complete != 0 || f.store.d.State != Received {
						t.Fatal("prepared child effects escaped the completion fence/order")
					}
					if fault == "commit" {
						return errDown
					}
					return nil
				}}, nil
			}}
			j := f.job()
			if fault == "last prepare" {
				j.Attempt = j.MaxAttempts
			}
			if fault == "lost claim" {
				f.jobs.hold = queue.ErrLeaseLost
			}
			err := f.svc.Processed(t.Context(), j)
			if prepared != 1 || (err != nil) != (fault != "ok") {
				t.Fatal("dispatch lost preparation/failure", prepared, err)
			}
			if fault == "lost claim" && committed != 0 {
				t.Fatal("stale worker committed prepared children")
			}
			if fault == "ok" && (committed != 1 || f.store.d.State != Dispatched || f.jobs.complete != 1) {
				t.Fatal("prepared work did not share dispatch completion")
			}
			if fault == "prepare" || fault == "last prepare" {
				if err == nil || strings.Contains(err.Error(), "current-secret") || f.store.d.LastError != errDispatch.Error() || committed != 0 {
					t.Fatal("unsafe preparation failure escaped", err)
				}
			}
		})
	}
}

func TestUnitPreparationDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.receive(t, []byte(`{"type":"push"}`)); err != nil {
			t.Fatal(err)
		}
		f.svc.d.Dispatcher = dispatcher{prepare: func(context.Context, Delivery, []vcsapi.Event) (DispatchPlan, error) {
			time.Sleep(11 * time.Minute) //nolint:forbidigo // synctest advances virtual time, never the wall clock
			return preparedDispatch{commit: func(context.Context) error {
				t.Error("expired preparation enqueued children")
				return nil
			}}, nil
		}}
		if err := f.svc.Processed(t.Context(), f.job()); err != nil || f.store.d.State != Expired || f.jobs.complete != 1 {
			t.Fatal("expired prepared delivery did not settle", err)
		}
	})
}

func TestUnitPreparationRateLimit(t *testing.T) {
	f := newFixture(t)
	if _, err := f.receive(t, []byte(`{"type":"push"}`)); err != nil {
		t.Fatal(err)
	}
	f.svc.d.Dispatcher = dispatcher{prepare: func(context.Context, Delivery, []vcsapi.Event) (DispatchPlan, error) {
		return nil, &port.RateLimited{Reset: time.Now().Add(time.Minute)}
	}}
	err := f.svc.Processed(t.Context(), f.job())
	if err == nil || !strings.HasPrefix(err.Error(), "snoozed for ") || f.store.d.State != Received || f.store.d.LastError != "" || f.jobs.complete != 0 {
		t.Fatal("provider rate limit was lost or recorded as a failed parse", err)
	}
}
