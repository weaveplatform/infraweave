package pg_test

import (
	"context"
	"errors"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authz"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/settings"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/test/deps"
)

func schemeFixture(t *testing.T) (seeded, kernel.Principal, kernel.Principal) {
	t.Helper()
	x := seed(t, pool(t), "scheme")
	_, err := x.db.Exec(t.Context(), `INSERT INTO role_bindings
		(account_id, id, actor_kind, actor_id, role_id, space_id, origin, created_by)
		SELECT $1, $2, 'api_key', $3, id, $1, 'binding', 'acceptance'
		FROM roles WHERE account_id = $1 AND slug = 'space-admin'`, x.acct, kernel.NewULID(), x.key)
	if err != nil {
		t.Fatal(err)
	}
	caller, peer := x.keySession(time.Now().Add(time.Hour)), x.keySession(time.Now().Add(time.Hour))
	if err := x.s.CreateSession(t.Context(), caller); err != nil {
		t.Fatal(err)
	}
	if err := x.s.CreateSession(t.Context(), peer); err != nil {
		t.Fatal(err)
	}
	return x, caller.Principal(), peer.Principal()
}

func TestAccAuthorizationSchemeRollback(t *testing.T) {
	x, caller, peer := schemeFixture(t)
	var dropped int
	// The real outbox rejects an unregistered event after the transaction has
	// already updated the scheme, revoked peers and recorded its audit entry.
	s := pg.New(x.db).WithEvents(outbox.New(x.db, queue.New(x.db), outbox.NewKinds())).
		WithSnapshots(func(kernel.AccountID) { dropped++ })
	_, err := s.SetAuthorizationScheme(t.Context(), caller, accessapi.LoginPolicies,
		onTrail(x, settings.ActionAuthorizationScheme))
	if err == nil {
		t.Fatal("an unregistered outbox event committed")
	}
	st, err := x.s.Settings(t.Context(), x.acct)
	if err != nil || st.AuthorizationScheme != accessapi.ManagedUsers {
		t.Fatalf("rolled-back scheme = %s, %v", st.AuthorizationScheme, err)
	}
	ss, err := x.s.Session(t.Context(), peer.SessionID)
	if err != nil || !ss.Live(time.Now()) {
		t.Fatalf("rolled-back peer = %+v, %v", ss, err)
	}
	var grants, audit, events int
	err = x.db.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM session_grants),
		(SELECT count(*) FROM audit_trail_entries WHERE action = $1),
		(SELECT count(*) FROM events WHERE kind = $2)`, settings.ActionAuthorizationScheme, accessapi.SchemeChangedKind).
		Scan(&grants, &audit, &events)
	if err != nil || grants != 0 || audit != 0 || events != 0 || dropped != 0 {
		t.Fatalf("rollback grants/audit/events/drop = %d/%d/%d/%d, %v", grants, audit, events, dropped, err)
	}
}

func TestAccAuthorizationSchemeCallerFences(t *testing.T) {
	x, caller, peer := schemeFixture(t)
	entry := onTrail(x, settings.ActionAuthorizationScheme)
	for _, tc := range []struct {
		name string
		p    kernel.Principal
		want error
	}{
		{"no account", kernel.Principal{AccountID: kernel.NewAccountID()}, port.ErrNotFound},
		{"other account session", kernel.Principal{AccountID: x.acct, SessionID: kernel.NewULID()}, settings.ErrSessionEnded},
		{"other actor", kernel.Principal{AccountID: x.acct, SessionID: caller.SessionID, Kind: caller.Kind, ActorID: kernel.NewULID()}, settings.ErrSessionEnded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := x.s.SetAuthorizationScheme(t.Context(), tc.p, accessapi.LoginPolicies, entry); !errors.Is(err, tc.want) {
				t.Fatalf("switch = %v, want %v", err, tc.want)
			}
		})
	}
	if _, err := x.s.SetAuthorizationScheme(t.Context(), caller, "UNKNOWN", entry); !errors.Is(err, settings.ErrInvalid) {
		t.Fatalf("unknown strategy = %v", err)
	}
	if _, err := x.db.Exec(t.Context(), `DELETE FROM role_bindings WHERE account_id = $1`, x.acct); err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.SetAuthorizationScheme(t.Context(), caller, accessapi.LoginPolicies, entry); !errors.Is(err, settings.ErrNotAdmin) {
		t.Fatalf("stale admin = %v", err)
	}
	if _, err := x.s.EndSession(t.Context(), peer.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.SetAuthorizationScheme(t.Context(), peer, accessapi.LoginPolicies, entry); !errors.Is(err, settings.ErrSessionEnded) {
		t.Fatalf("ended caller = %v", err)
	}
}

func TestAccAuthorizationSchemeConcurrentFence(t *testing.T) {
	x, caller, peer := schemeFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	hold, err := x.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck // a committed transaction is already closed
	var held kernel.AccountID
	if err := hold.QueryRow(ctx, `SELECT id FROM accounts WHERE id = $1 FOR NO KEY UPDATE`, x.acct).Scan(&held); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for _, p := range []kernel.Principal{caller, peer} {
		go func() {
			_, err := x.s.SetAuthorizationScheme(ctx, p, accessapi.LoginPolicies, onTrail(x, settings.ActionAuthorizationScheme))
			results <- err
		}()
	}
	if err := deps.Eventually(ctx, "both administrators waiting on the account fence", func(ctx context.Context) (bool, error) {
		var waiting int
		err := x.db.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()
			AND wait_event_type = 'Lock' AND query LIKE '%SELECT authorization_scheme FROM accounts%'`).Scan(&waiting)
		return waiting == 2, err
	}); err != nil {
		t.Fatal(err)
	}
	if err := hold.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	first, second := <-results, <-results
	if (first != nil || !errors.Is(second, settings.ErrSessionEnded)) && (second != nil || !errors.Is(first, settings.ErrSessionEnded)) {
		t.Fatalf("concurrent switch results = %v, %v", first, second)
	}
	d, err := x.s.AuthzData(ctx, x.acct)
	if err != nil || len(d.Sessions) != 1 {
		t.Fatalf("retained session grants = %+v, %v", d.Sessions, err)
	}
	for _, p := range []kernel.Principal{caller, peer} {
		ss, err := x.s.Session(ctx, p.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		if authz.Build(d).Admin(authz.WhoOf(p)) != ss.Live(time.Now()) {
			t.Fatal("revoked caller retained administration")
		}
	}
}

