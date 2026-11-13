package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authn"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// subjects is the store's tokens, which also keep their subject: a token
// is "<kind> <subject> <audience>".
type subjects struct{ *store }

func (subjects) Issue(_ context.Context, r token.Request) (string, token.Claims, error) {
	return string(r.Kind) + " " + r.Subject + " " + r.Audience,
		token.Claims{Kind: r.Kind, Subject: r.Subject, Expiry: t0.Add(r.TTL)}, nil
}

func (subjects) Verify(_ context.Context, raw, aud string) (token.Claims, error) {
	parts := strings.Split(raw, " ")
	if len(parts) != 3 || parts[2] != aud {
		return token.Claims{}, token.ErrToken
	}
	return token.Claims{Kind: token.Kind(parts[0]), Subject: parts[1], Expiry: t0.Add(time.Hour)},
		nil
}

// TestUnitWorkerAuthenticator checks that the worker API's authenticator
// admits a worker session token on a worker route and a run token on a
// runner route, putting the worker's or the run's principal and bearer on
// the context, and refuses each on the other's routes and an API token on
// both; and that neither authenticator admits a credential on the other's
// routes.
func TestUnitWorkerAuthenticator(t *testing.T) {
	base := newWorld(t)
	auth := authn.New(authn.Deps{
		Keys: base.st, Sessions: base.st, Tokens: subjects{base.st}, Audience: audience,
		TTL: 10 * time.Hour, WorkerTTL: time.Hour, RunTTL: time.Hour,
		Now: func() time.Time { return t0 },
	})
	worker, run := kernel.NewULID(), kernel.NewULID()
	tok, err := auth.WorkerToken(t.Context(), worker)
	if err != nil {
		t.Fatal(err)
	}
	runTok, err := auth.RunToken(t.Context(), run)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		p kernel.Principal
		b accessapi.Bearer
	}
	seen := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.p, _ = kernel.PrincipalFrom(r.Context())
		got.b, _ = accessapi.BearerFrom(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	send := func(a Authenticator, acc accessapi.Access, bearer string) int {
		got.p, got.b = kernel.Principal{}, accessapi.Bearer{}
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		a.Middleware(func(*http.Request) accessapi.Access { return acc })(seen).ServeHTTP(rec, req)
		return rec.Code
	}
	workers := NewWorkerAuthenticator(auth)
	if code := send(workers, accessapi.Worker, tok.Raw); code != http.StatusNoContent ||
		got.p.Kind != kernel.PrincipalWorker || got.p.ActorID != worker ||
		got.b.ID != worker || !got.b.Expires.Equal(tok.Expires) {
		t.Errorf("worker token = %d, %+v, %+v", code, got.p, got.b)
	}
	if code := send(workers, accessapi.Runner, runTok.Raw); code != http.StatusNoContent ||
		got.p.Kind != kernel.PrincipalRunner || got.p.ActorID != run ||
		got.b.ID != run || !got.b.Expires.Equal(runTok.Expires) {
		t.Errorf("run token = %d, %+v, %+v", code, got.p, got.b)
	}
	if code := send(workers, accessapi.Public, ""); code != http.StatusNoContent {
		t.Errorf("public route = %d", code)
	}
	for name, tc := range map[string]struct {
		a      Authenticator
		acc    accessapi.Access
		bearer string
	}{
		"no token":                       {workers, accessapi.Worker, ""},
		"garbage":                        {workers, accessapi.Worker, "garbage"},
		"an API route":                   {workers, accessapi.Account, tok.Raw},
		"a worker token on the API":      {NewAuthenticator(auth, publicURL), accessapi.Account, tok.Raw},
		"the API's on a worker route":    {NewAuthenticator(auth, publicURL), accessapi.Worker, tok.Raw},
		"the operator's on a worker one": {NewOperatorAuthenticator(auth), accessapi.Worker, tok.Raw},
		"a run token on a worker route":  {workers, accessapi.Worker, runTok.Raw},
		"a worker token on a run's":      {workers, accessapi.Runner, tok.Raw},
		"no token on a run's":            {workers, accessapi.Runner, ""},
		"a run token on the API":         {NewAuthenticator(auth, publicURL), accessapi.Account, runTok.Raw},
		"the API's on a run's route":     {NewAuthenticator(auth, publicURL), accessapi.Runner, runTok.Raw},
	} {
		if code := send(tc.a, tc.acc, tc.bearer); code != http.StatusUnauthorized {
			t.Errorf("%s = %d", name, code)
		}
	}
}

