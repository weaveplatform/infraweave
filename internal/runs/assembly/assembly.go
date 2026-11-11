package assembly

import (
	"time"

	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/http"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/schedule"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/trigger"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Stream is the runs domain's migration stream.
func Stream() migrate.Stream { return pg.Stream() }

// Held are the runs columns that hold other domains' rows, for the
// nightly orphan count.
func Held() []integrity.Held { return pg.Held() }

// Deps are what the runs domain takes from the platform and the domains
// before it: the database, the stacks domain, the outbox its runs' events
// go to, the queue its jobs go to, and a clock, time.Now when nil.
type Deps struct {
	DB     queue.DB
	Stacks stacksapi.Stacks
	Events lifecycle.Events
	Jobs   schedule.Queue
	Now    func() time.Time
}

// Domain is the runs domain, constructed once per process.
type Domain struct {
	runs      *core.Service
	lifecycle *lifecycle.Lifecycle
	scheduler *schedule.Scheduler
	trigger   *trigger.Service
}

// New constructs the domain on d.
func New(d Deps) *Domain {
	store := pg.New(d.DB)
	lc := lifecycle.New(lifecycle.Deps{
		Store: store, Stacks: d.Stacks, Events: d.Events, Jobs: d.Jobs, Now: d.Now,
	})
	return &Domain{
		runs:      core.New(store),
		lifecycle: lc,
		scheduler: schedule.New(schedule.Deps{
			DB: d.DB, Store: store, Stacks: d.Stacks, Lifecycle: lc, Queue: d.Jobs,
		}),
		trigger: trigger.New(trigger.Deps{
			DB: d.DB, Store: store, Stacks: d.Stacks, Lifecycle: lc,
		}),
	}
}

// Jobs are the domain's job handlers, which the scheduler registers:
// lifecycle.ScheduleKind's, which moves a stack's queued runs to READY.
func (d *Domain) Jobs() []queue.Registration {
	return []queue.Registration{{Kind: lifecycle.ScheduleKind, Handler: d.scheduler.Handle}}
}

// Subscribe subscribes the domain's consumers on events: the scheduling
// of a stack that is unlocked or enabled.
func (d *Domain) Subscribe(events *outbox.Outbox) error {
	//nolint:wrapcheck // names the consumer
	return events.Subscribe(stacksapi.ChangedKind, schedule.Consumer, d.scheduler.StackChanged)
}

// RegisterEvents adds the domain's event kinds to kinds, before the
// scheduler starts.
func RegisterEvents(kinds *outbox.Kinds) error {
	//nolint:wrapcheck // names the kind
	return kinds.Register(runsapi.UpdatedKind, runsapi.UpdatedVersion, runsapi.UpdatedSchema)
}

// BindSources binds the vcs domain's revocation of a run's source
// credentials and its resolution of a stack's commits, back edges
// internal/app binds after it constructs that domain; from then on every
// run that ends releases its credentials, and a trigger may name a commit.
func (d *Domain) BindSources(s runsapi.RunSources, r stacksapi.StackSources) {
	d.lifecycle.BindSources(s)
	d.trigger.BindSources(r)
}

// StackStatus is the domain's answer to the stack view's read of the run
// blocking each stack, a back edge internal/app binds in the stacks
// domain.
func (d *Domain) StackStatus() stacksapi.StackStatus { return d.runs }

// StackGuard is the domain's say in the stacks domain's moves and
// deletes, which internal/app registers there, so a deleted stack's runs
// go in its delete's transaction.
func (d *Domain) StackGuard() stacksapi.StackGuard { return d.runs }

// RunsAPI is the domain's partial of the API's strict server, which
// internal/app embeds under this alias.
type RunsAPI = http.Server

// API is the domain's partial of the API's strict server; a nil domain's
// is the zero partial, for tests that construct none.
func (d *Domain) API() RunsAPI {
	if d == nil {
		return RunsAPI{}
	}
	return http.New(d.trigger)
}
