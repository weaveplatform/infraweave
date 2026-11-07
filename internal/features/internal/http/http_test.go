package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/iotest"

	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/features/internal/features"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/idempotency"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

var errDown = errors.New("store down")

// allower refuses the features in refuse, failing every feature with err,
// and records each feature it was asked about.
type allower struct {
	refuse map[string]*features.Refusal
	err    error
	asked  []string
}

func (a *allower) Allow(_ context.Context, feature string) error {
	a.asked = append(a.asked, feature)
	if r := a.refuse[feature]; r != nil {
		return r
	}
	return a.err
}

var apiKeys = featuresapi.Gate{
	Feature: "api-keys",
	Options: []featuresapi.OptionGate{
		{Property: "oidc", Feature: "oidc-api-keys"},
		{Property: "kind", Value: "admin", Feature: "admin-keys"},
	},
}

// serve runs body through Middleware with g as every request's gate, and
// returns the response and the body the handler read.
func serve(
	t *testing.T,
	a Allower,
	g featuresapi.Gate,
	gated bool,
	body io.Reader,
) (*httptest.ResponseRecorder, string) {
	t.Helper()
	var read string
	h := Middleware(a, func(*nethttp.Request) (featuresapi.Gate, bool) { return g, gated })(
		nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			b, _ := io.ReadAll(r.Body)
			read = string(b)
			w.WriteHeader(nethttp.StatusNoContent)
		}))
	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodPost, "/api-keys", body)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, read
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) problem.Problem {
	t.Helper()
	var p problem.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("not a problem: %s", rec.Body)
	}
	return p
}

// TestUnitFeatureMiddleware checks which features a request needs: its
// operation's capability, then each option its body sets, an option with
// no value by any value but null.
func TestUnitFeatureMiddleware(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
		want []string
	}{
		{"no options set", `{"name":"ci"}`, []string{"api-keys"}},
		{"an option set to any value", `{"oidc":{"issuer":"x"}}`, []string{"api-keys", "oidc-api-keys"}},
		{"an option set to null", `{"oidc":null}`, []string{"api-keys"}},
		{"an option's value", `{"kind":"admin"}`, []string{"api-keys", "admin-keys"}},
		{"another value", `{"kind":"reader"}`, []string{"api-keys"}},
		{"not an object", `[1]`, []string{"api-keys"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := &allower{}
			rec, read := serve(t, a, apiKeys, true, strings.NewReader(c.body))
			if rec.Code != nethttp.StatusNoContent || read != c.body ||
				!slices.Equal(a.asked, c.want) {
				t.Errorf("%d, handler read %q, asked %v; want %v", rec.Code, read, a.asked, c.want)
			}
		})
	}
	a := &allower{}
	if rec, _ := serve(
		t,
		a,
		featuresapi.Gate{},
		false,
		nil,
	); rec.Code != nethttp.StatusNoContent ||
		a.asked != nil {
		t.Errorf("an ungated request: %d, asked %v", rec.Code, a.asked)
	}
	a = &allower{}
	if rec, _ := serve(
		t,
		a,
		featuresapi.Gate{Feature: "stacks"},
		true,
		nil,
	); rec.Code != nethttp.StatusNoContent ||
		!slices.Equal(a.asked, []string{"stacks"}) {
		t.Errorf("a capability alone: %d, asked %v", rec.Code, a.asked)
	}
}

func TestUnitFeatureMiddlewareRefusals(t *testing.T) {
	disabled := &allower{refuse: map[string]*features.Refusal{
		"api-keys": {Disabled: true, Feature: "api-keys"},
	}}
	rec, _ := serve(t, disabled, apiKeys, true, strings.NewReader(`{}`))
	p := decode(t, rec)
	if rec.Code != nethttp.StatusForbidden ||
		p.Type != problem.TypePrefix+problem.FeatureDisabled.Slug ||
		p.Feature != "api-keys" ||
		p.Tiers != nil {
		t.Errorf("disabled: %d %+v", rec.Code, p)
	}

	lacked := &allower{refuse: map[string]*features.Refusal{
		"oidc-api-keys": {Feature: "oidc-api-keys", Tiers: []string{"Business", "Enterprise"}},
	}}
	rec, read := serve(t, lacked, apiKeys, true, strings.NewReader(`{"oidc":{}}`))
	p = decode(t, rec)
	if rec.Code != nethttp.StatusForbidden || read != "" ||
		p.Type != problem.TypePrefix+problem.FeatureNotInTier.Slug || p.Feature != "oidc-api-keys" ||
		!reflect.DeepEqual(p.Tiers, []string{"Business", "Enterprise"}) {
		t.Errorf("not in tier: %d %+v, handler read %q", rec.Code, p, read)
	}

	rec, _ = serve(t, &allower{err: errDown}, apiKeys, true, strings.NewReader(`{}`))
	if p := decode(t, rec); rec.Code != nethttp.StatusInternalServerError {
		t.Errorf("a failed check: %d %+v", rec.Code, p)
	}
}

func TestUnitFeatureMiddlewareBody(t *testing.T) {
	rec, _ := serve(t, &allower{}, apiKeys, true, iotest.ErrReader(errDown))
	if p := decode(t, rec); rec.Code != nethttp.StatusBadRequest ||
		p.Type != problem.TypePrefix+problem.InvalidRequest.Slug {
		t.Errorf("an unreadable body: %d %+v", rec.Code, p)
	}
	big := `{"name":"` + strings.Repeat("x", MaxBody) + `"}`
	rec, _ = serve(t, &allower{}, apiKeys, true, strings.NewReader(big))
	if p := decode(t, rec); rec.Code != nethttp.StatusRequestEntityTooLarge ||
		p.Type != problem.TypePrefix+problem.PayloadTooLarge.Slug {
		t.Errorf("a body over MaxBody: %d %+v", rec.Code, p)
	}
	if MaxBody != idempotency.MaxBody {
		t.Errorf("MaxBody %d, idempotency.MaxBody %d", MaxBody, idempotency.MaxBody)
	}
}
