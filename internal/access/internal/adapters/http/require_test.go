package http

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// grantsOf is an accessapi.Authorizer allowing the actions it lists on
// each space, and anywhere for Holds, answering them as each space's
// Actions and the spaces that list one as Reachable; err fails every
// decision.
type grantsOf struct {
	on  map[kernel.ULID][]accessapi.Action
	err error
}

func (g grantsOf) Authorize(
	_ context.Context,
	_ kernel.Principal,
	a accessapi.Action,
	space kernel.ULID,
) (bool, error) {
	for _, held := range g.on[space] {
		if held == a {
			return true, g.err
		}
	}
	return false, g.err
}

func (g grantsOf) Reachable(
	_ context.Context,
	_ kernel.Principal,
	a accessapi.Action,
) ([]kernel.ULID, error) {
	var out []kernel.ULID
	for space, held := range g.on {
		if slices.Contains(held, a) {
			out = append(out, space)
		}
	}
	return out, g.err
}

func (g grantsOf) Holds(ctx context.Context, p kernel.Principal, a accessapi.Action) (bool, error) {
	for space := range g.on {
		if ok, _ := g.Authorize(ctx, p, a, space); ok {
			return true, g.err
		}
	}
	return false, g.err
}

func (g grantsOf) Actions(
	_ context.Context,
	_ kernel.Principal,
	space kernel.ULID,
) ([]accessapi.Action, error) {
	return g.on[space], g.err
}

