package http

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authn"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/breakglass"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/operator"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/space"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	opapi "github.com/weaveplatform/infraweave/internal/platform/gen/operator"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

const operatorAudience = "http://infraweave.test/operator/v1"

// orgs is operator.Store in memory; down fails every call.
type orgs struct {
	byID    map[kernel.OrganizationID]operator.Organization
	entries []kernel.OrganizationAuditEntry
	down    bool
}

func (o *orgs) err() error {
	if o.down {
		return errDown
	}
	return nil
}

func (o *orgs) CreateOrganization(
	_ context.Context,
	org organization.Organization,
	first account.Account,
	bg *breakglass.Credential,
	e kernel.OrganizationAuditEntry,
) error {
	if err := o.err(); err != nil {
		return err
	}
	for _, x := range o.byID {
		if x.Slug == org.Slug {
			return fmt.Errorf("%w: organization slug %q is in use", port.ErrSlugTaken, org.Slug)
		}
	}
	o.byID[org.ID] = operator.Organization{
		Organization: org, Accounts: []account.Account{first}, BreakGlass: bg,
	}
	o.entries = append(o.entries, e)
	return nil
}

func (o *orgs) Organizations(
	_ context.Context,
	after kernel.OrganizationID,
	limit int,
) ([]operator.Organization, error) {
	var out []operator.Organization
	for _, x := range o.byID {
		if after.IsZero() || x.ID.String() > after.String() {
			out = append(out, x)
		}
	}
	slices.SortFunc(out, func(a, b operator.Organization) int {
		return strings.Compare(a.ID.String(), b.ID.String())
	})
	return out[:min(len(out), limit)], o.err()
}

func (o *orgs) OrganizationBySlug(
	_ context.Context,
	slug kernel.Slug,
) (operator.Organization, error) {
	if err := o.err(); err != nil {
		return operator.Organization{}, err
	}
	for _, x := range o.byID {
		if x.Slug == slug {
			return x, nil
		}
	}
	return operator.Organization{}, port.ErrNotFound
}

func (o *orgs) DeleteOrganization(
	_ context.Context,
	id kernel.OrganizationID,
	e kernel.OrganizationAuditEntry,
) ([]kernel.ULID, error) {
	delete(o.byID, id)
	o.entries = append(o.entries, e)
	return nil, nil
}

func (o *orgs) SetBreakGlass(
	_ context.Context,
	id kernel.OrganizationID,
	c breakglass.Credential,
	e kernel.OrganizationAuditEntry,
) error {
	x := o.byID[id]
	x.BreakGlass = &c
	o.byID[id] = x
	o.entries = append(o.entries, e)
	return nil
}

func (o *orgs) DeleteBreakGlass(
	_ context.Context,
	id kernel.OrganizationID,
	e kernel.OrganizationAuditEntry,
) error {
	x := o.byID[id]
	x.BreakGlass = nil
	o.byID[id] = x
	o.entries = append(o.entries, e)
	return nil
}

// operatorStubs is the operator partial with the 501 stubs below it.
type operatorFull struct {
	OperatorServer
	operatorStubs
}

type operatorStubs struct{ opapi.UnimplementedStrict }

// opWorld is the API and the operator API over one authn service that
// knows one API key and one operator key, each under its own prefix as
// internal/app mounts them.
type opWorld struct {
	*world
	orgs           *orgs
	h              http.Handler
	opKey          kernel.ULID
	opSecret, hash string
}

func newOpWorld(t *testing.T) *opWorld {
	t.Helper()
	base := newWorld(t)
	w := &opWorld{
		world:    base,
		orgs:     &orgs{byID: map[kernel.OrganizationID]operator.Organization{}},
		opKey:    kernel.NewULID(),
		opSecret: apikey.NewSecret(),
	}
	h, err := breakglass.New("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	w.hash = h.String()
	auth := authn.New(authn.Deps{
		Keys:     base.st,
		Sessions: base.st,
		Tokens:   base.st,
		Audience: audience,
		TTL:      10 * time.Hour,
		Operators: map[kernel.ULID][sha256.Size]byte{
			w.opKey: sha256.Sum256([]byte(w.opSecret)),
		},
		OperatorAudience: operatorAudience,
		Now:              func() time.Time { return t0 },
	})
	keys := apikey.NewService(apikey.Deps{
		Store: base.st, Authz: base.authz, Drop: auth.Cache().Drop,
		Now: func() time.Time { return t0 },
	})
	svc := operator.NewService(operator.Deps{
		Store: w.orgs, Drop: auth.Cache().Drop, Now: func() time.Time { return t0 },
	})
	opts := api.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  problem.RequestError,
		ResponseErrorHandlerFunc: problem.ResponseError,
	}
	isPublic := func(r *http.Request) accessapi.Access {
		if r.URL.Path == "/api"+public {
			return accessapi.Public
		}
		return accessapi.Account
	}
	mux := http.NewServeMux()
	mux.Handle(
		"/api/",
		NewAuthenticator(auth, publicURL).Middleware(isPublic)(api.HandlerWithOptions(
			api.NewStrictHandlerWithOptions(
				full{Server: NewServer(Services{
					Auth: auth, Keys: keys, Authz: base.authz, Accounts: base.st,
					Spaces: space.NewService(base.tree, base.authz, func() time.Time { return t0 }),
				})},
				nil,
				opts,
			),
			api.ChiServerOptions{BaseURL: "/api"},
		)),
	)
	mux.Handle(
		"/operator/",
		NewOperatorAuthenticator(auth).Middleware(isPublic)(opapi.HandlerWithOptions(
			opapi.NewStrictHandlerWithOptions(
				operatorFull{OperatorServer: NewOperatorServer(svc)},
				nil,
				opapi.StrictHTTPServerOptions(opts),
			),
			opapi.ChiServerOptions{BaseURL: "/operator"},
		)),
	)
	w.h = mux
	base.h = mux
	return w
}