// TestUnitStateAuthenticator checks that the state backend's
// authenticator admits a run token as the password of Basic credentials
// named run, putting the run's principal on the context, and refuses with
// the Basic challenge another user name, a worker session token, an API
// token, a bearer header and no credential; that it refuses any other
// route; and that no other authenticator admits a state route.
func TestUnitStateAuthenticator(t *testing.T) {
	base := newWorld(t)
	auth := authn.New(authn.Deps{
		Keys: base.st, Sessions: base.st, Tokens: subjects{base.st}, Audience: audience,
		TTL: 10 * time.Hour, WorkerTTL: time.Hour, RunTTL: time.Hour,
		Now: func() time.Time { return t0 },
	})
	run := kernel.NewULID()
	runTok, err := auth.RunToken(t.Context(), run)
	if err != nil {
		t.Fatal(err)
	}
	workerTok, err := auth.WorkerToken(t.Context(), kernel.NewULID())
	if err != nil {
		t.Fatal(err)
	}
	_, apiTok := base.memberToken(kernel.NewAccountID())
	apiTok = strings.TrimPrefix(apiTok, "Bearer ")
	var got kernel.Principal
	seen := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = kernel.PrincipalFrom(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	send := func(a Authenticator, acc accessapi.Access, set func(*http.Request)) *httptest.
		ResponseRecorder {
		got = kernel.Principal{}
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), "LOCK", "/", nil)
		set(req)
		a.Middleware(func(*http.Request) accessapi.Access { return acc })(seen).ServeHTTP(rec, req)
		return rec
	}
	basic := func(user, pass string) func(*http.Request) {
		return func(r *http.Request) { r.SetBasicAuth(user, pass) }
	}
	state := NewStateAuthenticator(auth)
	if rec := send(state, accessapi.State, basic("run", runTok.Raw)); rec.Code !=
		http.StatusNoContent || got.Kind != kernel.PrincipalRunner || got.ActorID != run {
		t.Errorf("a run token = %d, %+v", rec.Code, got)
	}
	challenge := `Basic realm="infraweave-state"`
	for name, set := range map[string]func(*http.Request){
		"no credential":          func(*http.Request) {},
		"another user name":      basic("admin", runTok.Raw),
		"an empty password":      basic("run", ""),
		"a worker session token": basic("run", workerTok.Raw),
		"an API token":           basic("run", apiTok),
		"a bearer header": func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+runTok.Raw)
		},
	} {
		if rec := send(state, accessapi.State, set); rec.Code != http.StatusUnauthorized ||
			rec.Header().Get("WWW-Authenticate") != challenge {
			t.Errorf("%s = %d %q", name, rec.Code, rec.Header().Get("WWW-Authenticate"))
		}
	}
	for name, tc := range map[string]struct {
		a   Authenticator
		acc accessapi.Access
	}{
		"an API route":                  {state, accessapi.Account},
		"a runner route":                {state, accessapi.Runner},
		"the API's on a state route":    {NewAuthenticator(auth, publicURL), accessapi.State},
		"the worker's on a state route": {NewWorkerAuthenticator(auth), accessapi.State},
		"the operator's on one":         {NewOperatorAuthenticator(auth), accessapi.State},
	} {
		if rec := send(tc.a, tc.acc, basic("run", runTok.Raw)); rec.Code !=
			http.StatusUnauthorized {
			t.Errorf("%s = %d", name, rec.Code)
		}
	}
}
