package timers

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
)

// Kind is run.timers, the pass that ends the runs nobody moves. It runs
// every minute on the scheduler queue beside worker.reap, so a long
// maintenance job never delays it, with policy Ignore, so a slow pass
// never overlaps the next.
//
// ref: decision 0008 § "Timeouts and timers"
// ref: decision 0011 § "Scheduling and cron"
var Kind = queue.Kind{Name: "run.timers", Queue: queue.Scheduler, Policy: queue.Ignore}

// Every is how often run.timers runs; each rule fires within a minute of
// its boundary.
const Every = time.Minute

// Batch bounds one transaction of a pass, and Batches the transactions of
// one pass: a pass ends at most Batch × Batches runs, and the next, a
// minute on, goes on where it stopped.
const (
	Batch   = 100
	Batches = 10
)

// Settings are run.timers: how long a proposed or test run waits to be
// picked up, how long a tracked run, task or destroy run waits in READY,
// how long a tracked run stays UNCONFIRMED, and CONFIRMED or
// REPLAN_REQUESTED, unchanged, and how long after its creation any run
// that no worker holds may go on.
//
// ref: decision 0008 § "Configuration"
type Settings struct {
	PickupProposed time.Duration `mapstructure:"pickup_proposed"`
	PickupBlocking time.Duration `mapstructure:"pickup_blocking"`
	Unconfirmed    time.Duration `mapstructure:"unconfirmed"`
	Confirmed      time.Duration `mapstructure:"confirmed"`
	MaxAge         time.Duration `mapstructure:"max_age"`
}

const day = 24 * time.Hour

// DefaultSettings are Spacelift's: 2 days to be picked up for a proposed
// or test run, 7 days for any other, 7 unchanged days to confirm or apply,
// and 30 days in all.
//
// ref: report 08 § "Timers"
func DefaultSettings() Settings {
	return Settings{
		PickupProposed: 2 * day, PickupBlocking: 7 * day, Unconfirmed: 7 * day,
		Confirmed: 7 * day, MaxAge: 30 * day,
	}
}

// ErrSettings reports a timer run.timers could not keep.
var ErrSettings = errors.New("invalid run.timers")

// Validate rejects a timer under Every, which no pass could keep.
func (s Settings) Validate() error {
	for _, r := range s.rules() {
		if r.after < Every {
			return fmt.Errorf("%w: run.timers.%s %s is under %s", ErrSettings, r.timer,
				r.after, Every)
		}
	}
	return nil
}

// rule is one timer rule: the run types and states it applies to,
// whether it counts from the run's creation rather than from its state's
// last change, how long, and the note of the transitions it takes.
type rule struct {
	timer   runsapi.Timer
	types   []kernel.RunType
	states  []kernel.RunState
	created bool
	after   time.Duration
	note    string
}

// waiting are the states no worker holds that a run stops in until
// something moves it: the states the timers end runs in. A run on a
// worker is liveness's, offline worker or not.
var waiting = []kernel.RunState{
	kernel.RunStateQueued, kernel.RunStateReady, kernel.RunStatePendingReview,
	kernel.RunStateUnconfirmed, kernel.RunStateConfirmed, kernel.RunStateReplanRequested,
}

// rules are s's, in the order a run that more than one times out takes
// them: the 30-day rule first, which never notifies, so a run that old
// ends quietly whatever else timed it out.
//
// ref: report 08 § "Timers"
func (s Settings) rules() []rule {
	every := []kernel.RunType{
		kernel.RunTypeProposed, kernel.RunTypeTracked, kernel.RunTypeTask,
		kernel.RunTypeTesting, kernel.RunTypeDestroy,
	}
	tracked := []kernel.RunType{kernel.RunTypeTracked}
	return []rule{
		{
			timer: runsapi.TimerMaxAge, types: every, states: waiting, created: true,
			after: s.MaxAge, note: "not ended within " + span(s.MaxAge) + " of its creation",
		},
		{
			timer:  runsapi.TimerPickupProposed,
			types:  []kernel.RunType{kernel.RunTypeProposed, kernel.RunTypeTesting},
			states: []kernel.RunState{kernel.RunStateQueued, kernel.RunStateReady},
			after:  s.PickupProposed, note: "not picked up within " + span(s.PickupProposed),
		},
		{
			timer: runsapi.TimerPickupBlocking,
			types: []kernel.RunType{
				kernel.RunTypeTracked, kernel.RunTypeTask, kernel.RunTypeDestroy,
			},
			states: []kernel.RunState{kernel.RunStateReady},
			after:  s.PickupBlocking, note: "not picked up within " + span(s.PickupBlocking),
		},
		{
			timer: runsapi.TimerUnconfirmed, types: tracked,
			states: []kernel.RunState{kernel.RunStateUnconfirmed},
			after:  s.Unconfirmed, note: "unconfirmed for " + span(s.Unconfirmed),
		},
		{
			timer: runsapi.TimerConfirmed, types: tracked,
			states: []kernel.RunState{kernel.RunStateConfirmed, kernel.RunStateReplanRequested},
			after:  s.Confirmed, note: "not applied within " + span(s.Confirmed),
		},
	}
}

