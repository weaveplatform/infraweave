package worker_test

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/worker"
)

func TestUnitWorkerGenerationRemainsRevokedAfterResume(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, worker.Deps{})
		w, b := f.add()
		current := w
		current.Serial++
		f.store.mu.Lock()
		p := f.store.pools[w.Pool]
		p.Serial = current.Serial
		f.store.pools[w.Pool] = p
		current.Busy, current.Offline = true, true
		current.LastSeenAt = time.Now().Add(-time.Hour)
		f.store.workers[w.ID] = current
		f.store.mu.Unlock()
		for _, serial := range []int64{0, b.WorkerSerial} {
			old := b
			old.WorkerSerial = serial
			old.Expires = time.Now().Add(time.Minute)
			if got := <-f.poll(old, worker.PollInput{Busy: false}); !errors.Is(got.err, worker.ErrUnauthenticated) || got.got.Token != nil || got.got.Assignment != nil {
				t.Fatalf("revoked poll: %+v", got)
			}
			if err := f.svc.Deregister(t.Context(), old); !errors.Is(err, worker.ErrUnauthenticated) {
				t.Fatal(err)
			}
			if got := f.store.workers[w.ID]; got.Serial != current.Serial || got.Busy != current.Busy || got.Offline != current.Offline ||
				!got.LastSeenAt.Equal(current.LastSeenAt) {
				t.Fatal("revoked bearer changed replacement worker")
			}
		}
		b.WorkerSerial = current.Serial
		b.Expires = time.Now().Add(time.Minute)
		for range 2 {
			got := <-f.poll(b, worker.PollInput{})
			if got.err != nil || got.got.Token == nil || got.got.Token.WorkerSerial != current.Serial {
				t.Fatalf("same-generation renewal/response-loss retry: %+v", got)
			}
		}
	})
}

func TestUnitHeldPollRechecksGenerationOnTimeoutAndSupersession(t *testing.T) {
	for _, superseded := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "superseded"}[superseded], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newFixture(t, worker.Deps{})
				w, b := f.add()
				old := f.poll(b, worker.PollInput{Busy: true, Wait: worker.MinWait})
				pending(t, old)
				f.store.mu.Lock()
				p := f.store.pools[w.Pool]
				p.Serial++
				f.store.pools[w.Pool] = p
				current := f.store.workers[w.ID]
				current.Serial = p.Serial
				f.store.workers[w.ID] = current
				f.store.mu.Unlock()
				var replacement <-chan held
				if superseded {
					fresh := accessapi.Bearer{ID: w.ID, WorkerSerial: p.Serial, Expires: b.Expires}
					replacement = f.poll(fresh, worker.PollInput{Busy: true})
					pending(t, replacement)
				}
				got := <-old
				if !errors.Is(got.err, worker.ErrUnauthenticated) {
					t.Fatalf("held stale request returned success: %+v", got)
				}
				if replacement != nil {
					f.svc.Stop()
					if got := <-replacement; got.err != nil {
						t.Fatal(got.err)
					}
				}
			})
		})
	}
}
