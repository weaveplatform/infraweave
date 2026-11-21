package pg_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authz"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/role"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/settings"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// terminateLoginQuery makes a selected production query encounter a real
// connection failure. It never substitutes a query result or injected error.
type terminateLoginQuery struct {
	db      *pgxpool.Pool
	query   string
	payload string
	fired   atomic.Bool
	err     error
}

func (f *terminateLoginQuery) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !strings.Contains(data.SQL, f.query) || f.payload != "" && !slices.ContainsFunc(data.Args, func(arg any) bool {
		value, ok := arg.(string)
		return ok && strings.HasPrefix(value, f.payload)
	}) || !f.fired.CompareAndSwap(false, true) {
		return ctx
	}
	_, f.err = f.db.Exec(ctx, `SELECT pg_terminate_backend($1)`, conn.PgConn().PID())
	return ctx
}

func (*terminateLoginQuery) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestAccLoginPolicyChangesRollBackAtPostgresFailureBoundaries(t *testing.T) {
	for _, stage := range []struct{ name, query, payload string }{
		{"editor", "FROM sessions", ""},
		{"spaces", "inherit_entities FROM spaces", ""},
		{"roles", "FROM roles r LEFT JOIN role_actions", ""},
		{"bindings", "FROM role_bindings WHERE", ""},
		{"grants", "FROM session_grants g", ""},
		{"groups", "FROM managed_user_groups", ""},
		{"revocation", "WITH revoked AS", ""},
		{"session invalidation", "SELECT pg_notify", "session:"},
		{"version", "UPDATE accounts SET authz_version", ""},
		{"snapshot invalidation", "SELECT pg_notify", "authz:"},
	} {
		t.Run(stage.name, func(t *testing.T) {
			x, editor, peer := loginChangeFixture(t)
			before, err := x.s.AuthzData(t.Context(), x.acct)
			if err != nil {
				t.Fatal(err)
			}
			listener := listen(t, x.db)
			failure := &terminateLoginQuery{db: x.db, query: stage.query, payload: stage.payload}
			config := x.db.Config()
			config.ConnConfig.Tracer = failure
			traced, err := pgxpool.NewWithConfig(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(traced.Close)
			var sessionDrops, snapshotDrops int
			store := pg.New(traced).
				WithSessionInvalidations(func(kernel.ULID) { sessionDrops++ }).
				WithSnapshots(func(kernel.AccountID) { snapshotDrops++ })
			err = store.LoginPolicyChanged(t.Context(), editor)
			if !failure.fired.Load() || failure.err != nil || !errors.Is(err, kernel.ErrUnavailable) {
				t.Fatalf("controlled query failure not observed: fired=%t terminate=%v operation=%v", failure.fired.Load(), failure.err, err)
			}
			after, err := x.s.AuthzData(t.Context(), x.acct)
			currentPeer, peerErr := x.s.Session(t.Context(), peer.SessionID)
			currentEditor, editorErr := x.s.Session(t.Context(), editor.SessionID)
			if err != nil || peerErr != nil || editorErr != nil || !currentPeer.Live(time.Now()) || !currentEditor.Live(time.Now()) ||
				after.Version != before.Version || !reflect.DeepEqual(after.Sessions, before.Sessions) || sessionDrops != 0 || snapshotDrops != 0 {
				t.Fatalf("failed LOGIN change leaked security effects: version=%d/%d drops=%d/%d errors=%v/%v/%v",
					after.Version, before.Version, sessionDrops, snapshotDrops, err, peerErr, editorErr)
			}
			if got := messages(t, listener); len(got) != 0 {
				t.Fatalf("failed LOGIN change emitted invalidations: %v", got)
			}
		})
	}
}

func loginChangeFixture(t *testing.T) (seeded, kernel.Principal, kernel.Principal) {
	t.Helper()
	x, editor, _ := schemeFixture(t)
	if _, err := x.s.SetAuthorizationScheme(t.Context(), editor, accessapi.LoginPolicies,
		onTrail(x, settings.ActionAuthorizationScheme)); err != nil {
		t.Fatal(err)
	}
	peer := x.keySession(time.Now().Add(time.Hour))
	if err := x.s.CreateSession(t.Context(), peer); err != nil {
		t.Fatal(err)
	}
	return x, editor, peer.Principal()
}

func TestAccLoginPolicyChangeEffectsCommitTogether(t *testing.T) {
	x, editor, peer := loginChangeFixture(t)
	ctx := t.Context()
	var dropped []kernel.ULID
	x.s.WithSessionInvalidations(func(id kernel.ULID) { dropped = append(dropped, id) })
	before, err := x.s.AuthzData(ctx, x.acct)
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("final policy event rejected")
	err = tx.Do(ctx, x.db, tx.Options{}, func(ctx context.Context) error {
		if err := x.s.HoldAccount(ctx, x.acct); err != nil {
			return err
		}
		if err := x.s.LoginPolicyChanged(ctx, editor); err != nil {
			return err
		}
		return injected
	})
	if !errors.Is(err, injected) || len(dropped) != 0 {
		t.Fatalf("rolled-back LOGIN change leaked cache invalidation: %v, %v", err, dropped)
	}
	stored, err := x.s.Session(ctx, peer.SessionID)
	rolledBack, readErr := x.s.AuthzData(ctx, x.acct)
	if err != nil || readErr != nil || !stored.Live(time.Now()) || rolledBack.Version != before.Version {
		t.Fatalf("rolled-back security effects survived: version=%d want=%d, errors=%v/%v", rolledBack.Version, before.Version, err, readErr)
	}
	if err := x.s.LoginPolicyChanged(ctx, editor); err != nil {
		t.Fatal(err)
	}
	stored, err = x.s.Session(ctx, peer.SessionID)
	committed, readErr := x.s.AuthzData(ctx, x.acct)
	currentEditor, editorErr := x.s.Session(ctx, editor.SessionID)
	if err != nil || readErr != nil || editorErr != nil || stored.Live(time.Now()) || !currentEditor.Live(time.Now()) ||
		committed.Version != before.Version+1 || !slices.Equal(dropped, []kernel.ULID{peer.SessionID}) {
		t.Fatalf("committed LOGIN effects mismatch: version=%d dropped=%v errors=%v/%v/%v", committed.Version, dropped, err, readErr, editorErr)
	}
}

func TestAccLoginPolicyChangePreservesInactiveConfiguration(t *testing.T) {
	x, editor, peer := schemeFixture(t)
	var drops int
	x.s.WithSessionInvalidations(func(kernel.ULID) { drops++ })
	before, err := x.s.AuthzData(t.Context(), x.acct)
	if err != nil {
		t.Fatal(err)
	}
	if err := x.s.LoginPolicyChanged(t.Context(), editor); err != nil {
		t.Fatal(err)
	}
	after, err := x.s.AuthzData(t.Context(), x.acct)
	ss, sessionErr := x.s.Session(t.Context(), peer.SessionID)
	if err != nil || sessionErr != nil || before.Version != after.Version || drops != 0 || !ss.Live(time.Now()) {
		t.Fatalf("inactive LOGIN settings revoked managed authorization: %v/%v, drops=%d", err, sessionErr, drops)
	}
}

func TestAccLoginPolicyChangeAcceptsTheCurrentSpaceAdminAction(t *testing.T) {
	x, editor, peer := loginChangeFixture(t)
	ctx := t.Context()
	r := role.Role{
		AccountID: x.acct, ID: kernel.NewULID(), Slug: "policy-editor", Name: "Policy editor", Version: 1,
		Actions: []accessapi.Action{accessapi.ActionSpaceAdmin},
	}
	if err := x.s.CreateRole(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(ctx, `DELETE FROM role_bindings WHERE account_id=$1 AND actor_kind='api_key' AND actor_id=$2`, x.acct, x.key); err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(ctx, `DELETE FROM session_grants WHERE session_id=$1`, editor.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(ctx, `INSERT INTO session_grants (account_id,id,session_id,role_id,space_id,origin)
		VALUES ($1,$2,$3,$4,$1,'ASSIGNED_ROLE')`, x.acct, kernel.NewULID(), editor.SessionID, r.ID); err != nil {
		t.Fatal(err)
	}
	data, err := x.s.AuthzData(ctx, x.acct)
	if err != nil || authz.Build(data).Admin(authz.WhoOf(editor)) {
		t.Fatalf("custom action fixture unexpectedly has system root administration: %v", err)
	}
	if err := x.s.LoginPolicyChanged(ctx, editor); err != nil {
		t.Fatalf("LOGIN editing incorrectly required system root administration: %v", err)
	}
	ss, err := x.s.Session(ctx, peer.SessionID)
	if err != nil || ss.Live(time.Now()) {
		t.Fatalf("custom authorized editor failed to revoke its peer: %v", err)
	}
}

func TestAccLoginPolicyChangeRefusesAStaleEditor(t *testing.T) {
	for _, source := range []string{"ended", "other actor", "other organization", "no admin action"} {
		t.Run(source, func(t *testing.T) {
			x, editor, peer := loginChangeFixture(t)
			switch source {
			case "ended":
				if _, err := x.s.EndSession(t.Context(), editor.SessionID); err != nil {
					t.Fatal(err)
				}
			case "other actor":
				editor.ActorID = kernel.NewULID()
			case "other organization":
				editor.OrganizationID = kernel.NewOrganizationID()
			case "no admin action":
				if _, err := x.db.Exec(t.Context(), `DELETE FROM role_bindings WHERE account_id=$1`, x.acct); err != nil {
					t.Fatal(err)
				}
				if _, err := x.db.Exec(t.Context(), `DELETE FROM session_grants WHERE account_id=$1`, x.acct); err != nil {
					t.Fatal(err)
				}
			}
			if err := x.s.LoginPolicyChanged(t.Context(), editor); !errors.Is(err, accessapi.ErrLoginPolicyEditor) {
				t.Fatalf("stale editor was not refused: %v", err)
			}
			ss, err := x.s.Session(t.Context(), peer.SessionID)
			if err != nil || !ss.Live(time.Now()) {
				t.Fatalf("refused edit ended a peer: %v", err)
			}
		})
	}
}
