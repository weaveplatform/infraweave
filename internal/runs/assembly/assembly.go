package assembly

import (
	"context"
	"errors"
	"fmt"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	configapi "github.com/weaveplatform/infraweave/internal/configuration/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/expr"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/http"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/control"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/handover"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/logs"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/report"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/schedule"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/surface"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/trigger"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vendorsapi "github.com/weaveplatform/infraweave/internal/vendors/api"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// Stream is the runs domain's migration stream.
func Stream() migrate.Stream { return pg.Stream() }

// Settings are the run settings: LostAfter, how long a worker goes unseen
// before its run is a lost run and fails, run.lost_after, and
// PreparingTimeoutDefault, the preparing timeout of a pool that sets
// none, run.preparing_timeout_default. The workers domain's reap applies
// both, so internal/app hands them to it.
//
// ref: decision 0008 § "Configuration"
type Settings struct {
	LostAfter               time.Duration `mapstructure:"lost_after"`
	PreparingTimeoutDefault time.Duration `mapstructure:"preparing_timeout_default"`
}

// DefaultSettings fail a lost run an hour after its worker's last poll,
// which covers a launcher's restart and a short outage, and a run no
// started report moved on 15 minutes after its claim.
func DefaultSettings() Settings {
	return Settings{LostAfter: time.Hour, PreparingTimeoutDefault: 15 * time.Minute}
}

// ErrConfig reports a run setting the server could not start with.
var ErrConfig = errors.New("invalid run configuration")

// Validate rejects a preparing timeout under a second, the unit a pool's
// own is set in. run.lost_after is checked against worker.offline_after,
// which internal/app/config does, since it spans both domains.
func (s Settings) Validate() error {
	if s.PreparingTimeoutDefault < time.Second {
		return fmt.Errorf("%w: run.preparing_timeout_default %s is under 1s", ErrConfig,
			s.PreparingTimeoutDefault)
	}
	return nil
}

// Held are the runs columns that hold other domains' rows, for the
// nightly orphan count.
func Held() []integrity.Held { return pg.Held() }

// Keys are the runs domain's rows other domains hold, for the nightly
// orphan count.
func Keys() []integrity.Key { return pg.Keys() }

// SealedColumns are the runs domain's sealed columns.
func SealedColumns() []crypt.Column { return pg.SealedColumns() }

// Entity is the name the access domain resolves a run's space by, for
// the operations whose subject is a run.
const Entity = "run"

// Objects are the objects a run's worker uploads and its logs are
// compacted into, as blob_objects records them; blobs.Objects is one.
type Objects interface {
	report.Objects
	logs.Objects
	trigger.Objects
}

// Deps are what the runs domain takes from the platform and the domains
// before it: the database, the keys a run's artifact key is sealed under,
// the objects its workers upload, the access domain's spaces, accounts,
// authorizer and the tokens a handover mints, the stacks domain, the
// configuration domain's environment, which a handover opens, the outbox
// its runs' events go to, the queue its jobs go to, and a clock, time.Now
// when nil.
type Deps struct {
	DB          queue.DB
	Keys        pg.Sealer
	Objects     Objects
	Spaces      accessapi.Spaces
	Accounts    accessapi.Accounts
	Authz       accessapi.Authorizer
	Tokens      handover.Tokens
	Stacks      stacksapi.Stacks
	Environment configapi.Environment
	Catalog     vendorsapi.Catalog
	Events      lifecycle.Events
	Jobs        schedule.Queue
	Now         func() time.Time
}

// Domain is the runs domain, constructed once per process.
type Domain struct {
	store     *pg.Store
	runs      *core.Service
	lifecycle *lifecycle.Lifecycle
	scheduler *schedule.Scheduler
	claims    *schedule.Claims
	liveness  *schedule.Liveness
	handover  *handover.Service
	reports   *report.Service
	logs      *logs.Service
	trigger   *trigger.Service
	read      *read.Service
	control   *control.Service
}

// New constructs the domain on d.
func New(d Deps) *Domain {
	store := pg.New(d.DB, d.Keys)
	lc := lifecycle.New(lifecycle.Deps{
		Store: store, Stacks: d.Stacks, Events: d.Events, Jobs: d.Jobs, Now: d.Now,
	})
	views := read.New(read.Deps{Store: store, Stacks: d.Stacks, Spaces: d.Spaces, Authz: d.Authz})
	return &Domain{
		store:     store,
		runs:      core.New(store),
		lifecycle: lc,
		scheduler: schedule.New(schedule.Deps{
			DB: d.DB, Store: store, Stacks: d.Stacks, Lifecycle: lc, Queue: d.Jobs,
		}),
		claims:   schedule.NewClaims(store, lc),
		liveness: schedule.NewLiveness(store, lc),
		handover: handover.New(handover.Deps{
			Store: store, Stacks: d.Stacks, Spaces: d.Spaces, Accounts: d.Accounts,
			Environment: d.Environment, Tokens: d.Tokens, Catalog: d.Catalog, Objects: d.Objects,
		}),
		reports: report.New(report.Deps{
			DB: d.DB, Store: store, Objects: d.Objects, Lifecycle: lc, Stacks: d.Stacks,
		}),
		logs: logs.New(logs.Deps{DB: d.DB, Store: store, Objects: d.Objects, Jobs: d.Jobs}),
		trigger: trigger.New(trigger.Deps{
			DB: d.DB, Stacks: d.Stacks, Lifecycle: lc, Views: views, Objects: d.Objects,
		}),
		read: views,
		control: control.New(control.Deps{
			DB: d.DB, Store: store, Stacks: d.Stacks, Authz: d.Authz, Lifecycle: lc,
			Views: views, Now: d.Now,
		}),
	}
}

// SpaceOf finds the space of a run's stack by the run's ID, which
// internal/app registers with the access domain for Entity.
func (d *Domain) SpaceOf() accessapi.SpaceOf { return d.read.SpaceOf }

// RunAccess is the run reader an in-run API token's authentication reads
// its run through, a back edge internal/app binds in the access domain.
func (d *Domain) RunAccess() accessapi.Runs { return d.read }

// PurgeBatch is how many rows one statement of Purge deletes, so no
// transaction holds an account's every run and transition at once.
const PurgeBatch = 500

// Purge deletes the deleted account acct's runs' log chunks and other
// uploads with their objects and compacted logs, then its runs, with their transitions, and
// its stacks' run locks, tracked runs and outputs, PurgeBatch rows at a
// time. internal/app runs it for each accessapi.Deleted; a purge stopped
// part way keeps what it deleted, and running it again finishes.
func (d *Domain) Purge(ctx context.Context, acct kernel.AccountID) error {
	for {
		done, err := d.reports.PurgeObjects(ctx, acct, PurgeBatch)
		if err != nil {
			return err //nolint:wrapcheck // the service names the purge
		}
		if done {
			break
		}
	}
	for {
		done, err := d.store.Purge(ctx, acct, PurgeBatch)
		if err != nil || done {
			return err //nolint:wrapcheck // the store names the purge
		}
	}
}

// Surfaces are the domain's additions to the surfaces other domains own:
// run, on the configuration domain's environment surface.
//
// ref: decision 0024 "Who declares a surface"
func (d *Domain) Surfaces() []expr.Surface {
	return []expr.Surface{{Name: configapi.Surface, Variables: []expr.Variable{surface.Variable()}}}
}

// Jobs are the domain's job handlers, which the scheduler registers:
// lifecycle.ScheduleKind's, which moves a stack's queued runs to READY,
// and lifecycle.CompactKind's, which writes a state's log to one object
// and may stream as long as the longest handler may run.
func (d *Domain) Jobs() []queue.Registration {
	return []queue.Registration{
		{Kind: lifecycle.ScheduleKind, Handler: d.scheduler.Handle},
		{
			Kind: lifecycle.CompactKind, Handler: d.logs.Compact,
			Options: queue.HandlerOptions{Timeout: queue.MaxTimeout},
		},
	}
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

// BindSources binds the vcs domain's run sources and its resolution of a
// stack's commits, back edges internal/app binds after it constructs that
// domain; from then on a handover names the run's source and seals its
// credential, every run that ends releases its credentials, and a trigger
// may name a commit.
func (d *Domain) BindSources(s runsapi.RunSources, r stacksapi.StackSources) {
	d.lifecycle.BindSources(s)
	d.handover.BindSources(s)
	d.trigger.BindSources(r)
}

// StackStatus is the domain's answer to the stack view's read of the run
// blocking each stack and to runStack's read of a run's stack, a back
// edge internal/app binds in the stacks domain.
func (d *Domain) StackStatus() stacksapi.StackStatus { return d.runs }

// Scheduler is the domain's side of the worker protocol, a back edge
// internal/app binds in the workers domain, whose polls claim runs
// through it and whose specifications it builds.
func (d *Domain) Scheduler() workersapi.Scheduler { return scheduler{d.claims, d.handover} }

// scheduler is the domain's workersapi.Scheduler: its claims and its
// handovers.
type scheduler struct {
	*schedule.Claims
	*handover.Service
}

// Reports is the domain's side of a claimed run's reports and uploads, a
// back edge internal/app binds in the workers domain.
func (d *Domain) Reports() workersapi.Reports { return d.reports }

// Liveness is the domain's side of lost workers, ended containers and
// overdue preparations, a back edge internal/app binds in the workers
// domain, whose reap and polls fail runs through it.
func (d *Domain) Liveness() workersapi.Liveness { return d.liveness }

// BindExpressions binds the expression registry, which internal/app
// builds from the domains' Surfaces after constructing them, and which a
// handover renders the environment's templates with.
func (d *Domain) BindExpressions(r *expr.Registry) { d.handover.BindExpressions(r) }

// StackGuard is the domain's say in the stacks domain's moves and
// deletes, which internal/app registers there, so a deleted stack's runs
// go in its delete's transaction.
func (d *Domain) StackGuard() stacksapi.StackGuard { return d.runs }

// StateAccess decides a run token's access to its stack's state from the
// run's current state, the forward edge internal/app hands the state
// domain.
func (d *Domain) StateAccess() runsapi.StateAccess { return d.runs }

// RunsAPI is the domain's partial of the API's strict server, which
// internal/app embeds under this alias.
type RunsAPI = http.Server

// API is the domain's partial of the API's strict server; a nil domain's
// is the zero partial, for tests that construct none.
func (d *Domain) API() RunsAPI {
	if d == nil {
		return RunsAPI{}
	}
	return http.New(d.trigger, d.read, d.control, d.logs)
}
