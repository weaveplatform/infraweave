package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
)

// SignalCycle asks a launcher to exit: at once when idle, after its run
// when busy.
const SignalCycle = "cycle"

// PollInput is what a poll reports: whether the launcher is busy, the
// drain flag it last learned, the hold it asks for, DefaultWait when
// zero, and, when not busy, the run whose container ended since its last
// poll and how it ended.
type PollInput struct {
	Busy    bool
	Drained bool
	Wait    time.Duration
	Run     *kernel.ULID
	Exit    *workersapi.Exit
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
// it to, the run's state version after it, the preparing deadline the
// claim issued, and the run token its launcher and runner carry.
type Assignment struct {
	Retry             bool
	RetryAfter        time.Duration
	Run               kernel.ULID
	State             kernel.RunState
	StateVersion      int64
	PreparingDeadline time.Time
	Token             accessapi.Token
}

func result(w Worker) PollResult {
	out := PollResult{Drained: w.Drained}
	if w.Cycle {
		out.Signals = []string{SignalCycle}
	}
	return out
}

// Poll marks the session's worker seen with in's state and answers what
// the server has to say to it. A poll that is not busy and names a run
// fails the run, when the worker still holds it, as one whose container
// ended without a report ending it. An idle, undrained worker's poll first
// claims its pool's next run, and answers at once with it; a busy
// worker's poll delivers its run's stop or kill, which it answers at once
// with. A poll also answers at once with a renewed token when the
// session's has less than RenewWithin left, with a cycle signal, or when
// the worker's drain flag is not the one in reports; otherwise it holds
// the request, with no transaction or pooled connection open, until a
// wake-up for its pool, which it claims and delivers again on, the worker
// is cycled or its drain flag changes, the same worker's next poll
// reaches this replica, or its hold ends, so a worker holds at most one
// poll per replica. A worker that is gone, or that registered under a
// certificate a reset replaced, is ErrUnauthenticated.
//
// ref: decision 0008 § "The worker API", § "Claiming runs", § "Stop and kill"
func (s *Service) Poll(ctx context.Context, b accessapi.Bearer, in PollInput) (PollResult, error) {
	w, renewed, err := s.beginPoll(ctx, b, in)
	if err != nil {
		return PollResult{}, err
	}
	// The worker is reread and the claim tried after the wake-up is
	// registered, so a change, or a run made ready, between Seen and the
	// registration still ends the hold.
	wake, ended, done := s.hub.wait(w.Pool, waiter{
		worker: w.ID, registered: w.CreatedAt, claimant: w.idle(), public: w.Public,
	})
	defer done()
	timer := time.NewTimer(s.hold(in.Wait))
	defer timer.Stop()
	for {
		cur, err := s.session(ctx, b, false)
		if err != nil {
			return PollResult{}, err
		}
		out := result(cur)
		out.Token = renewed
		a, ok, err := s.claim(ctx, cur, b)
		if err != nil {
			return PollResult{}, err
		}
		if a.Retry {
			if s.shouldAnswer(out, in) {
				return s.sessionReply(ctx, b, out)
			}
			again, err := retryPoll(ctx, a.RetryAfter, wake, ended, timer.C)
			if err != nil {
				return PollResult{}, err
			}
			if !again || s.hub.stopped() {
				return s.sessionReply(ctx, b, out)
			}
			continue
		}
		if ok {
			out.Assignment = &a
		}
		sig, err := s.deliver(ctx, cur, b)
		if err != nil {
			return PollResult{}, err
		}
		if sig != "" {
			out.Signals = append(out.Signals, string(sig))
		}
		if s.shouldAnswer(out, in) {
			return s.sessionReply(ctx, b, out)
		}
		select {
		case <-wake:
		case <-ended:
			return s.sessionReply(ctx, b, result(cur))
		case <-timer.C:
			return s.sessionReply(ctx, b, result(cur))
		case <-ctx.Done():
			return PollResult{}, ctx.Err() //nolint:wrapcheck // the request ended
		}
	}
}

// session checks the immutable bearer generation against both registration
// and current pool state. Locked checks acquire the pool before its worker.
func (s *Service) session(ctx context.Context, b accessapi.Bearer, lock bool) (Worker, error) {
	if b.WorkerSerial <= 0 {
		return Worker{}, refuse("worker token has no credential generation")
	}
	w, serial, err := s.store.Worker(ctx, b.ID, lock)
	if err != nil {
		return Worker{}, s.unauthenticated(err)
	}
	if b.WorkerSerial != w.Serial || b.WorkerSerial != serial {
		return Worker{}, refuse("worker %s credential generation is no longer current", b.ID)
	}
	return w, nil
}

// beginPoll fences all initial effects in one short transaction. Neither its
// pool/worker locks nor its connection survive into a long poll's wait.
func (s *Service) beginPoll(ctx context.Context, b accessapi.Bearer, in PollInput) (Worker, *accessapi.Token, error) {
	var w Worker
	var renewed *accessapi.Token
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		renewed = nil
		var err error
		w, err = s.session(ctx, b, true)
		if err != nil {
			return err
		}
		now := s.now()
		w, _, err = s.store.Seen(ctx, b.ID, b.WorkerSerial, in.Busy, now)
		if err != nil {
			return s.unauthenticated(err)
		}
		if !in.Busy && in.Run != nil {
			if err := s.ended(ctx, w.ID, *in.Run, in.Exit); err != nil {
				return err
			}
		}
		if b.Expires.Sub(now) < RenewWithin {
			t, err := s.tokens.WorkerToken(ctx, w.ID, b.WorkerSerial)
			if err != nil {
				return err //nolint:wrapcheck // access names token issuance
			}
			renewed = &t
		}
		return nil
	})
	return w, renewed, err //nolint:wrapcheck // every failure is classified above
}

