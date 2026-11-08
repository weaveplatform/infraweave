package pg_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// listen is a connection to db's database listening on
// infraweave_invalidate.
func listen(t *testing.T, db *pgxpool.Pool) *pgx.Conn {
	t.Helper()
	c, err := pgx.ConnectConfig(t.Context(), db.Config().ConnConfig.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	if _, err := c.Exec(t.Context(), "LISTEN "+notify.Invalidate); err != nil {
		t.Fatal(err)
	}
	return c
}

// messages are the payloads c received, until none arrives for a moment.
func messages(t *testing.T, c *pgx.Conn) []string {
	t.Helper()
	var out []string
	for {
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		n, err := c.WaitForNotification(ctx)
		cancel()
		if err != nil {
			slices.Sort(out)
			return out
		}
		out = append(out, n.Payload)
	}
}

// keys returns the session messages of ids, sorted.
func keys(ids ...kernel.ULID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, session.Key(id))
	}
	slices.Sort(out)
	return out
}

// seeded is a tenant with a member and an API key, on one database.
type seeded struct {
	db     *pgxpool.Pool
	s      *pg.Store
	org    kernel.OrganizationID
	acct   kernel.AccountID
	member kernel.MemberID
	key    kernel.ULID
}

func seed(t *testing.T, db *pgxpool.Pool, slug string) seeded {
	t.Helper()
	ctx := t.Context()
	s := pg.New(db)
	o, a := tenant(t, slug, slug)
	if err := s.CreateOrganization(ctx, o, a, nil, created(o)); err != nil {
		t.Fatal(err)
	}
	m, key := kernel.NewMemberID(), kernel.NewULID()
	h, _ := apikey.Hash(apikey.NewSecret())
	if _, err := db.Exec(ctx, `INSERT INTO members (organization_id, id, login, name, email, status)
		VALUES ($1, $2, 'alice', 'Alice', 'alice@example.com', 'active')`, o.ID, m); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO api_keys (account_id, id, name, type, secret_hash,
		teams, creator) VALUES ($1, $2, 'ci', 'SECRET', $3, '{ops}', 'test')`,
		a.ID, key, h); err != nil {
		t.Fatal(err)
	}
	return seeded{db: db, s: s, org: o.ID, acct: a.ID, member: m, key: key}
}

func (x seeded) keySession(at time.Time) session.Session {
	return session.Session{
		ID: kernel.NewULID(), OrganizationID: x.org, AccountID: x.acct,
		Channel: session.ChannelUnknown, ActorKind: kernel.PrincipalAPIKey, ActorID: x.key,
		Login: x.key.String(), Name: "ci", Teams: []string{"ops"}, ExpiresAt: at,
	}
}

func (x seeded) webSession(at time.Time) session.Session {
	return session.Session{
		ID: kernel.NewULID(), OrganizationID: x.org, AccountID: x.acct, MemberID: x.member,
		Channel: session.ChannelWeb, ActorKind: kernel.PrincipalUser, ActorID: x.member.ULID,
		Login: "alice", Email: "alice@example.com", Admin: true, IPAddress: "192.0.2.7",
		ExpiresAt: at, IdleExpiresAt: at,
	}
}

func TestAccSessions(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "hooli")
	l := listen(t, db)
	later := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)

	// One key holds many sessions; a member holds one per channel.
	k1, k2 := x.keySession(later), x.keySession(later)
	w1, w2 := x.webSession(later), x.webSession(later)
	for _, ss := range []session.Session{k1, k2, w1} {
		if err := x.s.CreateSession(ctx, ss); err != nil {
			t.Fatal(err)
		}
	}
	if got := messages(t, l); len(got) != 0 {
		t.Errorf("creating sent %v", got)
	}
	if err := x.s.CreateSession(ctx, w2); err != nil {
		t.Fatal(err)
	}
	if got := messages(t, l); !slices.Equal(got, keys(w1.ID)) {
		t.Errorf("channel replacement sent %v, want %v", got, keys(w1.ID))
	}

	got, err := x.s.Session(ctx, w2.ID)
	if err != nil || got.MemberID != x.member || got.Channel != session.ChannelWeb ||
		got.IPAddress != "192.0.2.7" || !got.IdleExpiresAt.Equal(later) || !got.Admin ||
		got.CreatedAt.IsZero() || !got.Live(time.Now()) {
		t.Errorf("Session = %+v, %v", got, err)
	}
	if got, err := x.s.Session(ctx, w1.ID); err != nil || got.Live(time.Now()) {
		t.Errorf("the replaced session = %+v, %v", got, err)
	}
	if got, err := x.s.Session(ctx, k1.ID); err != nil || got.ActorID != x.key ||
		got.Teams[0] != "ops" || got.IPAddress != "" || !got.IdleExpiresAt.IsZero() {
		t.Errorf("key session = %+v, %v", got, err)
	}
	if _, err := x.s.Session(ctx, kernel.NewULID()); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("unknown session = %v", err)
	}

	list := func() []kernel.ULID {
		t.Helper()
		ss, err := x.s.Sessions(ctx, x.acct, kernel.ULID{}, 100)
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]kernel.ULID, 0, len(ss))
		for _, s := range ss {
			ids = append(ids, s.ID)
		}
		return ids
	}
	if got := list(); !slices.Equal(got, []kernel.ULID{k1.ID, k2.ID, w2.ID}) {
		t.Errorf("Sessions = %v", got)
	}
	if got, err := x.s.Sessions(ctx, x.acct, k1.ID, 1); err != nil || len(got) != 1 ||
		got[0].ID != k2.ID {
		t.Errorf("the page after the first = %+v, %v", got, err)
	}

	// Another account's session is not found.
	y := seed(t, db, "piedpiper")
	messages(t, l) // its creation's authz message
	if _, err := x.s.RevokeSession(ctx, y.acct, k1.ID); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("revoke across accounts = %v", err)
	}
	if got, err := x.s.RevokeSession(ctx, x.acct, k1.ID); err != nil || got.ID != k1.ID ||
		got.RevokedAt.IsZero() || got.Name != "ci" {
		t.Fatalf("RevokeSession = %+v, %v", got, err)
	}
	if got := messages(t, l); !slices.Equal(got, keys(k1.ID)) {
		t.Errorf("revoking sent %v", got)
	}
	if _, err := x.s.RevokeSession(ctx, x.acct, k1.ID); err != nil {
		t.Errorf("revoking again = %v", err)
	}
	messages(t, l)
	if got, err := x.s.RevokeSessionsExcept(ctx, x.acct, k2.ID); err != nil || len(got) != 1 ||
		got[0].ID != w2.ID || got[0].RevokedAt.IsZero() {
		t.Fatalf("RevokeSessionsExcept = %+v, %v", got, err)
	}
	if got := messages(t, l); !slices.Equal(got, keys(w2.ID)) {
		t.Errorf("revoking all but one sent %v", got)
	}
	if got := list(); !slices.Equal(got, []kernel.ULID{k2.ID}) {
		t.Errorf("Sessions after revoking = %v", got)
	}

	// A browser session waits for its CLI sessions to go.
	parent, child := x.keySession(later), x.keySession(later)
	for _, ss := range []session.Session{parent, child} {
		if err := x.s.CreateSession(ctx, ss); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, `UPDATE sessions SET parent_session_id = $1 WHERE id = $2`,
		parent.ID, child.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE sessions SET revoked_at = now() - interval '31 days'
		WHERE id = $1`, parent.ID); err != nil {
		t.Fatal(err)
	}
	month := time.Now().Add(-30 * 24 * time.Hour)
	if n, err := x.s.ExpireSessions(ctx, month); err != nil || n != 0 {
		t.Errorf("ExpireSessions with a child = %d, %v", n, err)
	}
	if _, err := db.Exec(ctx, `UPDATE sessions SET expires_at = now() - interval '31 days'
		WHERE id = $1`, child.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := x.s.ExpireSessions(ctx, month); err != nil || n != 1 {
		t.Errorf("ExpireSessions = %d, %v, want the child", n, err)
	}
	if n, err := x.s.ExpireSessions(ctx, month); err != nil || n != 1 {
		t.Errorf("ExpireSessions = %d, %v, want the parent", n, err)
	}
	if _, err := x.s.Session(ctx, k1.ID); err != nil {
		t.Errorf("a session revoked today was expired: %v", err)
	}
}

