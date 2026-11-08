package assembly

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authz"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/role"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/test/acceptance"
)

// rows is one account's authorization rows, which a test changes behind
// the service's back, as another replica's write does.
type rows struct {
	mu   sync.Mutex
	d    authz.Data
	down error
}

func (r *rows) AuthzData(context.Context, kernel.AccountID) (authz.Data, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.d, nil
}

func (r *rows) AuthzVersions(
	context.Context,
	[]kernel.AccountID,
) (map[kernel.AccountID]int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.down != nil {
		return nil, r.down
	}
	return map[kernel.AccountID]int64{r.d.Account: r.d.Version}, nil
}

// TestUnitWatchAuthorization runs the watcher in virtual time: a binding
// whose message is lost is decided within refreshEvery, a message decides
// it at once, a Resync drops every snapshot, and a failed comparison is
// logged.
func TestUnitWatchAuthorization(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		acct, m, admin := kernel.NewAccountID(), kernel.NewMemberID(), kernel.NewULID()
		r := &rows{d: authz.Data{
			Account: acct,
			Spaces:  []authz.Space{{ID: acct.ULID}},
			Roles:   []authz.Role{{ID: admin, Slug: role.SpaceAdmin, System: true}},
		}}
		svc := authz.NewService(r)
		msgs := make(chan notify.Message)
		var logged bytes.Buffer
		var wg sync.WaitGroup
		wg.Go(func() {
			watchAuthorization(ctx, svc, msgs, slog.New(slog.NewTextHandler(&logged, nil)))
		})
		defer func() {
			cancel()
			wg.Wait()
		}()
		p := kernel.Principal{
			Kind:      kernel.PrincipalUser,
			AccountID: acct,
			ActorID:   m.ULID,
			MemberID:  m,
		}
		create := func() bool {
			t.Helper()
			ok, err := svc.Authorize(ctx, p, accessapi.ActionStackCreate, acct.ULID)
			if err != nil {
				t.Fatal(err)
			}
			return ok
		}
		write := func(bind bool) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.d.Bindings = nil
			if bind {
				r.d.Bindings = []role.Binding{
					{
						AccountID: acct,
						ID:        kernel.NewULID(),
						ActorKind: role.ActorUser,
						ActorID:   m.ULID,
						RoleID:    admin,
						SpaceID:   acct.ULID,
					},
				}
			}
			r.d.Version++
		}

		if create() {
			t.Fatal("an unbound member creates")
		}
		// The binding's message is lost.
		write(true)
		<-time.After(refreshEvery - time.Millisecond)
		synctest.Wait()
		if create() {
			t.Fatal("decided before any comparison")
		}
		<-time.After(time.Millisecond)
		synctest.Wait()
		if !create() {
			t.Fatal("a lost message's binding is undecided after a comparison")
		}

		write(false)
		msgs <- notify.Message{Payload: authz.Key(acct)}
		synctest.Wait()
		if create() {
			t.Error("a message left the unbinding undecided")
		}
		msgs <- notify.Message{Payload: "session:" + kernel.NewULID().String()}
		write(true)
		msgs <- notify.Message{Resync: true}
		synctest.Wait()
		if !create() {
			t.Error("a Resync kept a snapshot")
		}

		r.mu.Lock()
		r.down = errors.New("database down")
		r.mu.Unlock()
		<-time.After(refreshEvery)
		synctest.Wait()
		if !strings.Contains(logged.String(), "database down") {
			t.Errorf("a failed comparison logged %q", logged.String())
		}
	})
}

// TestAccWatchAuthorization claims an account on one replica and checks
// that another, whose snapshot of it is held, decides the claim's
// binding once its message arrives, through the Authorizer and the
// principal's Admin.
func TestAccWatchAuthorization(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
	}()
	db := sqltest.Open(t)
	deps := Deps{DB: db.Primary(), Settings: DefaultSettings(), Audience: "http://iw.test/api/v1"}
	a, b := mustNew(t, deps), mustNew(t, deps)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	l := notify.NewListener(db.ConnConfig, log)
	sub, _ := l.Subscribe(notify.Invalidate)
	wg.Go(func() { l.Run(ctx) })
	if m := <-sub.C; !m.Resync {
		t.Fatalf("first message %+v, want the connect's Resync", m)
	}
	wg.Go(func() { b.WatchAuthorization(ctx, sub, log) })

	o, err := organization.New("claimed", "Claimed")
	if err != nil {
		t.Fatal(err)
	}
	acct, err := account.New(o.ID, "", "claimed", "Claimed")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.store.CreateOrganization(ctx, o, acct, nil, kernel.OrganizationAuditEntry{
		OrganizationID:   o.ID,
		ActorKind:        kernel.PrincipalOperator,
		ActorID:          kernel.NewULID().String(),
		Action:           "organization.create",
		EventType:        "CREATE",
		AffectedResource: kernel.AuditResource{Type: "ORGANIZATION", ID: "claimed"},
	}); err != nil {
		t.Fatal(err)
	}
	m := kernel.NewMemberID()
	if _, err := db.Primary().Exec(ctx, `INSERT INTO members (organization_id, id, login, name,
		email, status) VALUES ($1, $2, 'alice', 'Alice', 'alice@example.com', 'active')`,
		o.ID, m); err != nil {
		t.Fatal(err)
	}
	p := kernel.Principal{
		Kind:      kernel.PrincipalUser,
		AccountID: acct.ID,
		ActorID:   m.ULID,
		MemberID:  m,
	}
	decide := func(ctx context.Context) (bool, error) {
		return b.Authorizer().Authorize(ctx, p, accessapi.ActionStackCreate, acct.ID.ULID)
	}
	if ok, err := decide(ctx); err != nil || ok {
		t.Fatalf("before the claim = %t, %v", ok, err)
	}

	if _, err := a.store.ClaimAccount(
		ctx,
		o.ID,
		acct.ID,
		m,
		time.Now(),
		kernel.OrganizationAuditEntry{
			OrganizationID:   o.ID,
			ActorKind:        kernel.PrincipalOperator,
			ActorID:          kernel.NewULID().String(),
			Action:           orgadmin.ActionAccountClaim,
			EventType:        "UPDATE",
			AffectedResource: kernel.AuditResource{Type: "ACCOUNT", ID: "claimed"},
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := acceptance.Eventually(ctx, "replica B to decide the claim", decide); err != nil {
		t.Fatal(err)
	}
	if got, err := b.Authorizer().Reachable(ctx, p, accessapi.ActionStackCreate); err != nil ||
		len(got) != 2 {
		t.Errorf("Reachable = %v, %v", got, err)
	}
	if ok, err := b.authz.Admin(ctx, p); err != nil || !ok {
		t.Errorf("Admin = %t, %v", ok, err)
	}
}
