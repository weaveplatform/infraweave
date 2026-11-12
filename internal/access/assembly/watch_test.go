package assembly

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/test/deps"
)

func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, schema.Stream(), Stream())) }

// TestAccWatchSessions revokes a cached session behind the cache's back,
// as another replica does, and checks that its message drops it.
func TestAccWatchSessions(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
	}()
	db := sqltest.Open(t)
	d := mustNew(
		t,
		Deps{DB: db.Primary(), Settings: DefaultSettings(), Audience: "http://iw.test/api/v1"},
	)
	l := notify.NewListener(db.ConnConfig, slog.New(slog.NewTextHandler(io.Discard, nil)))
	sub, _ := l.Subscribe(notify.Invalidate)
	wg.Go(func() { l.Run(ctx) })
	// The connect's Resync flushes the cache whenever it arrives, so the
	// test takes it before the cache holds anything.
	if m := <-sub.C; !m.Resync {
		t.Fatalf("first message %+v, want the connect's Resync", m)
	}
	wg.Go(func() { d.WatchSessions(ctx, sub) })

	org, acct, id := kernel.NewOrganizationID(), kernel.NewAccountID(), kernel.NewULID()
	if _, err := db.Primary().Exec(ctx, `
		INSERT INTO organizations (id, slug, name) VALUES ($1, 'w', 'w');`, org); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Primary().Exec(ctx, `
		INSERT INTO accounts (organization_id, id, slug, name) VALUES ($1, $2, 'w', 'w')`,
		org, acct); err != nil {
		t.Fatal(err)
	}
	if err := d.store.CreateSession(ctx, session.Session{
		ID:             id,
		OrganizationID: org,
		AccountID:      acct,
		Channel:        session.ChannelUnknown,
		ActorKind:      kernel.PrincipalAPIKey,
		ActorID:        kernel.NewULID(),
		ExpiresAt:      time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	cache := d.authn.Cache()
	if s, err := cache.Get(ctx, id); err != nil || !s.Live(time.Now()) {
		t.Fatalf("Get = %+v, %v", s, err)
	}
	if _, err := db.Primary().Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE id = $1`,
		id); err != nil {
		t.Fatal(err)
	}
	if s, _ := cache.Get(ctx, id); !s.Live(time.Now()) {
		t.Fatal("the cache reread before any message")
	}
	if err := notify.Notify(ctx, db.Primary(), notify.Invalidate, session.Key(id)); err != nil {
		t.Fatal(err)
	}
	if err := deps.Eventually(ctx, "the message to drop the session",
		func(ctx context.Context) (bool, error) {
			s, err := cache.Get(ctx, id)
			return err == nil && !s.Live(time.Now()), err
		}); err != nil {
		t.Error(err)
	}

	// authn.sessions.expire runs against the same store.
	if err := d.Cron()[0].Handler(ctx, queue.Job{}); err != nil {
		t.Errorf("authn.sessions.expire: %v", err)
	}
}

// TestAccAccountOrganization checks the lookup a queued job's entitlement
// check makes: an account's organization, the zero ID for an account that
// does not exist, and the store's error.
func TestAccAccountOrganization(t *testing.T) {
	ctx := t.Context()
	db := sqltest.Open(t)
	d := mustNew(
		t,
		Deps{DB: db.Primary(), Settings: DefaultSettings(), Audience: "http://iw.test/api/v1"},
	)
	org, acct := kernel.NewOrganizationID(), kernel.NewAccountID()
	if _, err := db.Primary().
		Exec(ctx, `INSERT INTO organizations (id, slug, name) VALUES ($1, 'o', 'o')`,
			org); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Primary().Exec(ctx,
		`INSERT INTO accounts (organization_id, id, slug, name) VALUES ($1, $2, 'o', 'o')`, org, acct); err != nil {
		t.Fatal(err)
	}
	if got, err := d.AccountOrganization(ctx, acct); err != nil || got != org {
		t.Errorf("an account's organization = %v, %v", got, err)
	}
	if got, err := d.AccountOrganization(ctx, kernel.NewAccountID()); err != nil || !got.IsZero() {
		t.Errorf("no account = %v, %v", got, err)
	}
	db.Primary().Close()
	if _, err := d.AccountOrganization(context.Background(), acct); err == nil {
		t.Error("a closed pool answered")
	}
}
