package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/idempotency"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/ratelimit"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/serve"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/blobs"
	"github.com/weaveplatform/infraweave/internal/platform/observe"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
)

// Mode is a process mode: the roles one process runs.
//
// ref: decision 0020
type Mode struct {
	// API serves the API on the API listener.
	API bool
	// Scheduler runs the scheduler's loops, which arrive with their phases;
	// until then it serves only the admin listener.
	Scheduler bool
}

// The process modes infraweave serve, scheduler and all run.
var (
	ModeServe     = Mode{API: true}
	ModeScheduler = Mode{Scheduler: true}
	ModeAll       = Mode{API: true, Scheduler: true}
)

// String is the mode field of every log line: serve, scheduler, or
// serve,scheduler for all.
func (m Mode) String() string {
	var roles []string
	if m.API {
		roles = append(roles, "serve")
	}
	if m.Scheduler {
		roles = append(roles, "scheduler")
	}
	return strings.Join(roles, ",")
}

// Run runs mode m until ctx is done, then shuts down in order (serve.Run):
// /readyz fails at once, the listeners keep accepting for
// server.shutdown.ready_delay so load balancers stop routing to the
// process, and in-flight requests drain. It opens PostgreSQL and brings the
// schema to this binary's (openDatabase) before any listener binds, so a
// server below the version floor or a schema this binary must not run on
// never answers. The keyring loads next, so a replica with the wrong KEK,
// or none, never answers either. The API's controls keep their state in the
// database (postgresGuards); the listening connection and the pools close
// only after the drain, when no request can use them.
//
// ref: decision 0017
func Run(ctx context.Context, c config.Config, log *slog.Logger, m Mode) error {
	errs := []error{c.Server.Validate(), c.Database.Validate(), c.ObjectStore.Require()}
	if m.API {
		errs = append(errs, c.Server.RequirePublicURL())
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	// Every mode Run starts seals, so each needs the KEK; migrate and
	// version never call Run.
	provider, closeKEK, err := openKEK(ctx, c.Secrets.KEK)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	defer closeKEK()
	kms, closeKMS, err := openSigners(ctx, c.Auth.Signing, c.Secrets.KEK.Vault)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	defer closeKMS()
	db, err := openDatabase(ctx, c.Database, log, Streams())
	if err != nil {
		return err
	}
	defer db.Close()
	keys, err := crypt.Load(ctx, db.Primary(), provider)
	if err != nil {
		return err //nolint:wrapcheck // names the keyring and the key
	}
	objs, err := openObjects(ctx, c.ObjectStore, keys)
	if err != nil {
		return err
	}
	defer objs.close()
	// ctx ends when the shutdown begins; the switch keeps following
	// invalidations until the drain is over.
	background, stop := context.WithCancel(context.WithoutCancel(ctx))
	var wg sync.WaitGroup
	defer func() {
		stop()
		wg.Wait()
	}()
	health := observe.NewHealth(time.Now)
	databaseChecks(health, db)
	keyringCheck(health, keys)
	// One listening connection per process; every subscriber subscribes
	// before it runs, so none misses the first Resync.
	l := notify.NewListener(db.ConnConfig, log)
	keysChanged, _ := l.Subscribe(notify.Invalidate) // one of notify.Channels, so never an error
	wg.Go(func() { keys.Watch(background, keysChanged, log) })
	var g Guards
	if m.API {
		g = postgresGuards(background, &wg, c.Server, db, keys, l)
	}
	// Every domain appends to and subscribes on this one outbox, and
	// registers its event kinds, before the scheduler starts.
	events := outbox.New(db.Primary(), queue.New(db.Primary()), outbox.NewKinds())
	domains, err := newDomains(c, db, keys, kms)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	// After the migrations, which Open ran, and before any role reads a
	// tier; every replica seeds, and the seed's lock makes all but the
	// first a no-op.
	if err := domains.Features.Seed(ctx); err != nil {
		return err //nolint:wrapcheck // names the seed
	}
	if m.API {
		revoked, _ := l.Subscribe(notify.Invalidate) // one of notify.Channels, so never an error
		wg.Go(func() { domains.Access.WatchSessions(background, revoked) })
		granted, _ := l.Subscribe(notify.Invalidate) // one of notify.Channels, so never an error
		wg.Go(func() { domains.Access.WatchAuthorization(background, granted, log) })
		entitlements, _ := l.Subscribe(
			notify.Invalidate,
		) // one of notify.Channels, so never an error
		wg.Go(func() { domains.Features.WatchEntitlements(background, entitlements) })
	}
	stopScheduler := func() {}
	if m.Scheduler {
		if stopScheduler, err = startScheduler(
			ctx,
			c,
			db,
			keys,
			provider,
			l,
			events,
			objs.store,
			domains.cron(),
			domains,
			log,
			health,
		); err != nil {
			return err
		}
	}
	wg.Go(func() { l.Run(background) })
	api, admin := handlers(
		log,
		c.Server,
		m,
		g,
		domains,
		platform(db.Primary(), provider),
		serving(c.ObjectStore, db.Primary(), keys, objs),
		health,
	)
	err = serve.Run(ctx, c.Server.Config, log, api, admin, health.Stop)
	stopScheduler()
	return err //nolint:wrapcheck // serve's errors name the listener
}

// databaseChecks are P2's probe checks, which every mode runs: the
// configuration decoded, which it has by the time any probe answers, and
// the schema at this binary's version, at startup; PostgreSQL answering
// through the pool, for readiness.
func databaseChecks(h *observe.Health, db *sql.DB) {
	h.Startup("config", func(context.Context) error { return nil })
	h.Startup("schema", func(ctx context.Context) error {
		cc, err := db.ConnConfig(ctx)
		if err != nil {
			return err //nolint:wrapcheck // reported as the check's error
		}
		return migrate.Check(ctx, cc, Streams()) //nolint:wrapcheck // as above
	})
	h.Ready("postgres", func(ctx context.Context) error {
		_, err := db.Primary().Exec(ctx, "SELECT 1")
		return err //nolint:wrapcheck // as above
	})
}

// keyringCheck is P2b's startup check: the keyring loaded, with an active
// key to seal under, which it has by the time any probe answers.
func keyringCheck(h *observe.Health, keys *crypt.Keyring) {
	h.Startup("keyring", func(context.Context) error {
		if keys.Active() == 0 {
			return crypt.ErrKeyring
		}
		return nil
	})
}

// handlers are mode m's two listeners' handlers: the API, behind the
// controls whose state g holds, serving the operations of the domains d
// and the installation's own through p, and holding objects as b, or nil
// when m serves no API, so serve binds no API listener; and the probes,
// which answer from health.
func handlers(
	log *slog.Logger,
	c config.Server,
	m Mode,
	g Guards,
	d Domains,
	p Platform,
	b Blobs,
	health *observe.Health,
) (api, admin http.Handler) {
	if m.API {
		api = API(log, c, g, d, p, b)
	}
	return api, observe.Probes(log, health)
}

// postgresGuards keeps the controls' state in db, shared by every replica,
// since replicas are stateless and share state only through the stores,
// with stored bodies sealed under keys. The disabled callers are both
// lists, the configuration's and api_disabled_callers; the table's copy
// follows infraweave_invalidate through l, in a watcher that runs, with wg,
// until ctx ends.
//
// ref: decision 0005
func postgresGuards(
	ctx context.Context,
	wg *sync.WaitGroup,
	c config.Server,
	db *sql.DB,
	keys *crypt.Keyring,
	l *notify.Listener,
) Guards {
	sub, _ := l.Subscribe(notify.Invalidate) // one of notify.Channels, so never an error
	table := ratelimit.NewTable(db.Primary(), time.Now)
	wg.Go(func() { table.Watch(ctx, sub) })
	return Guards{
		Switch:  ratelimit.Switches{ratelimit.NewCallers(c.RateLimit.DisabledCallers), table},
		Buckets: ratelimit.NewPostgres(db.Primary()),
		Keys:    idempotency.NewPostgres(db.Primary(), c.Idempotency, keys),
	}
}

// platform is the installation's own operations' reach: the queue on pool,
// and the data key's rotation, wrapped by provider, whatever its age.
func platform(pool keysDB, provider kek.Provider) Platform {
	q := queue.New(pool)
	return Platform{
		Queues: q,
		Dead:   q,
		RotateKey: func(ctx context.Context) (uint32, error) {
			return rotateKeys(ctx, pool, q, provider, 0)
		},
	}
}

// serving is the API role's object storage: blob records over
// objectstore.WriterOnly of the store, so no serve dependency holds the
// Store, and the fs driver's routes.
func serving(c objectstore.Config, pool queue.DB, keys *crypt.Keyring, o objects) Blobs {
	w := objectstore.WriterOnly(o.store)
	return Blobs{
		Objects: blobs.New(pool, queue.New(pool), w, keys, c),
		Routes:  o.routes,
	}
}
