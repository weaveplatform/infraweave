package assembly

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/space"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
)

// squatter is a SpaceGuard whose rows name every space and whose stacks
// reach one entity each move would cut off.
type squatter struct{}

func (squatter) SpaceInUse(context.Context, kernel.AccountID, kernel.ULID) (bool, error) {
	return true, nil
}

func (squatter) LostOnMove(context.Context, accessapi.SpaceMove) ([]accessapi.LostReach, error) {
	return []accessapi.LostReach{{Stack: "web", Entity: "context shared"}}, nil
}

// everything is a space.Authorizer allowing every action.
type everything struct{}

func (everything) Authorize(
	context.Context,
	kernel.Principal,
	accessapi.Action,
	kernel.ULID,
) (bool, error) {
	return true, nil
}

func admin(acct kernel.AccountID) kernel.Principal {
	return kernel.Principal{Kind: kernel.PrincipalUser, ActorID: kernel.NewULID(), AccountID: acct}
}

// devSpace is a domain over a fresh database whose one organization's
// first account holds a space, Dev, under its top space.
func devSpace(t *testing.T) (*Domain, *sql.DB, account.Account, space.Space) {
	t.Helper()
	ctx := t.Context()
	db := sqltest.Open(t)
	d := mustNew(t, Deps{DB: db.Primary(), Settings: DefaultSettings()})
	o, err := organization.New("guarded", "Guarded")
	if err != nil {
		t.Fatal(err)
	}
	a, err := account.New(o.ID, "firstAccount.", "guarded", "Guarded")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.store.CreateOrganization(ctx, o, a, nil, kernel.OrganizationAuditEntry{
		OrganizationID:   o.ID,
		ActorKind:        kernel.PrincipalOperator,
		ActorID:          kernel.NewULID().String(),
		Action:           "organization.create",
		EventType:        "CREATE",
		AffectedResource: kernel.AuditResource{Type: "ORGANIZATION", ID: "guarded"},
	}); err != nil {
		t.Fatal(err)
	}
	// The account has no admin yet, so the tree is changed as the rule
	// would let an account admin, through an authorizer allowing it.
	d.spaces = space.NewService(space.Deps{
		Store: d.store, Authz: everything{}, Drop: func(kernel.AccountID) {}, Now: time.Now,
	})
	dev, err := d.spaces.Create(ctx, admin(a.ID), "guarded", space.Space{Name: "Dev"})
	if err != nil {
		t.Fatal(err)
	}
	return d, db, a, dev
}

// claimed is a member who claims a, so is space-admin on its top space
// and reads every space under it.
func claimed(t *testing.T, d *Domain, db *sql.DB, a account.Account) kernel.Principal {
	t.Helper()
	ctx := t.Context()
	m := kernel.NewMemberID()
	if _, err := db.Primary().Exec(ctx, `INSERT INTO members (organization_id, id, login, name,
		email, status) VALUES ($1, $2, 'alice', 'Alice', 'alice@example.com', 'active')`,
		a.OrganizationID, m); err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.ClaimAccount(ctx, a.OrganizationID, a.ID, m, time.Now(),
		kernel.OrganizationAuditEntry{
			OrganizationID:   a.OrganizationID,
			ActorKind:        kernel.PrincipalOperator,
			ActorID:          kernel.NewULID().String(),
			Action:           orgadmin.ActionAccountClaim,
			EventType:        "UPDATE",
			AffectedResource: kernel.AuditResource{Type: "ACCOUNT", ID: a.Slug.String()},
		}); err != nil {
		t.Fatal(err)
	}
	return kernel.Principal{
		Kind:      kernel.PrincipalUser,
		ActorID:   m.ULID,
		MemberID:  m,
		AccountID: a.ID,
	}
}

// TestAccGuardSpaces checks that a guard another domain registers refuses
// the deletes and moves of the domain's trees, which a new organization's
// first account holds.
func TestAccGuardSpaces(t *testing.T) {
	ctx := t.Context()
	d, _, a, dev := devSpace(t)
	d.GuardSpaces(squatter{})
	_, deleted := d.spaces.Delete(ctx, admin(a.ID), dev.Slug, kernel.Expect{})
	_, moved := d.spaces.Update(ctx, admin(a.ID), dev.Slug, space.LegacySlug, kernel.Expect{}, dev)
	for name, err := range map[string]error{"delete": deleted, "move": moved} {
		if _, ok := errors.AsType[*space.Refusal](err); !ok {
			t.Errorf("%s a guard refuses = %v", name, err)
		}
	}
}

// TestAccSubjects checks that the Requirer finds a space by the slug a
// request names, hiding one the account lacks, and an entity's space
// through the lookup another domain registers.
func TestAccSubjects(t *testing.T) {
	ctx := t.Context()
	d, db, a, dev := devSpace(t)
	d.ResolveSubjects(
		"stack",
		func(_ context.Context, _ kernel.AccountID, id string) (kernel.ULID, error) {
			if id == "web" {
				return dev.ID, nil
			}
			return kernel.ULID{}, accessapi.ErrNoSubject
		},
	)
	p := claimed(t, d, db, a)
	for _, c := range []struct {
		subject accessapi.Subject
		id      string
		code    int
	}{
		{accessapi.Subject{Kind: accessapi.SubjectPath, Param: "id", Entity: accessapi.EntitySpace}, dev.Slug, http.StatusOK},
		{accessapi.Subject{Kind: accessapi.SubjectPath, Param: "id", Entity: accessapi.EntitySpace}, "nowhere", http.StatusNotFound},
		{accessapi.Subject{Kind: accessapi.SubjectPath, Param: "id", Entity: "stack"}, "web", http.StatusOK},
		{accessapi.Subject{Kind: accessapi.SubjectPath, Param: "id", Entity: "stack"}, "gone", http.StatusNotFound},
		{accessapi.Subject{Kind: accessapi.SubjectPath, Param: "id", Entity: "context"}, "shared", http.StatusNotImplemented},
	} {
		h := d.Requirer().Middleware(func(*http.Request) (accessapi.Required, bool) {
			return accessapi.Required{
				Action:  accessapi.ActionStackLock,
				Subject: c.subject,
				Params:  map[string]string{"id": c.id},
			}, true
		})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(kernel.WithPrincipal(ctx, p),
			http.MethodGet, "/", nil))
		if rec.Code != c.code ||
			!strings.Contains(rec.Body.String(), c.id) && c.code == http.StatusNotFound {
			t.Errorf("%s %q: %d %s, want %d", c.subject.Entity, c.id, rec.Code, rec.Body, c.code)
		}
	}
}
