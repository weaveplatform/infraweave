package assembly

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	workerapi "github.com/weaveplatform/infraweave/internal/platform/gen/worker"
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
	"github.com/weaveplatform/infraweave/internal/workers/internal/adapters/http"
	"github.com/weaveplatform/infraweave/internal/workers/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/workers/internal/adapters/protocol"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/images"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/worker"
)

// Stream is the domain's migration stream, which internal/app registers
// after access'.
func Stream() migrate.Stream { return pg.Stream() }

// Held are the workers columns that hold other domains' rows, for the
// nightly orphan count.
func Held() []integrity.Held { return pg.Held() }

// Keys are the workers domain's rows other domains hold, for the nightly
// orphan count.
func Keys() []integrity.Key { return pg.Keys() }

// SealedColumns are the workers domain's sealed columns.
func SealedColumns() []crypt.Column { return pg.SealedColumns() }

// Entity is the kind of entity the access domain's middleware finds a
// space for, as the API's x-infraweave-subject names it: a pool, in its
// own space.
const Entity = "worker_pool"

// Settings are the workers domain's: PollTimeoutMax, the longest a
// worker's poll is held, worker.poll_timeout_max.
//
// ref: decision 0008 § "Configuration"
type Settings struct {
	PollTimeoutMax time.Duration `mapstructure:"poll_timeout_max"`
}

// DefaultSettings hold a poll for at most two minutes, the longest a
// launcher asks for.
func DefaultSettings() Settings { return Settings{PollTimeoutMax: worker.MaxWait} }

// ErrConfig reports a workers setting the server could not start with.
var ErrConfig = errors.New("invalid worker configuration")

// Validate rejects a poll timeout shorter than the shortest hold a
// launcher may ask for.
func (s Settings) Validate() error {
	if s.PollTimeoutMax < worker.MinWait {
		return fmt.Errorf("%w: worker.poll_timeout_max %s is under %s", ErrConfig,
			s.PollTimeoutMax, worker.MinWait)
	}
	return nil
}

// DB is the pool the domain queries and begins its transactions on.
type DB interface {
	tx.Querier
	tx.Beginner
}

// Deps are what the workers domain takes from the platform and the
// domains below it: the database, the keys the pool CA's key is sealed
// under, the spaces a pool lives in and holds, the authorizer that
// decides the checks beyond each route's and a pool's allowedActions,
// Public, the server's public URL, which a pool token names, the tokens
// the worker API mints, the entitlements a new worker is
// counted against, Version, the server's version, which a launcher's
// must match to its minor version or the one before, PollMax, the
// longest a worker's poll is held, and the log a refused worker
// credential is recorded in.
type Deps struct {
	DB       DB
	Keys     pg.Sealer
	Spaces   accessapi.Spaces
	Authz    accessapi.Authorizer
	Public   string
	Tokens   accessapi.Tokens
	Features worker.Limits
	Version  string
	PollMax  time.Duration
	Log      *slog.Logger
	Now      func() time.Time
}

// Domain is the workers domain, constructed once per process.
type Domain struct {
	svc     *pool.Service
	workers *worker.Service
	api     http.Server
	log     *slog.Logger
}

// New constructs the domain on d.
func New(d Deps) *Domain {
	store := pg.New(d.DB, d.Keys)
	svc := pool.New(pool.Deps{
		Store: store, DB: d.DB, Spaces: d.Spaces, Authz: d.Authz,
		Public: d.Public, Now: d.Now,
	})
	wk := worker.New(worker.Deps{
		Store: store, DB: d.DB, Tokens: d.Tokens, Limits: d.Features, Version: d.Version,
		PollMax: d.PollMax, Now: d.Now,
	})
	log := d.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Domain{
		svc: svc, workers: wk, log: log,
		api: http.New(svc, images.New(store), wk, d.Authz, d.Spaces),
	}
}

// Protocol is the worker API's strict server, which internal/app mounts
// behind the controls of every API surface: a launcher's registration,
// polls and exit. A nil domain's answers 501 to each, for tests that
// construct none.
//
// ref: decision 0008 § "The worker API"
func (d *Domain) Protocol() workerapi.StrictServerInterface {
	if d == nil {
		return workerapi.UnimplementedStrict{}
	}
	return protocol.New(d.workers, d.log)
}

// WatchPolls ends the holds of this replica's held polls until ctx ends,
// from sub, a subscription to infraweave_runs: a pool's ID wakes the
// pool's polls, a workersapi.Waiting payload one of them, and a Resync,
// after which messages may have been lost, all of them. Each woken poll
// rereads its worker and claims a run when idle.
func (d *Domain) WatchPolls(ctx context.Context, sub *notify.Subscription) {
	watchPolls(ctx, d.workers, sub.C)
}

// StopPolls answers this replica's held polls at once, and later ones
// without a hold, which internal/app calls as the shutdown begins.
func (d *Domain) StopPolls() { d.workers.Stop() }

func watchPolls(ctx context.Context, svc *worker.Service, msgs <-chan notify.Message) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-msgs:
			pool, _, one := strings.Cut(m.Payload, "/")
			id, err := kernel.ParseULID(pool)
			switch {
			case m.Resync:
				svc.WakeAll()
			case err != nil:
			case one:
				svc.WakeOne(id)
			default:
				svc.Wake(id)
			}
		}
	}
}

// PurgeBatch is how many rows one statement of Purge deletes, so no
// transaction holds an account's every pool and worker at once.
const PurgeBatch = 500

// Purge deletes the deleted account acct's runner images, pools, workers
// and nonces, PurgeBatch rows at a time. internal/app runs it for each
// accessapi.Deleted; a purge stopped part way keeps what it deleted, and
// running it again finishes.
func (d *Domain) Purge(ctx context.Context, acct kernel.AccountID) error {
	for {
		done, err := d.svc.Purge(ctx, acct, PurgeBatch)
		if err != nil || done {
			return err //nolint:wrapcheck // the store names the purge
		}
	}
}

// BindScheduler binds the runs domain's workersapi.Scheduler, a back
// edge internal/app binds after it constructs that domain, which polls
// claim runs through.
func (d *Domain) BindScheduler(s workersapi.Scheduler) { d.workers.BindScheduler(s) }

// Pools is what other domains call of the domain's pools, which
// internal/app hands the stacks domain.
func (d *Domain) Pools() workersapi.Pools { return d.svc }

// GuardPools registers g, which every later delete and move of a pool
// asks.
func (d *Domain) GuardPools(g workersapi.PoolGuard) { d.svc.Guard(g) }

// BindStacks binds the stacks domain's SpaceOf, which a stack's
// attachable pools find its space with.
func (d *Domain) BindStacks(stacks accessapi.SpaceOf) { d.svc.BindStacks(stacks) }

// SpaceGuard is the domain's say in the access domain's space deletes and
// moves, which internal/app registers there.
func (d *Domain) SpaceGuard() accessapi.SpaceGuard { return d.svc.SpaceGuard() }

// SpaceOf finds a pool's space by its ID, which internal/app registers
// with the access domain for Entity.
func (d *Domain) SpaceOf() accessapi.SpaceOf { return d.svc.SpaceOf }

// WorkersAPI is the domain's partial of the API's strict server, which
// internal/app embeds under this alias.
type WorkersAPI = http.Server

// API is the domain's partial of the API's strict server; a nil domain's
// is the zero partial, for tests that construct none.
func (d *Domain) API() WorkersAPI {
	if d == nil {
		return WorkersAPI{}
	}
	return d.api
}
