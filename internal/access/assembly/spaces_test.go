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

// holder is a SpaceGuard whose rows name every space and whose stacks
// reach one entity each move would cut off.
type holder struct{}

func (holder) SpaceInUse(context.Context, kernel.AccountID, kernel.ULID) (bool, error) {
	return true, nil
}

func (holder) LostOnMove(context.Context, accessapi.SpaceMove) ([]accessapi.LostReach, error) {
	return []accessapi.LostReach{{Stack: "web", Entity: "context shared"}}, nil
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
	dev, err := d.spaces.Create(ctx, a.ID, "guarded", space.Space{Name: "Dev"})
	if err != nil {
		t.Fatal(err)
	}
	return d, db, a, dev
}

// TestAccGuardSpaces checks that a guard another domain registers refuses
// the deletes and moves of the domain's trees, which a new organization's
// first account holds.
func TestAccGuardSpaces(t *testing.T) {
	ctx := t.Context()
	d, _, a, dev := devSpace(t)
	d.GuardSpaces(holder{})
	for name, err := range map[string]error{
		"delete": d.spaces.Delete(ctx, a.ID, dev.Slug),
		"move":   d.spaces.Move(ctx, a.ID, dev.Slug, space.LegacySlug),
	} {
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
	// The member who claims the account is space-admin on its top space,
	// so reads every space under it.
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
	p := kernel.Principal{Kind: kernel.PrincipalUser, ActorID: m.ULID, MemberID: m, AccountID: a.ID}
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