// TestUnitRequire checks each subject's decision: 404 with one body for
// a missing entity and one the caller cannot read, 403 naming the
// action, 501 for an entity no domain registers, and the store's and
// the authorizer's failures.
func TestUnitRequire(t *testing.T) {
	acct := kernel.NewAccountID()
	top, team, secret := acct.ULID, kernel.NewULID(), kernel.NewULID()
	p := kernel.Principal{Kind: kernel.PrincipalAPIKey, ActorID: kernel.NewULID(), AccountID: acct}
	read := accessapi.ActionSpaceRead
	authz := grantsOf{on: map[kernel.ULID][]accessapi.Action{
		top:  {read, accessapi.ActionApiKeyCreate},
		team: {read, accessapi.ActionStackLock, accessapi.ActionStackCreate},
	}}
	subjects := NewSubjects()
	var lookupErr error
	subjects.Register(
		"stack",
		func(_ context.Context, a kernel.AccountID, id string) (kernel.ULID, error) {
			if a != acct {
				t.Errorf("lookup in account %s", a)
			}
			switch {
			case lookupErr != nil:
				return kernel.ULID{}, lookupErr
			case id == "mine":
				return team, nil
			case id == "theirs":
				return secret, nil
			}
			return kernel.ULID{}, accessapi.ErrNoSubject
		},
	)
	subjects.Register(
		accessapi.EntitySpace,
		func(_ context.Context, _ kernel.AccountID, id string) (kernel.ULID, error) {
			if id == "team" {
				return team, nil
			}
			return kernel.ULID{}, accessapi.ErrNoSubject
		},
	)
	stack := func(param string) accessapi.Subject {
		return accessapi.Subject{Kind: accessapi.SubjectPath, Param: param, Entity: "stack"}
	}
	body := accessapi.Subject{
		Kind:   accessapi.SubjectBody,
		Field:  "input.space",
		Entity: accessapi.EntitySpace,
	}
	query := accessapi.Subject{
		Kind:   accessapi.SubjectQuery,
		Param:  "space",
		Entity: accessapi.EntitySpace,
	}
	for _, c := range []struct {
		name   string
		authz  accessapi.Authorizer
		req    accessapi.Required
		target string
		body   string
		lookup error
		code   int
		typ    problem.Type
		detail string
	}{
		{name: "account", req: accessapi.Required{
			Action: accessapi.ActionApiKeyCreate, Subject: accessapi.Subject{Kind: accessapi.SubjectAccount},
		}, code: http.StatusOK},
		{name: "account denied", req: accessapi.Required{
			Action: accessapi.ActionSessionDelete, Subject: accessapi.Subject{Kind: accessapi.SubjectAccount},
		}, code: http.StatusForbidden, typ: problem.Forbidden},
		{name: "any", req: accessapi.Required{
			Action: accessapi.ActionStackLock, Subject: accessapi.Subject{Kind: accessapi.SubjectAny},
		}, code: http.StatusOK},
		{name: "any denied", req: accessapi.Required{
			Action: accessapi.ActionStackDelete, Subject: accessapi.Subject{Kind: accessapi.SubjectAny},
		}, code: http.StatusForbidden, typ: problem.Forbidden},
		{name: "organization", req: accessapi.Required{
			Action: accessapi.ActionAuthenticated, Subject: accessapi.Subject{Kind: accessapi.SubjectOrganization},
		}, code: http.StatusOK},
		{name: "list", req: accessapi.Required{
			Action: read, Subject: accessapi.Subject{Kind: accessapi.SubjectList},
		}, code: http.StatusOK},
		{name: "path", req: accessapi.Required{
			Action: accessapi.ActionStackLock, Subject: stack("stackId"),
			Params: map[string]string{"stackId": "mine"},
		}, code: http.StatusOK},
		{name: "path denied", req: accessapi.Required{
			Action: accessapi.ActionStackDelete, Subject: stack("stackId"),
			Params: map[string]string{"stackId": "mine"},
		}, code: http.StatusForbidden, typ: problem.Forbidden},
		{name: "path hidden", req: accessapi.Required{
			Action: accessapi.ActionStackLock, Subject: stack("stackId"),
			Params: map[string]string{"stackId": "theirs"},
		}, code: http.StatusNotFound, typ: problem.NotFound, detail: `No stack "theirs" in this account.`},
		{name: "path missing", req: accessapi.Required{
			Action: accessapi.ActionStackLock, Subject: stack("stackId"),
			Params: map[string]string{"stackId": "nothing"},
		}, code: http.StatusNotFound, typ: problem.NotFound, detail: `No stack "nothing" in this account.`},
		{name: "path down", lookup: kernel.ErrUnavailable, req: accessapi.Required{
			Action: accessapi.ActionStackLock, Subject: stack("stackId"),
			Params: map[string]string{"stackId": "mine"},
		}, code: http.StatusServiceUnavailable, typ: problem.ServiceUnavailable},
		{name: "unregistered", req: accessapi.Required{
			Action: accessapi.ActionStackLock,
			Subject: accessapi.Subject{
				Kind: accessapi.SubjectPath, Param: "id", Entity: "stack_dependency_reference",
			},
		}, code: http.StatusNotImplemented, typ: problem.NotImplemented},
		{name: "body", body: `{"input":{"space":"team"}}`, req: accessapi.Required{
			Action: accessapi.ActionStackCreate, Subject: body,
		}, code: http.StatusOK},
		{
			name: "body without the field decides on the top space", body: `{"input":{}}`,
			req:  accessapi.Required{Action: accessapi.ActionStackCreate, Subject: body},
			code: http.StatusForbidden, typ: problem.Forbidden,
		},
		{
			name: "body not JSON", body: `{`,
			req:  accessapi.Required{Action: accessapi.ActionStackCreate, Subject: body},
			code: http.StatusForbidden, typ: problem.Forbidden,
		},
		{
			name: "body too large", body: `{"input":{"space":"team"},"pad":"` + strings.Repeat("x", bodyPeek) + `"}`,
			req:  accessapi.Required{Action: accessapi.ActionStackCreate, Subject: body},
			code: http.StatusForbidden, typ: problem.Forbidden,
		},
		{
			name: "body field not an object", body: `{"input":"team"}`,
			req:  accessapi.Required{Action: accessapi.ActionStackCreate, Subject: body},
			code: http.StatusForbidden, typ: problem.Forbidden,
		},
		{
			name: "body hidden", body: `{"input":{"space":"elsewhere"}}`,
			req:  accessapi.Required{Action: accessapi.ActionStackCreate, Subject: body},
			code: http.StatusNotFound, typ: problem.NotFound, detail: `No space "elsewhere" in this account.`,
		},
		{
			name: "query", target: "/?space=team",
			req:  accessapi.Required{Action: accessapi.ActionStackCreate, Subject: query},
			code: http.StatusOK,
		},
		{
			name: "query without the parameter decides on the top space", target: "/",
			req:  accessapi.Required{Action: accessapi.ActionStackCreate, Subject: query},
			code: http.StatusForbidden, typ: problem.Forbidden,
		},
		{
			name: "query hidden", target: "/?space=elsewhere",
			req:  accessapi.Required{Action: accessapi.ActionStackCreate, Subject: query},
			code: http.StatusNotFound, typ: problem.NotFound, detail: `No space "elsewhere" in this account.`,
		},
		{name: "authorizer down", authz: grantsOf{err: kernel.ErrUnavailable}, req: accessapi.Required{
			Action: accessapi.ActionStackLock, Subject: stack("stackId"),
			Params: map[string]string{"stackId": "mine"},
		}, code: http.StatusServiceUnavailable, typ: problem.ServiceUnavailable},
		{name: "authorizer fails", authz: grantsOf{err: errors.New("boom")}, req: accessapi.Required{
			Action: accessapi.ActionApiKeyCreate, Subject: accessapi.Subject{Kind: accessapi.SubjectAccount},
		}, code: http.StatusInternalServerError, typ: problem.InternalError},
	} {
		t.Run(c.name, func(t *testing.T) {
			lookupErr = c.lookup
			a := c.authz
			if a == nil {
				a = authz
			}
			var seen string
			h := NewRequire(a, subjects).Middleware(func(*http.Request) (accessapi.Required, bool) {
				return c.req, true
			})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				seen = string(b)
				w.WriteHeader(http.StatusOK)
			}))
			target := c.target
			if target == "" {
				target = "/"
			}
			req := httptest.NewRequestWithContext(kernel.WithPrincipal(t.Context(), p),
				http.MethodPost, target, strings.NewReader(c.body))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.code {
				t.Fatalf("%d %s, want %d", rec.Code, rec.Body, c.code)
			}
			if c.code == http.StatusOK {
				if seen != c.body {
					t.Errorf("the handler read %q, want %q", seen, c.body)
				}
				return
			}
			got := decode[problem.Problem](t, rec)
			if got.Type != c.typ.New("").Type || c.detail != "" && got.Detail != c.detail {
				t.Errorf("%+v, want %s %q", got, c.typ.New("").Type, c.detail)
			}
			if (c.typ == problem.Forbidden) != (got.Action == string(c.req.Action)) {
				t.Errorf("action %q", got.Action)
			}
		})
	}
}

// TestUnitRequirePasses checks that a route no operation names, and a
// request without a principal, reach the handler undecided: the route
// table refuses the first and authentication the second.
func TestUnitRequirePasses(t *testing.T) {
	for name, c := range map[string]struct {
		ok  bool
		ctx context.Context
	}{
		"no operation": {ctx: kernel.WithPrincipal(context.Background(), kernel.Principal{})},
		"no principal": {ok: true, ctx: context.Background()},
	} {
		h := NewRequire(grantsOf{err: errors.New("decided")}, NewSubjects()).Middleware(
			func(*http.Request) (accessapi.Required, bool) {
				return accessapi.Required{
					Action:  accessapi.ActionStackLock,
					Subject: accessapi.Subject{Kind: accessapi.SubjectAny},
				}, c.ok
			})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(c.ctx, http.MethodGet, "/", nil))
		if rec.Code != http.StatusTeapot {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}

// TestUnitBodyFieldNoBody checks that a request without a body decides on
// the top space.
func TestUnitBodyFieldNoBody(t *testing.T) {
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
	r.Body = nil
	if got := bodyField(r, "input.space"); got != "" {
		t.Errorf("bodyField = %q", got)
	}
}