func TestAccAuthorizationSchemeGrantLifetime(t *testing.T) {
	x, caller, _ := schemeFixture(t)
	entry := onTrail(x, settings.ActionAuthorizationScheme)
	for _, scheme := range []accessapi.AuthorizationScheme{accessapi.LoginPolicies, accessapi.ManagedUsers, accessapi.LoginPolicies} {
		if _, err := x.s.SetAuthorizationScheme(t.Context(), caller, scheme, entry); err != nil {
			t.Fatal(err)
		}
	}
	d, err := x.s.AuthzData(t.Context(), x.acct)
	if err != nil || len(d.Sessions) != 1 {
		t.Fatalf("repeated switches duplicated the retained grant: %d, %v", len(d.Sessions), err)
	}
	before, err := x.s.Session(t.Context(), caller.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(t.Context(), `UPDATE sessions SET expires_at = now() - interval '1 second' WHERE id = $1`, caller.SessionID); err != nil {
		t.Fatal(err)
	}
	d, err = x.s.AuthzData(t.Context(), x.acct)
	if err != nil || len(d.Sessions) != 0 {
		t.Fatalf("an expired session still supplied retained grants: %d, %v", len(d.Sessions), err)
	}
	if _, err := x.s.SetAuthorizationScheme(t.Context(), caller, accessapi.ManagedUsers, entry); !errors.Is(err, settings.ErrSessionEnded) {
		t.Fatalf("expired caller switched: %v", err)
	}
	if _, err := x.s.ExpireSessions(t.Context(), before.ExpiresAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var grants int
	if err := x.db.QueryRow(t.Context(), `SELECT count(*) FROM session_grants WHERE account_id = $1`, x.acct).Scan(&grants); err != nil || grants != 0 {
		t.Fatalf("ended-session grants survived collection: %d, %v", grants, err)
	}
}

func TestAccAuthorizationSchemeUnavailable(t *testing.T) {
	x, caller, _ := schemeFixture(t)
	err := tx.Do(t.Context(), x.db, tx.Options{}, func(ctx context.Context) error {
		var pid int32
		if err := tx.Q(ctx, x.db).QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
			return err
		}
		if _, err := x.db.Exec(ctx, `SELECT pg_terminate_backend($1)`, pid); err != nil {
			return err
		}
		_, err := x.s.SetAuthorizationScheme(ctx, caller, accessapi.LoginPolicies, onTrail(x, settings.ActionAuthorizationScheme))
		return err
	})
	if !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("unavailable database = %v", err)
	}
}

func TestAccAuthorizationSchemeRevokesAdmissionBeforeFence(t *testing.T) {
	x, caller, _ := schemeFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	issued := x.keySession(time.Now().Add(time.Hour))
	held := make(chan int32, 1)
	release := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		results <- tx.Do(ctx, x.db, tx.Options{}, func(ctx context.Context) error {
			var pid int32
			if err := tx.Q(ctx, x.db).QueryRow(ctx, `SELECT pg_backend_pid() FROM accounts WHERE id = $1 FOR SHARE`, x.acct).Scan(&pid); err != nil {
				return err
			}
			if err := x.s.CreateSession(ctx, issued); err != nil {
				return err
			}
			held <- pid
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		})
	}()
	var blocker int32
	select {
	case blocker = <-held:
	case <-ctx.Done():
		t.Fatal(context.Cause(ctx))
	}
	go func() {
		_, err := x.s.SetAuthorizationScheme(ctx, caller, accessapi.LoginPolicies, onTrail(x, settings.ActionAuthorizationScheme))
		results <- err
	}()
	if err := deps.Eventually(ctx, "scheme switch waiting behind session issuance", func(ctx context.Context) (bool, error) {
		var waiting bool
		err := x.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1::integer=ANY(pg_blocking_pids(pid)))`, blocker).Scan(&waiting)
		return waiting, err
	}); err != nil {
		t.Fatal(err)
	}
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	ss, err := x.s.Session(ctx, issued.ID)
	if err != nil || ss.Live(time.Now()) {
		t.Fatalf("session admitted before the switch fence remained usable: revoked=%v, error=%v", ss.RevokedAt, err)
	}
}