// span is d in the largest whole unit of days, hours and minutes, or as
// a duration.
func span(d time.Duration) string {
	for _, u := range []struct {
		unit time.Duration
		name string
	}{{day, "day"}, {time.Hour, "hour"}, {time.Minute, "minute"}} {
		if d > 0 && d%u.unit == 0 {
			n := int64(d / u.unit)
			if n == 1 {
				return "1 " + u.name
			}
			return fmt.Sprintf("%d %ss", n, u.name)
		}
	}
	return d.String()
}

// ending is the state the timers' edge out of from takes a run of type
// t to, and false when no such edge exists.
func ending(t kernel.RunType, from kernel.RunState) (kernel.RunState, bool) {
	for _, e := range machine.Next(t, from) {
		if slices.Contains(e.Drivers, machine.DriverTimers) {
			return e.To, true
		}
	}
	return "", false
}

// Cutoff is one state and run type a rule times out: a run of Type in
// State whose creation, when Created, or else whose state's last change,
// is at or before At.
type Cutoff struct {
	State   kernel.RunState
	Type    kernel.RunType
	Created bool
	At      time.Time
}

// cutoffs are rs's at now, each pair of state and run type a rule names
// that the timers' edges take out of that state.
func cutoffs(rs []rule, now time.Time) []Cutoff {
	var out []Cutoff
	for _, r := range rs {
		for _, st := range r.states {
			for _, t := range r.types {
				if _, ok := ending(t, st); ok {
					out = append(out, Cutoff{
						State: st, Type: t, Created: r.created,
						At: now.Add(-r.after),
					})
				}
			}
		}
	}
	return out
}

// due is the first of rs that times run out at now, and false when none
// does: a rule fires once its time has passed in full, not a moment
// before.
func due(rs []rule, run lifecycle.Run, now time.Time) (rule, bool) {
	for _, r := range rs {
		if !slices.Contains(r.types, run.Type) || !slices.Contains(r.states, run.State) {
			continue
		}
		since := run.StateChangedAt
		if r.created {
			since = run.CreatedAt
		}
		if !since.After(now.Add(-r.after)) {
			return r, true
		}
	}
	return rule{}, false
}

// Store is the timers' storage port. It joins the transaction its context
// carries.
type Store interface {
	// Due reads up to batch runs that one of cutoffs times out, oldest
	// state change first, among the rows no other transaction locks, and
	// locks them FOR UPDATE.
	Due(ctx context.Context, cutoffs []Cutoff, batch int) ([]lifecycle.Run, error)
}

// Mover moves a run, as lifecycle.Lifecycle's Transition does.
type Mover interface {
	Transition(ctx context.Context, req lifecycle.Request) (lifecycle.Run, error)
}

// Deps are the timers' database, storage, the lifecycle that moves runs,
// run.timers and a clock, time.Now when nil.
type Deps struct {
	DB        tx.Beginner
	Store     Store
	Lifecycle Mover
	Settings  Settings
	Now       func() time.Time
}

// Service is run.timers.
type Service struct {
	d     Deps
	rules []rule
}

// New returns run.timers on d.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d, rules: d.Settings.rules()}
}

// Ended counts the runs one pass ended, by rule.
type Ended map[runsapi.Timer]int

// Pass is one run of run.timers. It reads the runs a rule times out in
// batches, oldest state change first, each batch in a transaction of its
// own, skipping rows another transaction locks, so passes on two replicas
// never end a run twice, and answers the runs it ended, by rule, in the
// transactions that committed. It moves each through the timers' edge out of its
// state, with a note that names the rule and the rule on its run_updated
// event, which releases the stack run lock a blocking run holds.
//
// ref: decision 0008 § "Timeouts and timers"
func (s *Service) Pass(ctx context.Context) (Ended, error) {
	now := s.d.Now().UTC().Truncate(time.Microsecond)
	cuts := cutoffs(s.rules, now)
	ended := Ended{}
	for range Batches {
		var batch Ended
		n := 0
		err := tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
			batch = Ended{}
			runs, err := s.d.Store.Due(ctx, cuts, Batch)
			if err != nil {
				return err //nolint:wrapcheck // the store names the read
			}
			n = len(runs)
			for _, r := range runs {
				timer, err := s.end(ctx, r, now)
				if err != nil {
					return err
				}
				batch[timer]++
			}
			return nil
		})
		if err != nil {
			return ended, err //nolint:wrapcheck // the store's, the lifecycle's or the transaction's
		}
		for timer, k := range batch {
			ended[timer] += k
		}
		if n < Batch {
			break
		}
	}
	return ended, nil
}

// end moves r through the timers' edge out of its state for the rule
// that times it out at now, and answers the rule.
func (s *Service) end(ctx context.Context, r lifecycle.Run, now time.Time) (runsapi.Timer, error) {
	ru, ok := due(s.rules, r, now)
	to, edge := ending(r.Type, r.State)
	if !ok || !edge {
		return "", fmt.Errorf("%w: run %s, %s in %s", errNotDue, r.ID, r.Type, r.State)
	}
	_, err := s.d.Lifecycle.Transition(ctx, lifecycle.Request{
		Account: r.Account, Run: r.ID, Version: r.Version, To: to,
		Driver: machine.DriverTimers, Note: ru.note, Timer: ru.timer,
	})
	if err != nil {
		return "", err //nolint:wrapcheck // the lifecycle names the move
	}
	return ru.timer, nil
}

// errNotDue reports a run the store read that no rule times out, which a
// store that applies the cutoffs never reads.
var errNotDue = errors.New("runs: the timers read a run no rule times out")
