package problem_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/ratelimit"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

type diagnosticTakeError struct{ secret string }

func (*diagnosticTakeError) Error() string    { panic("diagnostics must not call Error") }
func (*diagnosticTakeError) SQLState() string { return "P0001" }

type diagnosticTakeStore struct{ calls int }

func (s *diagnosticTakeStore) Take(context.Context, ratelimit.Key, ratelimit.Limit) (ratelimit.State, error) {
	s.calls++
	return ratelimit.State{}, &diagnosticTakeError{secret: "private-store-value"}
}

func TestUnitFailureDiagnosticRateLimitBoundary(t *testing.T) {
	t.Setenv("INFRAWEAVE_DIAGNOSTIC_HTTP500", "1")
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	store := &diagnosticTakeStore{}
	entered := false
	h := middleware.RequestID(ratelimit.Middleware(
		ratelimit.Defaults(), store,
		func(*http.Request) string { return "private-caller" },
		func(*http.Request) ratelimit.Class { return ratelimit.Standard },
	)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { entered = true })))
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/stacks/private-stack/runs/private-run/logs-delete?token=private-query", strings.NewReader("private-body"))
	r.Header.Set("Authorization", "Bearer private-credential")
	h.ServeHTTP(w, r)
	if entered || store.calls != 1 || w.Code != http.StatusInternalServerError {
		t.Fatal("rate-limit failure admitted its handler or changed status")
	}
	var body problem.Problem
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal("rate-limit error did not answer a problem")
	}
	id := w.Header().Get(problem.RequestIDHeader)
	if id == "" || body.Instance != problem.InstancePrefix+id || body.Detail != "The server failed to handle the request." {
		t.Fatal("rate-limit error lost its request ID or sanitized detail")
	}
	var record map[string]any
	if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
		t.Fatal("rate-limit error did not emit exactly one diagnostic")
	}
	stage, ok := record["stage"].(string)
	if !ok || !strings.Contains(stage, "/ratelimit.Middleware.") || record["request_id"] != id || record["sqlstate"] != "P0001" {
		t.Fatal("diagnostic missed the real rate-limit boundary or SQLSTATE")
	}
	for _, value := range []string{"private-store-value", "private-caller", "private-stack", "private-run", "private-query", "private-body", "private-credential"} {
		if strings.Contains(logs.String(), value) || strings.Contains(w.Body.String(), value) {
			t.Fatal("rate-limit diagnostic exposed an error or request value")
		}
	}
}