// operatorToken exchanges the operator key at the API's /auth/token.
func (w *opWorld) operatorToken(t *testing.T) string {
	t.Helper()
	rec := w.do(t, http.MethodPost, "/api"+public, "",
		fmt.Sprintf(`{"id":%q,"secret":%q}`, w.opKey, w.opSecret))
	if rec.Code != http.StatusOK {
		t.Fatalf("operator exchange: %d %s", rec.Code, rec.Body)
	}
	return "Bearer " + decode[api.User](t, rec).Jwt
}

func (w *opWorld) apiToken(t *testing.T) string {
	t.Helper()
	rec := w.do(t, http.MethodPost, "/api"+public, "",
		fmt.Sprintf(`{"id":%q,"secret":%q}`, w.key, w.secret))
	if rec.Code != http.StatusOK {
		t.Fatalf("exchange: %d %s", rec.Code, rec.Body)
	}
	return "Bearer " + decode[api.User](t, rec).Jwt
}

func (w *opWorld) create(t *testing.T, bearer, slug string) *httptest.ResponseRecorder {
	t.Helper()
	return w.do(t, http.MethodPost, "/operator/organizations", bearer, fmt.Sprintf(
		`{"slug":%q,"name":"Acme","account":{"slug":"main","name":"Main"},`+
			`"breakGlass":{"login":"admin","passwordHash":%q}}`, slug, w.hash))
}

func wantProblem(
	t *testing.T,
	rec *httptest.ResponseRecorder,
	code int,
	slug string,
) problem.Problem {
	t.Helper()
	p := decode[problem.Problem](t, rec)
	if rec.Code != code || !strings.HasSuffix(p.Type, ":"+slug) {
		t.Errorf("%d %+v, want %d %s", rec.Code, p, code, slug)
	}
	return p
}

