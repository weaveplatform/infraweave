package worker

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
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

// PollResult is a poll's answer: the run it claimed for the worker, the
// signals for the worker, its drain flag, and a renewed session token
// when the poll's had less than RenewWithin left.
type PollResult struct {
	Assignment *Assignment
	Signals    []string
	Drained    bool
	Token      *accessapi.Token
}

// Assignment is a run a poll claimed, the preparing state the claim moved
// it to, the run's state version after it, and the run token its launcher
// and runner carry.
type Assignment struct {
	Run          kernel.ULID
	State        kernel.RunState
	StateVersion int64
	Token        accessapi.Token
}

func result(w Worker) PollResult {
	out := PollResult{Drained: w.Drained}
	if w.Cycle {
		out.Signals = []string{SignalCycle}
	}
	return out
}

// Poll marks the session's worker seen with in's state and answers what
// the server has to say to it. An idle, undrained worker's poll first
// claims its pool's next run, and answers at once with it. A poll also
// answers at once with a renewed token when the session's has less than
// RenewWithin left, with a cycle signal, or when the worker's drain flag
// is not the one in reports; otherwise it holds the request, with no
// transaction or pooled connection open, until a wake-up for its pool,
// which it claims again on, the worker is cycled or its drain flag
// changes, the same worker's next poll reaches this replica, or its hold
// ends, so a worker holds at most one poll per replica. A worker that is
// gone, or that registered under a certificate a reset replaced, is
// ErrUnauthenticated.
//
// ref: decision 0008 § "The worker API", § "Claiming runs"
func (s *Service) Poll(ctx context.Context, b accessapi.Bearer, in PollInput) (PollResult, error) {
	now := s.now()
	w, serial, err := s.store.Seen(ctx, b.ID, in.Busy, now)
	if err != nil {
		return PollResult{}, s.unauthenticated(err)
	}
	if w.Serial != serial {
		return PollResult{}, refuse("worker %s registered under a replaced certificate", w.ID)
	}
	var renewed *accessapi.Token
	if b.Expires.Sub(now) < RenewWithin {
		t, err := s.tokens.WorkerToken(ctx, w.ID)
		if err != nil {
			return PollResult{}, err //nolint:wrapcheck // the access domain names the mint
		}
		renewed = &t
	}
	// The worker is reread and the claim tried after the wake-up is
	// registered, so a change, or a run made ready, between Seen and the
	// registration still ends the hold.
	wake, ended, done := s.hub.wait(w.Pool, waiter{
		worker: w.ID, registered: w.CreatedAt, claimant: w.idle(),
	})
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
		out := result(cur)
		out.Token = renewed
		a, ok, err := s.claim(ctx, cur)
		if err != nil {
			return PollResult{}, err
		}
		if ok {
			out.Assignment = &a
		}
		if out.Assignment != nil || out.Token != nil || cur.Cycle || cur.Drained != in.Drained ||
			s.hub.stopped() {
			return out, nil
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

// idle reports whether w may be assigned a run: it is neither busy,
// drained nor cycled.
func (w Worker) idle() bool { return !w.Busy && !w.Drained && !w.Cycle }

// claim claims the next run of w's pool for w through the scheduler
// internal/app binds, and reports false when w is not idle or none waits.
// It locks w's row before it claims, so polls of one worker on two
// replicas claim one after the other and the second finds the worker
// busy; a run token that cannot be minted rolls the claim back.
func (s *Service) claim(ctx context.Context, w Worker) (Assignment, bool, error) {
	if s.scheduler == nil || !w.idle() {
		return Assignment{}, false, nil
	}
	var out *Assignment
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		out = nil
		cur, _, err := s.store.Worker(ctx, w.ID, true)
		if err != nil || !cur.idle() {
			return err //nolint:wrapcheck // the store names the read
		}
		p, err := s.store.Get(ctx, cur.Account, cur.Pool, false)
		if err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		c := workersapi.Claim{Account: cur.Account, Pool: cur.Pool, Worker: cur.ID}
		if p.DriftDetectionRunLimit != nil {
			limit := int(*p.DriftDetectionRunLimit)
			c.DriftLimit = &limit
		}
		a, ok, err := s.scheduler.Claim(ctx, c)
		if err != nil || !ok {
			return err //nolint:wrapcheck // the runs domain names the claim
		}
		if err := s.store.Claimed(ctx, cur.ID); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		t, err := s.tokens.RunToken(ctx, a.Run)
		if err != nil {
			return err //nolint:wrapcheck // the access domain names the mint
		}
		out = &Assignment{Run: a.Run, State: a.State, StateVersion: a.StateVersion, Token: t}
		return nil
	})
	if err != nil || out == nil {
		return Assignment{}, false, s.unauthenticated(err)
	}
	return *out, true, nil
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
// its worker, claims a run when idle, and goes on holding when nothing
// changed.
func (s *Service) Wake(pool kernel.ULID) { s.hub.wake(pool) }

// WakeOne ends the hold of the poll held for pool that a waiting run
// goes to: the idle worker's that registered last, which approximates
// Spacelift's newest-first assignment. Each replica wakes one, and a
// woken poll that loses its claim goes on holding.
//
// ref: decision 0008 § "Claiming runs"
func (s *Service) WakeOne(pool kernel.ULID) { s.hub.wakeOne(pool) }

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
	waiting  map[kernel.ULID]map[chan struct{}]waiter
	held     map[kernel.ULID]chan struct{}
	stopping bool
}

// waiter is a held poll's worker, when it registered, and whether it was
// idle, so a waiting run may wake it.
type waiter struct {
	worker     kernel.ULID
	registered time.Time
	claimant   bool
}

// after reports whether w's worker registered after o's, the later ID
// first when both registered at once.
func (w waiter) after(o waiter) bool {
	if !w.registered.Equal(o.registered) {
		return w.registered.After(o.registered)
	}
	return bytes.Compare(w.worker[:], o.worker[:]) > 0
}

func (h *hub) stopped() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stopping
}

func newHub() *hub {
	return &hub{
		waiting: map[kernel.ULID]map[chan struct{}]waiter{},
		held:    map[kernel.ULID]chan struct{}{},
	}
}

// wait registers w's held poll for pool, ending its worker's earlier one:
// wake receives on each wake-up, ended is closed when the worker's next
// poll registers, and done unregisters it.
func (h *hub) wait(pool kernel.ULID, w waiter) (wake, ended <-chan struct{}, done func()) {
	ch, end := make(chan struct{}, 1), make(chan struct{})
	worker := w.worker
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.waiting[pool] == nil {
		h.waiting[pool] = map[chan struct{}]waiter{}
	}
	h.waiting[pool][ch] = w
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

func (h *hub) wakeOne(pool kernel.ULID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var (
		pick chan struct{}
		last waiter
	)
	for ch, w := range h.waiting[pool] {
		if w.claimant && (pick == nil || w.after(last)) {
			pick, last = ch, w
		}
	}
	if pick != nil {
		signal(pick)
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