// sessionReply rechecks generation even when a timeout or superseding poll ends
// the wait without another loop iteration. A cached worker is not authority.
func (s *Service) sessionReply(ctx context.Context, b accessapi.Bearer, out PollResult) (PollResult, error) {
	if _, err := s.session(ctx, b, false); err != nil {
		return PollResult{}, err
	}
	return out, nil
}

// shouldAnswer applies the same delivery rules before normal holds and before
// contention backoff, so a retry cannot hide a renewal, signal or drain change.
func (s *Service) shouldAnswer(out PollResult, in PollInput) bool {
	return out.Assignment != nil || out.Token != nil || len(out.Signals) > 0 || out.Drained != in.Drained || s.hub.stopped()
}

// retryPoll waits outside the claim transaction. The original poll deadline
// bounds contention retries; a wake may retry sooner, and a superseding poll,
// cancellation or shutdown can end the wait without selecting another run.
func retryPoll(ctx context.Context, delay time.Duration, wake, ended <-chan struct{}, hold <-chan time.Time) (bool, error) {
	next := time.NewTimer(max(0, delay))
	defer next.Stop()
	select {
	case <-next.C:
	case <-wake:
	case <-ended:
		return false, nil
	case <-hold:
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err() //nolint:wrapcheck // the request ended
	}
	// A wake and the deadline may become ready together. An ended poll
	// takes precedence over another database claim in that case.
	select {
	case <-ended:
		return false, nil
	case <-hold:
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err() //nolint:wrapcheck // the request ended
	default:
		return true, nil
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
func (s *Service) claim(ctx context.Context, w Worker, b accessapi.Bearer) (Assignment, bool, error) {
	if s.scheduler == nil || !w.idle() || w.Public && !s.publicPoolEnabled {
		return Assignment{}, false, nil
	}
	var out *Assignment
	var retry bool
	var retryAfter time.Duration
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		out = nil
		retry = false
		retryAfter = 0
		var publicPool pool.Pool
		if w.Public {
			var err error
			publicPool, err = s.store.PublicShare(ctx)
			if err != nil {
				return err //nolint:wrapcheck // the store names the owner lock
			}
		}
		cur, err := s.session(ctx, b, true)
		if err != nil || !cur.idle() {
			return err //nolint:wrapcheck // the store names the read
		}
		p := publicPool
		if cur.Public {
			if p.ID != cur.Pool || p.Serial != cur.Serial {
				return refuse("public worker %s has stale pool credentials", cur.ID)
			}
		} else {
			p, err = s.store.Get(ctx, cur.Account, cur.Pool, false)
			if err != nil {
				return err //nolint:wrapcheck // the store names the read
			}
		}
		c := workersapi.Claim{
			Account: cur.Account, Pool: cur.Pool, Worker: cur.ID, Public: cur.Public,
			Preparing: s.preparing(p, cur.Public),
		}
		if p.DriftDetectionRunLimit != nil {
			limit := int(*p.DriftDetectionRunLimit)
			c.DriftLimit = &limit
		}
		a, ok, err := s.scheduler.Claim(ctx, c)
		if err != nil || !ok {
			retry = a.Retry && err == nil
			retryAfter = a.RetryAfter
			return err //nolint:wrapcheck // the runs domain names the claim
		}
		if err := s.store.Claimed(ctx, cur.ID); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		t, err := s.tokens.RunToken(ctx, a.Run)
		if err != nil {
			return err //nolint:wrapcheck // the access domain names the mint
		}
		out = &Assignment{
			Run: a.Run, State: a.State, StateVersion: a.StateVersion,
			PreparingDeadline: a.PreparingDeadline, Token: t,
		}
		return nil
	})
	if err == nil && retry {
		return Assignment{Retry: true, RetryAfter: retryAfter}, false, nil
	}
	if err != nil || out == nil {
		return Assignment{}, false, s.unauthenticated(err)
	}
	return *out, true, nil
}

