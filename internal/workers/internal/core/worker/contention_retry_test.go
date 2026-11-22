package worker_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/platform/tx"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/worker"
)

type (
	retryWindowDB struct {
		active  atomic.Bool
		commits atomic.Int32
	}
	retryWindowTx struct {
		fakeTx
		db *retryWindowDB
	}
)

func (d *retryWindowDB) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	d.active.Store(true)
	return retryWindowTx{db: d}, nil
}

func (t retryWindowTx) Commit(context.Context) error {
	t.db.active.Store(false)
	t.db.commits.Add(1)
	return nil
}
func (t retryWindowTx) Rollback(context.Context) error { t.db.active.Store(false); return nil }

type contentionScheduler struct {
	*scheduler
	t        *testing.T
	db       *retryWindowDB
	delay    time.Duration
	misses   int
	attempts atomic.Int32
}

func (s *contentionScheduler) Claim(ctx context.Context, c workersapi.Claim) (workersapi.Assignment, bool, error) {
	attempts := s.attempts.Add(1)
	if !tx.In(ctx) || !s.db.active.Load() {
		s.t.Error("claim escaped its transaction")
	}
	if s.misses < 0 || int(attempts) <= s.misses {
		return workersapi.Assignment{Retry: true, RetryAfter: s.delay}, false, nil
	}
	return s.scheduler.Claim(ctx, c)
}

// These ports isolate waiting/termination contracts. The app acceptance tests
// separately hold actual PostgreSQL owner locks and execute real worker claims.
func TestUnitWorkerContentionWaitReleasesConnectionAndHonorsWake(t *testing.T) {
	for _, early := range []bool{false, true} {
		t.Run(map[bool]string{false: "timer", true: "pool wake"}[early], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				db := &retryWindowDB{}
				f := newFixture(t, worker.Deps{DB: db})
				sch := &contentionScheduler{scheduler: &scheduler{}, t: t, db: db, delay: time.Second, misses: 1}
				f.svc.BindScheduler(sch)
				_, b := f.add()
				allowed := sch.wait()
				out := f.poll(b, worker.PollInput{})
				pending(t, out)
				if db.active.Load() || db.commits.Load() != 1 || sch.attempts.Load() != 1 {
					t.Fatalf("retry held connection or spun: active=%t commits=%d attempts=%d", db.active.Load(), db.commits.Load(), sch.attempts.Load())
				}
				<-time.After(250 * time.Millisecond)
				pending(t, out)
				if db.active.Load() || sch.attempts.Load() != 1 {
					t.Fatal("contention polled during bounded wait")
				}
				want := time.Second
				if early {
					f.svc.Wake(f.pool.ID)
					want = 250 * time.Millisecond
				}
				got := <-out
				if !claimed(got, allowed) || got.took != want || sch.attempts.Load() != 2 || db.active.Load() || db.commits.Load() != 2 {
					t.Fatalf("retry result=%+v attempts=%d active=%t commits=%d", got, sch.attempts.Load(), db.active.Load(), db.commits.Load())
				}
			})
		})
	}
}

func TestUnitWorkerContentionWaitKeepsOriginalPollDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db := &retryWindowDB{}
		f := newFixture(t, worker.Deps{DB: db})
		sch := &contentionScheduler{scheduler: &scheduler{}, t: t, db: db, delay: 2 * worker.MinWait, misses: -1}
		f.svc.BindScheduler(sch)
		_, b := f.add()
		got := <-f.poll(b, worker.PollInput{Wait: worker.MinWait})
		if got.err != nil || got.got.Assignment != nil || got.took != worker.MinWait || sch.attempts.Load() != 1 || db.active.Load() {
			t.Fatalf("contention extended original hold: %+v attempts=%d active=%t", got, sch.attempts.Load(), db.active.Load())
		}
	})
}

func TestUnitWorkerContentionWaitEndsOnCancellationOrShutdown(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancellation", true: "shutdown"}[shutdown], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				db := &retryWindowDB{}
				f := newFixture(t, worker.Deps{DB: db})
				sch := &contentionScheduler{scheduler: &scheduler{}, t: t, db: db, delay: time.Hour, misses: -1}
				f.svc.BindScheduler(sch)
				_, b := f.add()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				out := make(chan held, 1)
				go func() {
					start := time.Now()
					result, err := f.svc.Poll(ctx, b, worker.PollInput{})
					out <- held{got: result, err: err, took: time.Since(start)}
				}()
				pending(t, out)
				if shutdown {
					f.svc.Stop()
				} else {
					cancel()
				}
				got := <-out
				if (!shutdown && !errors.Is(got.err, context.Canceled)) || (shutdown && got.err != nil) || got.got.Assignment != nil || sch.attempts.Load() != 1 ||
					db.active.Load() {
					t.Fatalf("contention ignored end: %+v attempts=%d active=%t", got, sch.attempts.Load(), db.active.Load())
				}
			})
		})
	}
}

func TestUnitWorkerContentionWaitEndsWhenAnotherPollSupersedesIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db := &retryWindowDB{}
		f := newFixture(t, worker.Deps{DB: db})
		sch := &contentionScheduler{scheduler: &scheduler{}, t: t, db: db, delay: time.Hour, misses: 1}
		f.svc.BindScheduler(sch)
		_, b := f.add()
		allowed := sch.wait()
		first := f.poll(b, worker.PollInput{})
		pending(t, first)
		second := f.poll(b, worker.PollInput{})
		if got := <-first; got.err != nil || got.got.Assignment != nil {
			t.Fatalf("superseded contention poll: %+v", got)
		}
		if got := <-second; !claimed(got, allowed) || sch.attempts.Load() != 2 {
			t.Fatalf("replacement poll: %+v attempts=%d", got, sch.attempts.Load())
		}
	})
}

func TestUnitWorkerContentionRetryReturnsRenewalWithoutWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db := &retryWindowDB{}
		f := newFixture(t, worker.Deps{DB: db})
		sch := &contentionScheduler{scheduler: &scheduler{}, t: t, db: db, delay: time.Hour, misses: -1}
		f.svc.BindScheduler(sch)
		_, b := f.add()
		b.Expires = time.Now().Add(time.Minute)
		got := <-f.poll(b, worker.PollInput{})
		if got.err != nil || got.got.Token == nil || got.got.Assignment != nil || got.took != 0 || sch.attempts.Load() != 1 || db.active.Load() {
			t.Fatalf("contention delayed renewal: %+v attempts=%d active=%t", got, sch.attempts.Load(), db.active.Load())
		}
	})
}