func TestUnitOperatorOrganizations(t *testing.T) {
	w := newOpWorld(t)
	op := w.operatorToken(t)

	rec := w.create(t, op, "acme")
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	o := decode[opapi.Organization](t, rec)
	if o.Slug != "acme" || len(o.Accounts) != 1 || o.Accounts[0].Claimed || o.BreakGlass == nil ||
		o.BreakGlass.Login != "admin" || o.CreatedAt != t0.Unix() {
		t.Errorf("created = %+v", o)
	}
	if strings.Contains(rec.Body.String(), "pbkdf2") {
		t.Errorf("the answer carries the hash: %s", rec.Body)
	}
	e := w.orgs.entries[0]
	if e.ActorKind != kernel.PrincipalOperator || e.ActorID != w.opKey.String() ||
		e.RemoteIP != "192.0.2.9" {
		t.Errorf("entry = %+v", e)
	}
	wantProblem(t, w.create(t, op, "acme"), http.StatusConflict, "already-exists")
	if rec := w.create(t, op, "beta"); rec.Code != http.StatusCreated {
		t.Fatalf("create beta: %d %s", rec.Code, rec.Body)
	}

	page := decode[opapi.OrganizationPage](
		t,
		w.do(t, http.MethodGet, "/operator/organizations?limit=1", op, ""),
	)
	if len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatalf("first page = %+v", page)
	}
	rest := decode[opapi.OrganizationPage](t,
		w.do(t, http.MethodGet, "/operator/organizations?cursor="+*page.NextCursor, op, ""))
	if len(rest.Items) != 1 || rest.NextCursor != nil || rest.Items[0].Id == page.Items[0].Id {
		t.Errorf("second page = %+v", rest)
	}
	wantProblem(t, w.do(t, http.MethodGet, "/operator/organizations?cursor=nope", op, ""),
		http.StatusBadRequest, "invalid-cursor")

	if got := decode[opapi.Organization](
		t,
		w.do(t, http.MethodGet, "/operator/organizations/acme", op, ""),
	); got.Id != o.Id {
		t.Errorf("read = %+v", got)
	}
	wantProblem(t, w.do(t, http.MethodGet, "/operator/organizations/nobody", op, ""),
		http.StatusNotFound, "not-found")

	rec = w.do(t, http.MethodDelete, "/operator/organizations/acme/break-glass", op, "")
	if got := decode[opapi.Organization](
		t,
		rec,
	); rec.Code != http.StatusOK ||
		got.BreakGlass != nil {
		t.Errorf("break-glass delete: %d %s", rec.Code, rec.Body)
	}
	rec = w.do(t, http.MethodPut, "/operator/organizations/acme/break-glass", op,
		fmt.Sprintf(`{"login":"root-admin","passwordHash":%q}`, w.hash))
	if got := decode[opapi.Organization](t, rec); rec.Code != http.StatusOK ||
		got.BreakGlass == nil || got.BreakGlass.Login != "root-admin" {
		t.Errorf("break-glass set: %d %s", rec.Code, rec.Body)
	}
	rec = w.do(t, http.MethodPut, "/operator/organizations/acme/break-glass", op,
		`{"login":"root-admin","passwordHash":"$pbkdf2-sha256$i=1000$AAAAAAAAAAAAAAAAAAAAAA$AAAA"}`)
	if p := wantProblem(
		t,
		rec,
		http.StatusUnprocessableEntity,
		"validation-failed",
	); !strings.Contains(
		p.Detail,
		"passwordHash:",
	) {
		t.Errorf("detail %q does not name the field", p.Detail)
	}
	wantProblem(t, w.do(t, http.MethodPut, "/operator/organizations/nobody/break-glass", op,
		fmt.Sprintf(`{"login":"a","passwordHash":%q}`, w.hash)), http.StatusNotFound, "not-found")
	wantProblem(t, w.do(t, http.MethodDelete, "/operator/organizations/nobody/break-glass", op, ""),
		http.StatusNotFound, "not-found")

	rec = w.do(t, http.MethodDelete, "/operator/organizations/acme", op, "")
	if got := decode[opapi.Organization](t, rec); rec.Code != http.StatusOK || got.Id != o.Id {
		t.Errorf("delete: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, w.do(t, http.MethodDelete, "/operator/organizations/acme", op, ""),
		http.StatusNotFound, "not-found")

	w.orgs.down = true
	for _, target := range []string{"/operator/organizations", "/operator/organizations/beta"} {
		if rec := w.do(
			t,
			http.MethodGet,
			target,
			op,
			"",
		); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s with the store down: %d %s", target, rec.Code, rec.Body)
		}
	}
}

func TestUnitOperatorCreateInvalid(t *testing.T) {
	w := newOpWorld(t)
	rec := w.do(t, http.MethodPost, "/operator/organizations", w.operatorToken(t),
		`{"slug":"Acme","name":"","account":{"slug":"main","name":"Main"},`+
			`"breakGlass":{"login":"admin","passwordHash":"$argon2id$x"}}`)
	p := wantProblem(t, rec, http.StatusUnprocessableEntity, "validation-failed")
	for _, field := range []string{"slug:", "name:", "breakGlass.passwordHash:"} {
		if !strings.Contains(p.Detail, field) {
			t.Errorf("detail %q does not name %s", p.Detail, field)
		}
	}
	if rec := w.do(
		t,
		http.MethodPost,
		"/operator/organizations",
		w.operatorToken(t),
		`{"slug":"bare","name":"Bare","account":{"slug":"main","name":"Main"}}`,
	); rec.Code != http.StatusCreated ||
		strings.Contains(rec.Body.String(), "breakGlass") {
		t.Errorf("create without break glass: %d %s", rec.Code, rec.Body)
	}
}

// TestUnitOperatorTokens holds each API to its own kind of token.
func TestUnitOperatorTokens(t *testing.T) {
	w := newOpWorld(t)
	op, user := w.operatorToken(t), w.apiToken(t)
	wantUnauthenticated(t, w.do(t, http.MethodGet, "/operator/organizations", user, ""), true)
	wantUnauthenticated(t, w.do(t, http.MethodGet, "/api/viewer", op, ""), true)
	wantUnauthenticated(t, w.do(t, http.MethodGet, "/operator/organizations", "", ""), true)
	if rec := w.do(
		t,
		http.MethodGet,
		"/operator/organizations",
		op,
		"",
	); rec.Code != http.StatusOK {
		t.Errorf("operator token on the operator API: %d %s", rec.Code, rec.Body)
	}
	var ss session.Session
	for _, s := range w.st.sessions {
		if s.ActorKind == kernel.PrincipalOperator {
			ss = s
		}
	}
	if ss.ActorID != w.opKey || !ss.AccountID.IsZero() || !ss.OrganizationID.IsZero() {
		t.Errorf("operator session = %+v", ss)
	}
	if rec := w.do(
		t,
		http.MethodPost,
		"/api"+public,
		"",
		fmt.Sprintf(
			`{"id":%q,"secret":%q}`,
			w.opKey,
			apikey.NewSecret(),
		),
	); rec.Code != http.StatusUnauthorized {
		t.Errorf("operator key, wrong secret: %d %s", rec.Code, rec.Body)
	}
}