// preparing is the preparing timeout a claim for a worker of p issues
// its run's deadline from: the public pool's, or p's own, or the
// installation's default for a pool that sets none.
func (s *Service) preparing(p pool.Pool, public bool) time.Duration {
	switch {
	case public:
		return s.clocks.publicPreparing
	case p.PreparingTimeout > 0:
		return p.PreparingTimeout
	}
	return s.clocks.preparing
}

// deliver answers the stop or kill of the run w holds through the signals
// internal/app binds, in a transaction that records its delivery, so no
// other poll answers it; "" when w is not busy or none waits.
//
// ref: decision 0008 § "Stop and kill"
func (s *Service) deliver(ctx context.Context, w Worker, b accessapi.Bearer) (workersapi.Signal, error) {
	if s.signals == nil || !w.Busy {
		return "", nil
	}
	var sig workersapi.Signal
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		current, err := s.session(ctx, b, true)
		if err != nil || !current.Busy {
			return err
		}
		sig, err = s.signals.Deliver(ctx, current.ID)
		return err //nolint:wrapcheck // the runs domain names the delivery
	})
	return sig, err //nolint:wrapcheck // as above
}

// hold is how long a poll that asks for wait is held: DefaultWait when
// zero, within MinWait and MaxWait, and no longer than the server allows.
func (s *Service) hold(wait time.Duration) time.Duration {
	if wait == 0 {
		wait = DefaultWait
	}
	return min(max(wait, MinWait), s.LongestHold())
}

// LongestHold is the longest any poll is held: the server's PollMax,
// within MaxWait.
func (s *Service) LongestHold() time.Duration { return min(MaxWait, s.pollMax) }

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
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		if _, err := s.session(ctx, b, true); err != nil {
			return err
		}
		return s.store.Remove(ctx, b.ID) //nolint:wrapcheck // store names the removal
	})
	return s.unauthenticated(err)
}

// RunTokenKeys answers the session's worker what verifies the run tokens
// its polls carry; ErrUnauthenticated for a worker that is gone or whose
// credential generation is no longer current.
//
// ref: decision 0006 § "Signing keys"
func (s *Service) RunTokenKeys(ctx context.Context, b accessapi.Bearer) (accessapi.RunTokenKeys, error) {
	if _, err := s.session(ctx, b, false); err != nil {
		return accessapi.RunTokenKeys{}, err
	}
	keys, err := s.tokens.RunTokenKeys(ctx)
	if err != nil {
		return accessapi.RunTokenKeys{}, fmt.Errorf("read the run token keys: %w", err)
	}
	return keys, nil
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

// WakePublic wakes only the installation's public pool, leaving private pools
// asleep when a new unassigned public run becomes ready.
func (s *Service) WakePublic() {
	s.hub.mu.Lock()
	var pools []kernel.ULID
	for id, group := range s.hub.waiting {
		for _, waiting := range group {
			if waiting.public {
				pools = append(pools, id)
				break
			}
		}
	}
	s.hub.mu.Unlock()
	for _, id := range pools {
		s.hub.wakeOne(id)
	}
}

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
	public     bool
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
