package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/ratelimit"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

func TestUnitRuntimeConfigurationBodyPolicy(t *testing.T) {
	ops := apiOperations()
	trigger := ops["POST "+BaseURL+"/stacks/{stackId}/runs"]
	if !reflect.DeepEqual(trigger.SemanticBodyFields, []string{"/runtimeConfig"}) {
		t.Fatalf("trigger body policy = %v", trigger.SemanticBodyFields)
	}
	for key, op := range ops {
		if key != "POST "+BaseURL+"/stacks/{stackId}/runs" && len(op.SemanticBodyFields) > 0 {
			t.Errorf("unrelated operation %s has semantic body policy %v", key, op.SemanticBodyFields)
		}
	}
	router := Guarded(discard(), config.Defaults().Server, Operations{
		"POST /runtime":  {Cost: ratelimit.Standard, Public: true, Body: trigger.Body, SemanticBodyFields: trigger.SemanticBodyFields},
		"POST /ordinary": {Cost: ratelimit.Standard, Public: true, Body: trigger.Body},
	}, memory(config.Defaults().Server), nil, nil, nil)
	called := 0
	handler := func(w http.ResponseWriter, _ *http.Request) { called++; w.WriteHeader(http.StatusOK) }
	router.Post("/runtime", handler)
	router.Post("/ordinary", handler)
	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
		req.RemoteAddr = "192.0.2.1:1"
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	for body, want := range map[string]problem.FieldError{
		`{"runtimeConfig":{"beforePlan":"wrong"}}`:                     {Pointer: "/runtimeConfig/beforePlan", Reason: kernel.ReasonType},
		`{"runtimeConfig":{"beforePlan":[1]}}`:                         {Pointer: "/runtimeConfig/beforePlan/0", Reason: kernel.ReasonType},
		`{"runtimeConfig":{"beforPlan":[]}}`:                           {Pointer: "/runtimeConfig/beforPlan", Reason: kernel.ReasonUnknownField},
		`{"runtimeConfig":{"beforPlan":null}}`:                         {Pointer: "/runtimeConfig/beforPlan", Reason: kernel.ReasonUnknownField},
		`{"runtimeConfig":{"environment":[{"key":"A","value":true}]}}`: {Pointer: "/runtimeConfig/environment/0/value", Reason: kernel.ReasonType},
	} {
		rec := post("/runtime", body)
		wantTyped(t, rec, problem.ValidationFailed)
		var p problem.Problem
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || len(p.Errors) != 1 ||
			p.Errors[0].Pointer != want.Pointer || p.Errors[0].Reason != want.Reason {
			t.Fatalf("semantic body refusal = %s, %v", rec.Body, err)
		}
		wantTyped(t, post("/ordinary", body), problem.InvalidRequest)
	}
	for _, body := range []string{
		`{"unknown":null}`, `{"runType":true}`, `{"runtimeConfig":{"environment":[{"key":"A"}]}}`, `{"runtimeConfig":`,
	} {
		wantTyped(t, post("/runtime", body), problem.InvalidRequest)
	}
	if called != 0 {
		t.Fatalf("invalid body reached handler %d times", called)
	}
	if rec := post("/runtime", `{"runtimeConfig":{"beforePlan":[],"environment":[{"key":"A","value":"kept-secret"}]}}`); rec.Code != http.StatusOK || called != 1 {
		t.Fatalf("valid runtime body = %d %s", rec.Code, rec.Body)
	}
}
