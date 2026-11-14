package worker_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/worker"
)

// signals is the runs domain's side of a poll's signals: each worker's
// signals not yet delivered, first first, which Deliver hands out once
// each, and asked, the workers it was asked for. fail makes Deliver fail
// with errDown; a call outside a transaction fails.
type signals struct {
	mu      sync.Mutex
	pending map[kernel.ULID][]workersapi.Signal
	asked   []kernel.ULID
	fail    bool
}

func (s *signals) Deliver(ctx context.Context, w kernel.ULID) (workersapi.Signal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked = append(s.asked, w)
	switch {
	case !tx.In(ctx):
		return "", errNoTx
	case s.fail:
		return "", errDown
	case len(s.pending[w]) == 0:
		return "", nil
	}
	sig := s.pending[w][0]
	s.pending[w] = s.pending[w][1:]
	return sig, nil
}

func (s *signals) send(w kernel.ULID, sig workersapi.Signal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[w] = append(s.pending[w], sig)
}

// TestUnitWorkerPollRunSignals checks that a busy worker's held poll
// answers its run's stop and kill once each when its pool's polls are
// woken, that an idle worker's poll asks for none, and that a failing
// delivery fails the poll.
func TestUnitWorkerPollRunSignals(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, worker.Deps{})
		sig := &signals{pending: map[kernel.ULID][]workersapi.Signal{}}
		f.svc.BindSignals(sig)
		w, b := f.add()
		busy := worker.PollInput{Busy: true}

		ch := f.poll(b, busy)
		pending(t, ch)
		<-time.After(time.Second)
		sig.send(w.ID, workersapi.SignalStop)
		f.svc.Wake(w.Pool)
		if h := <-ch; h.err != nil || h.took != time.Second ||
			!slices.Equal(h.got.Signals, []string{"stop"}) {
			t.Errorf("stopped = %+v", h)
		}
		// The stop was delivered: the next poll holds.
		ch = f.poll(b, busy)
		pending(t, ch)
		sig.send(w.ID, workersapi.SignalKill)
		f.svc.Wake(w.Pool)
		if h := <-ch; h.err != nil || h.took != 0 ||
			!slices.Equal(h.got.Signals, []string{"kill"}) {
			t.Errorf("killed = %+v", h)
		}

		// An idle worker holds no run to signal.
		idle, ib := f.add()
		sig.send(idle.ID, workersapi.SignalStop)
		if h := <-f.poll(ib, worker.PollInput{Wait: worker.MinWait}); h.err != nil ||
			len(h.got.Signals) != 0 || slices.Contains(sig.asked, idle.ID) {
			t.Errorf("idle = %+v, asked %v", h, sig.asked)
		}

		sig.mu.Lock()
		sig.fail = true
		sig.mu.Unlock()
		if h := <-f.poll(b, busy); !errors.Is(h.err, errDown) {
			t.Errorf("delivery down = %+v", h)
		}
	})
}
