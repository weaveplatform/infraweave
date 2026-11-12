package worker

import (
	"context"
	"errors"
	"sync"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// SignalCycle asks a launcher to exit: at once when idle, after its run
// when busy.
const SignalCycle = "cycle"

// PollInput is what a poll reports: whether the launcher is busy, the
// drain flag it last learned, and the hold it asks for, DefaultWait when
// zero.
type PollInput struct {
	Busy    bool
	Drained bool
	Wait    time.Duration
}

// PollResult is a poll's answer: the signals for the worker, its drain
// flag, and a renewed session token when the poll's had less than
// RenewWithin left.
type PollResult struct {
	Signals []string
	Drained bool
	Token   *accessapi.Token
}

func result(w Worker) PollResult {
	out := PollResult{Drained: w.Drained}
	if w.Cycle {
		out.Signals = []string{SignalCycle}
	}
	return out
}

// Poll marks the session's worker seen with in's state and answers what
// the server has to say to it. It answers at once with a renewed token
// when the session's has less than RenewWithin left, with a cycle signal,
// or when the worker's drain flag is not the one in reports; otherwise it
// holds the request, with no transaction or pooled connection open, until
// the worker is cycled or its drain flag changes, the same worker's next
// poll reaches this replica, or its hold ends, so a worker holds at most
// one poll per replica. A worker that is gone, or that registered under a
// certificate a reset replaced, is ErrUnauthenticated.
//
// ref: decision 0008 § "The worker API"
func (s *Service) Poll(ctx context.Context, b accessapi.Bearer, in PollInput) (PollResult, error) {
	now := s.now()
	w, serial, err := s.store.Seen(ctx, b.ID, in.Busy, now)
	if err != nil {
		return PollResult{}, s.unauthenticated(err)
	}
	if w.Serial != serial {
		return PollResult{}, refuse("worker %s registered under a replaced certificate", w.ID)
	}
	if b.Expires.Sub(now) < RenewWithin {
		out := result(w)
		t, err := s.tokens.WorkerToken(ctx, w.ID)
		if err != nil {
			return PollResult{}, err //nolint:wrapcheck // the access domain names the mint
		}
		out.Token = &t
		return out, nil
	}
	if w.Cycle || w.Drained != in.Drained {
		return result(w), nil
	}
	// The worker is reread after the wake-up is registered, so a change
	// between Seen and the registration still ends the hold.
	wake, ended, done := s.hub.wait(w.Pool, w.ID)
	defer done()
	timer := time.NewTimer(s.hold(in.Wait))
	defer timer.Stop()
	for {
		cur, serial, err := s.store.Worker(ctx, w.ID, false)
		if err != nil {
			return PollResult{}, s.unauthenticated(err)
		}
		if cur.Serial != serial {
			return PollResult{}, refuse("worker %s registered under a replaced certificate", w.ID)
		}
		if cur.Cycle || cur.Drained != in.Drained || s.hub.stopped() {
			return result(cur), nil
		}
		select {
		case <-wake:
		case <-ended:
			return result(cur), nil
		case <-timer.C:
			return result(cur), nil
		case <-ctx.Done():
			return PollResult{}, ctx.Err() //nolint:wrapcheck // the request ended
		}
	}
}

// hold is how long a poll that asks for wait is held: DefaultWait when
// zero, within MinWait and MaxWait, and no longer than the server allows.
func (s *Service) hold(wait time.Duration) time.Duration {
	if wait == 0 {
		wait = DefaultWait
	}
	return min(max(wait, MinWait), MaxWait, s.pollMax)
}

// unauthenticated is a worker the store does not find as
// ErrUnauthenticated, and any other error as it is.
func (s *Service) unauthenticated(err error) error {
	if errors.Is(err, ErrNotFound) {
		return refuse("%v", err)
	}
	return err
}

// Deregister removes the session's worker, whose session then
// authenticates nothing; ErrUnauthenticated when it is gone already.
func (s *Service) Deregister(ctx context.Context, b accessapi.Bearer) error {
	return s.unauthenticated(s.store.Remove(ctx, b.ID))
}

// Wake ends the hold of every poll held for pool, each of which rereads
// its worker and goes on holding when nothing changed.
func (s *Service) Wake(pool kernel.ULID) { s.hub.wake(pool) }

// WakeAll ends the hold of every held poll, as Wake does, for a wake-up
// that may have been missed.
func (s *Service) WakeAll() { s.hub.wakeAll() }

// Stop answers every held poll at once, and every later one without a
// hold, so a replica that is shutting down drains its held requests
// within its shutdown timeout and its launchers poll another replica.
func (s *Service) Stop() {
	s.hub.mu.Lock()
	s.hub.stopping = true
	s.hub.mu.Unlock()
	s.hub.wakeAll()
}

// hub is this replica's held polls, by pool, and the channel that ends
// each worker's latest one.
type hub struct {
	mu       sync.Mutex
	waiting  map[kernel.ULID]map[chan struct{}]struct{}
	held     map[kernel.ULID]chan struct{}
	stopping bool
}

func (h *hub) stopped() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stopping
}

func newHub() *hub {
	return &hub{
		waiting: map[kernel.ULID]map[chan struct{}]struct{}{},
		held:    map[kernel.ULID]chan struct{}{},
	}
}

// wait registers worker's held poll for pool, ending the worker's earlier
// one: wake receives on each wake-up, ended is closed when the worker's
// next poll registers, and done unregisters it.
func (h *hub) wait(pool, worker kernel.ULID) (wake, ended <-chan struct{}, done func()) {
	ch, end := make(chan struct{}, 1), make(chan struct{})
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.waiting[pool] == nil {
		h.waiting[pool] = map[chan struct{}]struct{}{}
	}
	h.waiting[pool][ch] = struct{}{}
	if old, ok := h.held[worker]; ok {
		close(old)
	}
	h.held[worker] = end
	return ch, end, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.waiting[pool], ch)
		if len(h.waiting[pool]) == 0 {
			delete(h.waiting, pool)
		}
		if h.held[worker] == end {
			delete(h.held, worker)
		}
	}
}

func (h *hub) wake(pool kernel.ULID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.waiting[pool] {
		signal(ch)
	}
}

func (h *hub) wakeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, chs := range h.waiting {
		for ch := range chs {
			signal(ch)
		}
	}
}

// signal sends on ch unless a wake-up is pending already, which the poll
// rereads for as well.
func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}
