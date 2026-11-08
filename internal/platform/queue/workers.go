package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
)

// Handler runs one claimed job. It must tolerate running twice: delivery
// is at least once. A handler whose effect is database writes makes them
// in a transaction that ends with CompleteTx; ctx ends at the kind's
// timeout, on shutdown after the grace, or when the lease is lost.
type Handler func(ctx context.Context, j Job) error

// Handler timeouts. MaxTimeout is Spacelift's 900-second processing bound.
//
// ref: decision 0011
const (
	DefaultTimeout = 5 * time.Minute
	MaxTimeout     = 15 * time.Minute
)

// HandlerOptions are a kind's settings that only the replica running it
// needs.
type HandlerOptions struct {
	// Timeout bounds one attempt; 0 means DefaultTimeout.
	Timeout time.Duration
	// Feature, when set, is the feature an account's entitlement must
	// include for its jobs to run.
	// ref: decision 0021
	Feature string
	// RetryDelay is the wait before the attempt after a failed one; nil
	// means Backoff.
	RetryDelay func(attempt int) time.Duration
}

// ErrRegister reports a registration the process must not start with.
var ErrRegister = errors.New("invalid job registration")

type registered struct {
	kind    Kind
	handle  Handler
	timeout time.Duration
	feature string
	retry   func(attempt int) time.Duration
}

// Registry holds the handlers a process runs. It is filled before Run and
// read only after.
type Registry struct {
	kinds map[string]registered
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry { return &Registry{kinds: map[string]registered{}} }

// Register runs h for jobs of kind k. An invalid kind, a nil handler, a
// kind registered twice or a timeout above MaxTimeout is an error, so the
// process does not start.
func (r *Registry) Register(k Kind, h Handler, o HandlerOptions) error {
	if err := k.validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrRegister, err)
	}
	if o.Timeout == 0 {
		o.Timeout = DefaultTimeout
	}
	switch {
	case h == nil:
		return fmt.Errorf("%w: %s has no handler", ErrRegister, k.Name)
	case o.Timeout < 0 || o.Timeout > MaxTimeout:
		return fmt.Errorf(
			"%w: %s's timeout %s is outside (0, %s]",
			ErrRegister,
			k.Name,
			o.Timeout,
			MaxTimeout,
		)
	}
	if _, ok := r.kinds[k.Name]; ok {
		return fmt.Errorf("%w: %s is registered twice", ErrRegister, k.Name)
	}
	if o.RetryDelay == nil {
		o.RetryDelay = Backoff
	}
	r.kinds[k.Name] = registered{
		kind:    k,
		handle:  h,
		timeout: o.Timeout,
		feature: o.Feature,
		retry:   o.RetryDelay,
	}
	return nil
}

// on is the registered kinds of queue n, the only ones its claims take: a
// job of a kind this replica does not know, as during a rolling upgrade,
// stays pending for one that does.
func (r *Registry) on(n Name) []string {
	var kinds []string
	for name, k := range r.kinds {
		if k.kind.Queue == n {
			kinds = append(kinds, name)
		}
	}
	slices.Sort(kinds)
	return kinds
}

type snoozed struct{ d time.Duration }

func (s snoozed) Error() string { return fmt.Sprintf("snoozed for %s", s.d) }

// Snooze is a handler result that returns the job to pending for d without
// counting the attempt, for work that is not ready yet.
func Snooze(d time.Duration) error { return snoozed{d} }

type permanent struct{ err error }

func (p permanent) Error() string { return p.err.Error() }
func (p permanent) Unwrap() error { return p.err }

// Permanent is a handler result that makes the job dead at once, for a
// failure no retry can mend, such as a payload that does not decode.
func Permanent(err error) error { return permanent{err} }

// Entitlements decides whether an account's entitlement includes a
// feature.
type Entitlements interface {
	Allowed(ctx context.Context, account kernel.ULID, feature string) (bool, error)
}

// AllowAll is the Entitlements of an installation without the feature
// catalogue: every feature is included.
type AllowAll struct{}

// Allowed implements Entitlements.
func (AllowAll) Allowed(context.Context, kernel.ULID, string) (bool, error) { return true, nil }

// Causes a handler's context ends with.
var (
	errTimeout  = errors.New("job timed out")
	errShutdown = errors.New("replica shutting down")
)

// errPanic marks a handler that panicked; the job is retried.
var errPanic = errors.New("handler panicked")

// settleTimeout bounds recording an attempt's outcome, which must happen
// even after the handler's context ended.
const settleTimeout = 30 * time.Second

