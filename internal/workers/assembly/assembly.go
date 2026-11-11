package assembly

import (
	"context"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/workers/internal/adapters/http"
	"github.com/weaveplatform/infraweave/internal/workers/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
)

// Stream is the domain's migration stream, which internal/app registers
// after access'.
func Stream() migrate.Stream { return pg.Stream() }

// Held are the workers columns that hold other domains' rows, for the
// nightly orphan count.
func Held() []integrity.Held { return pg.Held() }

// SealedColumns are the workers domain's sealed columns.
func SealedColumns() []crypt.Column { return pg.SealedColumns() }

// Entity is the kind of entity the access domain's middleware finds a
// space for, as the API's x-infraweave-subject names it: a pool, in its
// own space.
const Entity = "worker_pool"

// DB is the pool the domain queries and begins its transactions on.
type DB interface {
	tx.Querier
	tx.Beginner
}

// Deps are what the workers domain takes from the platform and the
// domains below it: the database, the keys the pool CA's key is sealed
// under, the spaces a pool lives in and holds, the authorizer that
// decides the checks beyond each route's and a pool's allowedActions,
// and Public, the server's public URL, which a pool token names.
type Deps struct {
	DB     DB
	Keys   pg.Sealer
	Spaces accessapi.Spaces
	Authz  accessapi.Authorizer
	Public string
	Now    func() time.Time
}

// Domain is the workers domain, constructed once per process.
type Domain struct {
	svc *pool.Service
	api http.Server
}

// New constructs the domain on d.
func New(d Deps) *Domain {
	svc := pool.New(pool.Deps{
		Store: pg.New(d.DB, d.Keys), DB: d.DB, Spaces: d.Spaces, Authz: d.Authz,
		Public: d.Public, Now: d.Now,
	})
	return &Domain{svc: svc, api: http.New(svc, d.Authz, d.Spaces)}
}

// PurgeBatch is how many rows one statement of Purge deletes, so no
// transaction holds an account's every pool and worker at once.
const PurgeBatch = 500

// Purge deletes the deleted account acct's pools, workers and nonces,
// PurgeBatch rows at a time. internal/app runs it for each
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
