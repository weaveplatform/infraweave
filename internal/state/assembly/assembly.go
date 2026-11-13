package assembly

import (
	"context"
	"encoding/json"
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

// DB is the database the state domain stores workspaces, locks and
// versions in, and opens its transactions on.
type DB interface {
	tx.Querier
	tx.Beginner
}

// Deps are what the state domain takes from the platform and the domains
// before it: DB; Stacks, which holds a stack while its workspace is
// written and names a stack by its slug; and Access, the runs domain's
// decision of what a run token may do with its stack's state.
type Deps struct {
	DB     DB
	Stacks stacksapi.Stacks
	Access runsapi.StateAccess
}

// Domain is the state domain.
type Domain struct {
	state   *core.Service
	backend *backend.Handler
}

// New constructs the domain over d.
func New(d Deps) *Domain {
	svc := core.New(d.DB, pg.New(d.DB), d.Stacks)
	return &Domain{state: svc, backend: backend.New(d.Access, d.Stacks, svc)}
}

// Routes adds the state backend's routes to r, the API listener's router,
// under stateapi.BaseURL; on a nil domain, each answers 501.
func (d *Domain) Routes(r chi.Router) {
	if d == nil {
		backend.Unserved(r)
		return
	}
	d.backend.Routes(r)
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