// Workers runs the registered kinds' jobs: a pool per logical queue with
// that queue's worker count.
type Workers struct {
	q     *Queue
	reg   *Registry
	cfg   Config
	ent   Entitlements
	wake  <-chan notify.Message
	log   *slog.Logger
	owner string
	// waiting, when set, is called each time a queue's pool waits; tests
	// use it to enqueue only once a pool is idle.
	waiting func(Name)
	// skipped counts the jobs completed unrun because their account lacks
	// the kind's feature, the infraweave.features.skipped metric.
	// ref: decision 0011
	skipped atomic.Int64
}

// NewWorkers runs reg's handlers on q's jobs as cfg says. wake delivers
// infraweave_jobs notifications, whose payload is a queue's name; owner
// names this replica on the jobs it claims.
func NewWorkers(
	q *Queue,
	reg *Registry,
	cfg Config,
	ent Entitlements,
	wake <-chan notify.Message,
	log *slog.Logger,
	owner string,
) *Workers {
	return &Workers{q: q, reg: reg, cfg: cfg, ent: ent, wake: wake, log: log, owner: owner}
}

type pool struct {
	name  Name
	kinds []string
	slots chan struct{}
	wake  chan struct{}
}

// poke wakes the pool without blocking; one pending wake-up is enough.
func (p *pool) poke() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Run claims and runs jobs until ctx ends, then shuts down: it stops
// claiming, lets running handlers finish for queue.shutdown_grace, then
// cancels the rest and hands their jobs back without counting the attempt.
// It returns once every handler has.
func (w *Workers) Run(ctx context.Context) {
	base, stop := context.WithCancelCause(context.WithoutCancel(ctx))
	defer stop(nil)
	pools := map[Name]*pool{}
	var started []any
	for _, n := range Queues {
		kinds, workers := w.reg.on(n), w.cfg.Workers(n)
		if len(kinds) == 0 || workers == 0 {
			continue
		}
		pools[n] = &pool{
			name:  n,
			kinds: kinds,
			slots: make(chan struct{}, workers),
			wake:  make(chan struct{}, 1),
		}
		started = append(started, string(n), workers)
	}
	w.log.Info("queue workers started", slog.Group("workers", started...))
	var loops, running sync.WaitGroup
	for _, p := range pools {
		loops.Go(func() { w.poll(ctx, base, p, &running) })
	}
	loops.Go(func() { w.route(ctx, pools) })
	loops.Go(func() { w.recover(ctx) })
	loops.Wait()

	done := make(chan struct{})
	go func() {
		running.Wait()
		close(done)
	}()
	grace := time.NewTimer(w.cfg.ShutdownGrace)
	defer grace.Stop()
	select {
	case <-done:
	case <-grace.C:
		stop(errShutdown)
		<-done
	}
	w.log.Info("queue workers stopped")
}

// poll claims as many jobs as p has free slots, then waits for a
// notification, a freed slot or the poll interval.
func (w *Workers) poll(ctx, base context.Context, p *pool, running *sync.WaitGroup) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	failing := false
	for {
		if free := cap(p.slots) - len(p.slots); free > 0 {
			jobs, err := w.q.Claim(ctx, p.name, p.kinds, free, w.cfg.Lease, w.owner)
			switch {
			case err != nil && ctx.Err() == nil && !failing:
				failing = true
				w.log.Warn("queue claim failed", "queue", string(p.name), "error", err)
			case err == nil && failing:
				failing = false
				w.log.Info("queue claim recovered", "queue", string(p.name))
			}
			for _, j := range jobs {
				p.slots <- struct{}{}
				running.Go(func() {
					defer func() {
						<-p.slots
						p.poke()
					}()
					w.run(base, j)
				})
			}
		}
		if w.waiting != nil {
			w.waiting(p.name)
		}
		timer.Reset(w.cfg.PollInterval)
		select {
		case <-ctx.Done():
			return
		case <-p.wake:
		case <-timer.C:
		}
	}
}

// route wakes the pool a notification names, and every pool on a resync,
// since notifications sent while the listener was away are lost.
func (w *Workers) route(ctx context.Context, pools map[Name]*pool) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-w.wake:
			for n, p := range pools {
				if m.Resync || string(n) == m.Payload {
					p.poke()
				}
			}
		}
	}
}

// recover returns lapsed leases to pending every poll interval, so a
// crashed replica's work is taken over within one lease.
func (w *Workers) recover(ctx context.Context) {
	t := time.NewTicker(w.cfg.PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := w.q.Recover(ctx); err == nil && n > 0 {
				w.log.Warn("jobs recovered from lapsed leases", "jobs", n)
			}
		}
	}
}

