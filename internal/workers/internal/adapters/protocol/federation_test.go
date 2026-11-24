package protocol_test

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// TestUnitProtocolRunOIDCToken checks that a run token's bearer gets the
// federation token reissued for its run as {token, expiresAt}; that no
// token, another run's, a session token and a malformed run ID answer
// 401; that a run off its worker or in no phase answers 409
// invalid-state; and that an account whose tier lacks the feature answers
// 403 feature-not-in-tier naming it and its tiers, and one whose operator
// switched it off 403 feature-disabled.
func TestUnitProtocolRunOIDCToken(t *testing.T) {
	f := newFixture(t)
	run := kernel.NewULID()
	sch := &scheduler{federation: workersapi.FederationToken{Raw: "a.b.c", Expires: time.Unix(2_000_000_000, 0)}}
	f.svc.BindScheduler(sch)
	path, token := "/runs/"+run.String()+"/oidc-token", "run."+run.String()
	code, body := f.call(t, path, token, nil)
	if want := map[string]any{"token": "a.b.c", "expiresAt": 2e9}; code != http.StatusOK ||
		!reflect.DeepEqual(body, want) {
		t.Errorf("oidc-token = %d %v, want %v", code, body, want)
	}
	for name, tc := range map[string]struct{ path, token string }{
		"no token":          {path, ""},
		"another run's":     {path, "run." + kernel.NewULID().String()},
		"a malformed run":   {"/runs/nope/oidc-token", token},
		"a session token's": {path, "session." + kernel.NewULID().String()},
	} {
		if code, body := f.call(t, tc.path, tc.token, nil); code != http.StatusUnauthorized ||
			body["type"] != "urn:infraweave:problem:unauthenticated" {
			t.Errorf("%s = %d %v", name, code, body)
		}
	}
	for name, tc := range map[string]struct {
		err    error
		status int
		want   map[string]any
	}{
		"off its worker": {workersapi.ErrNotOnWorker, http.StatusConflict, map[string]any{
			"type": "urn:infraweave:problem:invalid-state", "detail": "The run is not on a worker.",
		}},
		"in no phase": {workersapi.ErrNotInPhase, http.StatusConflict, map[string]any{
			"type":   "urn:infraweave:problem:invalid-state",
			"detail": "The run is in no phase that receives a federation token.",
		}},
		"not in its tier": {
			&featuresapi.Refusal{Feature: featuresapi.OIDCFederation, Tiers: []string{"Starter", "Business"}},
			http.StatusForbidden,
			map[string]any{
				"type": "urn:infraweave:problem:feature-not-in-tier", "feature": "oidc-federation",
				"tiers": []any{"Starter", "Business"},
			},
		},
		"switched off": {
			&featuresapi.Refusal{Disabled: true, Feature: featuresapi.OIDCFederation},
			http.StatusForbidden,
			map[string]any{
				"type": "urn:infraweave:problem:feature-disabled", "feature": "oidc-federation",
			},
		},
	} {
		sch.err = tc.err
		code, body := f.call(t, path, token, nil)
		if code != tc.status {
			t.Errorf("%s = %d %v", name, code, body)
		}
		for k, v := range tc.want {
			if !reflect.DeepEqual(body[k], v) {
				t.Errorf("%s: %s = %v, want %v", name, k, body[k], v)
			}
		}
		if _, ok := body["token"]; ok {
			t.Errorf("%s answers a token: %v", name, body)
		}
	}
}
