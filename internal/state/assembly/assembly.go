package assembly

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
	"github.com/weaveplatform/infraweave/internal/state/internal/backend"
	"github.com/weaveplatform/infraweave/internal/state/internal/core"
	"github.com/weaveplatform/infraweave/internal/state/internal/pg"
)

// Stream is the domain's migration stream, which internal/app registers
// after runs'.
func Stream() migrate.Stream { return pg.Stream() }

// Held are the state columns that hold other domains' rows, for the
// nightly orphan count.
func Held() []integrity.Held { return pg.Held() }

// Settings are the state domain's, under state in the configuration:
// MaxSize, state.max_size, is the largest state version in bytes a write
// may carry.
//
// ref: decision 0010 § "Writing a version"
type Settings struct {
	MaxSize int64 `mapstructure:"max_size"`
}

// DefaultSettings bound a state version at 512 MiB.
func DefaultSettings() Settings { return Settings{MaxSize: core.DefaultMaxSize} }

// ErrConfig reports a state setting the server could not start with.
var ErrConfig = errors.New("invalid state configuration")

// Validate rejects a state.max_size that is not positive.
func (s Settings) Validate() error {
	if s.MaxSize <= 0 {
		return fmt.Errorf("%w: state.max_size must be positive, got %d", ErrConfig, s.MaxSize)
	}
	return nil
}

// DB is the database the state domain stores workspaces, locks and
// versions in, and opens its transactions on.
type DB interface {
	tx.Querier
	tx.Beginner
}

// Objects are the objects of the states store, as blob_objects records
// them; blobs.Objects is one.
type Objects = core.Objects

// Deps are what the state domain takes from the platform and the domains
// before it: DB; Stacks, which holds a stack while its workspace is
// written and names a stack by its slug; Access, the runs domain's
// decision of what a run token may do with its stack's state; Objects,
// which hold each version's object; Jobs, which enqueues a deleted
// stack's purge; and Settings.
type Deps struct {
	DB       DB
	Stacks   stacksapi.Stacks
	Access   runsapi.StateAccess
	Objects  Objects
	Jobs     core.Jobs
	Settings Settings
}

// Domain is the state domain.
type Domain struct {
	state   *core.Service
	backend *backend.Handler
}

// New constructs the domain over d.
func New(d Deps) *Domain {
	svc := core.New(core.Deps{
		DB: d.DB, Store: pg.New(d.DB), Stacks: d.Stacks, Objects: d.Objects, Jobs: d.Jobs,
	})
	return &Domain{state: svc, backend: backend.New(d.Access, d.Stacks, svc, d.Settings.MaxSize)}
}

// Versions are what other domains read of a stack's managed state.
func (d *Domain) Versions() stateapi.Versions { return d.state }

// Routes adds the state backend's routes to r, the API listener's router,
// under stateapi.BaseURL; on a nil domain, each answers 501.
func (d *Domain) Routes(r chi.Router) {
	if d == nil {
		backend.Unserved(r)
		return
	}
	d.backend.Routes(r)
}

// StackGuard is the domain's guard of a stack's delete, which enqueues
// the purge of its state; internal/app registers it with the stacks
// domain.
func (d *Domain) StackGuard() stacksapi.StackGuard { return d.state }

// StackPurge is the job that purges a deleted stack's state.
var StackPurge = core.PurgeKind

// Jobs are the domain's job handlers, which the scheduler registers:
// StackPurge's.
func (d *Domain) Jobs() []queue.Registration {
	return []queue.Registration{{Kind: StackPurge, Handler: d.state.Purged}}
}

// Purge deletes the deleted account acct's state, its versions' objects
// with them, core.PurgeBatch rows a statement.
func (d *Domain) Purge(ctx context.Context, acct kernel.AccountID) error {
	return d.state.Purge(ctx, acct, nil) //nolint:wrapcheck // the core names the purge
}

// LockRelease is the consumer that releases a run's locks once its
// run_updated names a terminal state; every replica subscribes it, and the
// outbox runs each event once.
const LockRelease = "state.lock_release"

// Subscribe subscribes the domain's consumers on events.
func (d *Domain) Subscribe(events *outbox.Outbox) error {
	//nolint:wrapcheck // names the consumer
	return events.Subscribe(runsapi.UpdatedKind, LockRelease, runUpdated(d.state.RunEnded))
}

// runUpdated is the outbox handler that decodes a runsapi.Updated and
// runs end on its run and state; a document that does not decode never
// will, so its job dies at once.
func runUpdated(
	end func(ctx context.Context, acct kernel.AccountID, run kernel.ULID, st kernel.RunState) error,
) outbox.Handler {
	return func(ctx context.Context, ev outbox.Event, _ queue.Job) error {
		var u runsapi.Updated
		if err := json.Unmarshal(ev.Document, &u); err != nil {
			return queue.Permanent(
				fmt.Errorf("%s %s: %w", ev.Kind, ev.ID, err),
			) //nolint:wrapcheck // marks it
		}
		return end(ctx, kernel.AccountID{ULID: ev.Account}, u.Run.ID, u.State)
	}
}
