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
// admits a worker session token on a worker route, putting the worker's
// principal and bearer on the context, and refuses an API token there; and
// that neither authenticator admits a credential on the other's routes.
func TestUnitWorkerAuthenticator(t *testing.T) {
	base := newWorld(t)
	auth := authn.New(authn.Deps{
		Keys: base.st, Sessions: base.st, Tokens: subjects{base.st}, Audience: audience,
		TTL: 10 * time.Hour, WorkerTTL: time.Hour, Now: func() time.Time { return t0 },
	})
	worker := kernel.NewULID()
	tok, err := auth.WorkerToken(t.Context(), worker)
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
	} {
		if code := send(tc.a, tc.acc, tc.bearer); code != http.StatusUnauthorized {
			t.Errorf("%s = %d", name, code)
		}
	}
}