func TestAccAPIKeys(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "aviato")
	k, err := x.s.APIKey(ctx, x.key)
	if err != nil || k.OrganizationID != x.org || k.AccountID != x.acct || k.Name != "ci" ||
		k.Type != apikey.TypeSecret || len(k.SecretHash) != 32 || k.Teams[0] != "ops" ||
		!k.Usable(time.Now()) {
		t.Errorf("APIKey = %+v, %v", k, err)
	}
	if _, err := db.Exec(ctx, `UPDATE api_keys SET expires_at = now(), disabled_at = now(),
		deleted_at = now() WHERE id = $1`, x.key); err != nil {
		t.Fatal(err)
	}
	if k, err := x.s.APIKey(ctx, x.key); err != nil || k.ExpiresAt.IsZero() ||
		k.DisabledAt.IsZero() || k.DeletedAt.IsZero() {
		t.Errorf("a spent key = %+v, %v", k, err)
	}
	if _, err := x.s.APIKey(ctx, kernel.NewULID()); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("unknown key = %v", err)
	}

	used := func() time.Time {
		t.Helper()
		var at time.Time
		if err := db.QueryRow(ctx, `SELECT last_used_at FROM api_keys WHERE id = $1`,
			x.key).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}
	if err := x.s.TouchAPIKey(ctx, x.key); err != nil {
		t.Fatal(err)
	}
	first := used()
	if err := x.s.TouchAPIKey(ctx, x.key); err != nil || !used().Equal(first) {
		t.Errorf("a second use within the minute wrote last_used_at: %v", err)
	}
	if _, err := db.Exec(ctx, `UPDATE api_keys SET last_used_at = now() - interval '2 minutes'
		WHERE id = $1`, x.key); err != nil {
		t.Fatal(err)
	}
	if err := x.s.TouchAPIKey(ctx, x.key); err != nil || !used().After(first.Add(-time.Second)) {
		t.Errorf("a use after a minute did not write last_used_at: %v", err)
	}
}

