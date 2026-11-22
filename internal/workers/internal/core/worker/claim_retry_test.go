package worker_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/platform/tx"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/worker"
)

type refusalScheduler struct {
	*scheduler
	t         *testing.T
	f         *fixture
	committed bool
	attempts  int
}

func (s *refusalScheduler) Claim(ctx context.Context, claim workersapi.Claim) (workersapi.Assignment, bool, error) {
	s.attempts++
	if !tx.In(ctx) {
		s.t.Error("claim without transaction")
	}
	if s.attempts == 1 {
		tx.AfterCommit(ctx, func(context.Context) { s.committed = true })
		return workersapi.Assignment{Retry: true}, false, nil
	}
	if !s.committed || s.f.store.workers[claim.Worker].Busy {
		s.t.Error("selection retried before refusal committed or marked worker busy")
	}
	return s.scheduler.Claim(ctx, claim)
}

type refusalCommitDB struct{ commits int }

type refusalCommitTx struct {
	fakeTx
	db *refusalCommitDB
}

func (d *refusalCommitDB) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	return refusalCommitTx{db: d}, nil
}

func (t refusalCommitTx) Commit(context.Context) error {
	t.db.commits++
	if t.db.commits == 2 {
		return errDown
	}
	return nil
}

// Real run refusal and assignment are covered by the worker acceptance suite;
// these fault-injected ports isolate commit ordering and commit failure.
func TestUnitWorkerPollCommitsRefusalBeforeRetryingClaim(t *testing.T) {
	for _, fails := range []bool{false, true} {
		t.Run(map[bool]string{false: "committed", true: "commit fails"}[fails], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				d := worker.Deps{}
				if fails {
					d.DB = &refusalCommitDB{}
				}
				f := newFixture(t, d)
				sch := &refusalScheduler{scheduler: &scheduler{}, t: t, f: f}
				f.svc.BindScheduler(sch)
				w, b := f.add()
				allowed := sch.wait()
				got := <-f.poll(b, worker.PollInput{})
				if fails {
					if !errors.Is(got.err, errDown) || got.got.Assignment != nil || sch.committed || sch.attempts != 1 || f.store.workers[w.ID].Busy {
						t.Fatalf("failed refusal commit: result=%+v attempts=%d committed=%t busy=%t", got, sch.attempts, sch.committed, f.store.workers[w.ID].Busy)
					}
					return
				}
				if !claimed(got, allowed) || sch.attempts != 2 || !sch.committed || !f.store.workers[w.ID].Busy {
					t.Fatalf("committed refusal then allowed claim: result=%+v attempts=%d committed=%t busy=%t", got, sch.attempts, sch.committed, f.store.workers[w.ID].Busy)
				}
			})
		})
	}
}

func TestUnitWorkerPollRefusalPreservesRenewal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, worker.Deps{})
		sch := &refusalScheduler{scheduler: &scheduler{}, t: t, f: f}
		f.svc.BindScheduler(sch)
		w, b := f.add()
		b.Expires = time.Now().Add(time.Minute)
		got := <-f.poll(b, worker.PollInput{})
		if got.err != nil || got.got.Token == nil || got.got.Assignment != nil || sch.attempts != 1 || !sch.committed || f.store.workers[w.ID].Busy {
			t.Fatalf("refusal lost renewal or delayed delivery: result=%+v attempts=%d committed=%t busy=%t", got, sch.attempts, sch.committed, f.store.workers[w.ID].Busy)
		}
	})
}