// Skipped is infraweave.features.skipped: how many jobs these workers
// completed without running because their account's entitlement lacks
// their kind's feature.
func (w *Workers) Skipped() int64 { return w.skipped.Load() }

// run runs one claimed job and records its outcome.
func (w *Workers) run(base context.Context, j Job) {
	r := w.reg.kinds[j.Kind]
	log := w.log.With("kind", j.Kind, "job", j.ID, "attempt", j.Attempt)
	settle, cancel := context.WithTimeout(context.WithoutCancel(base), settleTimeout)
	defer cancel()
	if r.feature != "" && !j.Account.IsZero() {
		ok, err := w.ent.Allowed(base, j.Account, r.feature)
		if err == nil && !ok {
			w.skipped.Add(1)
			log.Info("job skipped: feature outside the account's entitlement", "feature", r.feature)
			w.record(log, "skipped", w.q.Complete(settle, j))
			return
		}
		if err != nil {
			w.fail(settle, log, r, j, fmt.Errorf("check entitlement: %w", err))
			return
		}
	}
	hctx, lose := context.WithCancelCause(base)
	defer lose(nil)
	hctx, stop := context.WithTimeoutCause(hctx, r.timeout, errTimeout)
	defer stop()
	kept := make(chan struct{})
	go w.keep(hctx, j, lose, kept)
	err := call(hctx, log, r.handle, j)
	cause := context.Cause(hctx)
	stop()
	<-kept
	switch {
	case err == nil && j.completed():
		w.record(log, "succeeded", nil)
	case errors.Is(cause, ErrLeaseLost):
		log.Warn("job lease lost; its result is discarded", "error", err)
	case err == nil:
		w.record(log, "succeeded", w.q.Complete(settle, j))
	case errors.Is(context.Cause(base), errShutdown):
		w.record(log, "released", w.q.Release(settle, j))
	default:
		var s snoozed
		var p permanent
		switch {
		case errors.As(err, &s):
			w.record(log, "snoozed", w.q.Snooze(settle, j, s.d))
		case errors.As(err, &p):
			log.Warn("job dead: permanent failure", "error", p.err)
			w.record(log, "dead", w.q.Kill(settle, j, p.err))
		default:
			if errors.Is(cause, errTimeout) {
				err = fmt.Errorf("%w after %s: %w", errTimeout, r.timeout, err)
			}
			w.fail(settle, log, r, j, err)
		}
	}
}

// keep renews j's lease every third of it until ctx ends, and ends the
// handler's context with ErrLeaseLost when the lease was taken over.
func (w *Workers) keep(
	ctx context.Context,
	j Job,
	lose context.CancelCauseFunc,
	done chan<- struct{},
) {
	defer close(done)
	t := time.NewTicker(w.cfg.Lease / 3)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			err := w.q.Extend(ctx, j, w.cfg.Lease)
			if errors.Is(err, ErrLeaseLost) && !j.completed() {
				lose(ErrLeaseLost)
				return
			}
		}
	}
}

func (w *Workers) fail(ctx context.Context, log *slog.Logger, r registered, j Job, cause error) {
	dead, err := w.q.FailAfter(ctx, j, cause, r.retry(j.Attempt))
	if dead {
		log.Warn("job dead: attempts exhausted", "error", cause)
		w.record(log, "dead", err)
		return
	}
	log.Debug("job failed; retrying", "error", cause)
	w.record(log, "retried", err)
}

// record logs an attempt's outcome, or why it could not be recorded.
func (w *Workers) record(log *slog.Logger, outcome string, err error) {
	switch {
	case errors.Is(err, ErrLeaseLost):
		log.Warn("job lease lost; its result is discarded", "outcome", outcome)
	case err != nil:
		log.Warn("job outcome not recorded; the lease will lapse", "outcome", outcome, "error", err)
	default:
		log.Debug("job finished", "outcome", outcome)
	}
}

// call runs h, turning a panic into an error so the job is retried rather
// than the process lost.
func call(ctx context.Context, log *slog.Logger, h Handler, j Job) (err error) {
	defer func() {
		if p := recover(); p != nil {
			log.Error(
				"job handler panicked",
				"panic",
				fmt.Sprint(p),
				"stack",
				string(debug.Stack()),
			)
			err = fmt.Errorf("%w: %v", errPanic, p)
		}
	}()
	return h(ctx, j)
}