func TestAccSessionsUnavailable(t *testing.T) {
	p := pool(t)
	s := pg.New(p)
	p.Close()
	ctx := context.Background()
	id, acct := kernel.NewULID(), kernel.NewAccountID()
	ss := session.Session{ID: id, MemberID: kernel.NewMemberID(), Channel: session.ChannelWeb}
	for name, err := range map[string]error{
		"create":     s.CreateSession(ctx, ss),
		"create key": s.CreateSession(ctx, session.Session{ID: id}),
		"session":    func() error { _, err := s.Session(ctx, id); return err }(),
		"sessions":   func() error { _, err := s.Sessions(ctx, acct, id, 1); return err }(),
		"revoke":     func() error { _, err := s.RevokeSession(ctx, acct, id); return err }(),
		"revoke all": func() error { _, err := s.RevokeSessionsExcept(ctx, acct, id); return err }(),
		"expire":     func() error { _, err := s.ExpireSessions(ctx, time.Now()); return err }(),
		"api key":    func() error { _, err := s.APIKey(ctx, id); return err }(),
		"touch":      s.TouchAPIKey(ctx, id),
	} {
		if err == nil || errors.Is(err, port.ErrNotFound) {
			t.Errorf("%s on a closed pool = %v", name, err)
		}
	}
}

// TestAccSessionsAborted joins each write to a transaction a failed
// statement aborted, so every statement in it fails as one mid-transaction
// would.
func TestAccSessionsAborted(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "raviga")
	ss := x.webSession(time.Now().Add(time.Hour))
	errAborted := errors.New("aborted")
	err := tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
		_, _ = tx.Q(ctx, db).Exec(ctx, `SELECT 1/0`)
		for name, err := range map[string]error{
			"create":     x.s.CreateSession(ctx, ss),
			"create key": x.s.CreateSession(ctx, x.keySession(ss.ExpiresAt)),
			"sessions":   func() error { _, err := x.s.Sessions(ctx, x.acct, ss.ID, 1); return err }(),
			"revoke":     func() error { _, err := x.s.RevokeSession(ctx, x.acct, ss.ID); return err }(),
			"revoke all": func() error { _, err := x.s.RevokeSessionsExcept(ctx, x.acct, ss.ID); return err }(),
		} {
			if err == nil || errors.Is(err, port.ErrNotFound) {
				t.Errorf("%s in an aborted transaction = %v", name, err)
			}
		}
		return errAborted
	})
	if !errors.Is(err, errAborted) {
		t.Fatal(err)
	}
}
