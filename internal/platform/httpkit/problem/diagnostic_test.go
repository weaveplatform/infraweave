package problem

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

type diagnosticSecretError struct {
	secret string
	state  string
}

func (*diagnosticSecretError) Error() string      { panic("diagnostics must not call Error") }
func (e *diagnosticSecretError) SQLState() string { return e.state }

type diagnosticWrapper struct {
	secret string
	cause  error
}

func (*diagnosticWrapper) Error() string   { panic("diagnostics must not call Error") }
func (e *diagnosticWrapper) Unwrap() error { return e.cause }

func captureDiagnostic(t *testing.T) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&out, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &out
}

func diagnosticResponse(t *testing.T, cause error) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	w.Header().Set(RequestIDHeader, "REQUEST615")
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/private-path?token=private-query", strings.NewReader(`{"secret":"private-body"}`))
	r.Header.Set("Authorization", "Bearer private-credential")
	r.Header.Set("Idempotency-Key", "private-idempotency-key")
	ResponseError(w, r, cause)
	return w
}

func TestUnitFailureDiagnosticPrivacy(t *testing.T) {
	logs := captureDiagnostic(t)
	cause := errors.Join(
		&diagnosticWrapper{secret: "wrapper-secret", cause: context.Canceled},
		&diagnosticSecretError{secret: "database-secret", state: "P0001"},
		context.DeadlineExceeded,
	)
	t.Setenv(diagnosticEnv, "0")
	want := diagnosticResponse(t, cause)
	t.Setenv(diagnosticEnv, "1")
	got := diagnosticResponse(t, cause)
	if got.Code != http.StatusInternalServerError || got.Code != want.Code ||
		!reflect.DeepEqual(got.Header(), want.Header()) || got.Body.String() != want.Body.String() {
		t.Fatal("diagnostic changed the sanitized 500 response")
	}
	for _, secret := range []string{
		"wrapper-secret", "database-secret", "private-path", "private-query", "private-body", "private-credential", "private-idempotency-key",
	} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("diagnostic exposed an error value or request data")
		}
	}
	var record map[string]any
	if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
		t.Fatal("diagnostic did not emit exactly one JSON record")
	}
	for field := range record {
		if !slices.Contains(
			[]string{"time", "level", "msg", "diagnostic", "request_id", "stage", "callers", "error_types", "sqlstate", "canceled", "deadline", "truncated"},
			field,
		) {
			t.Fatal("diagnostic emitted a field outside its metadata allowlist")
		}
	}
	for field, want := range map[string]any{
		"diagnostic": diagnosticMarker, "request_id": "REQUEST615", "sqlstate": "P0001", "canceled": true, "deadline": true, "truncated": false,
	} {
		if record[field] != want {
			t.Fatal("diagnostic lost correlation, SQLSTATE or cancellation metadata")
		}
	}
	stage, stageOK := record["stage"].(string)
	callers, callersOK := record["callers"].([]any)
	if !stageOK || !callersOK || !strings.HasSuffix(stage, "/problem.ResponseError") ||
		len(callers) == 0 || len(callers) > 3 || callers[0] != stage {
		t.Fatal("diagnostic stage does not identify its boundary")
	}
	types, typesOK := record["error_types"].([]any)
	foundCause := false
	for _, value := range types {
		if name, ok := value.(string); ok && strings.HasSuffix(name, "/problem.diagnosticSecretError") {
			foundCause = true
		}
	}
	if !typesOK || !foundCause || len(types) > diagnosticTypeLimit {
		t.Fatal("diagnostic did not retain a bounded concrete error-type chain")
	}
}

func TestUnitFailureDiagnosticDisabled(t *testing.T) {
	for _, flag := range []string{"", "0", "true"} {
		t.Run(flag, func(t *testing.T) {
			t.Setenv(diagnosticEnv, flag)
			logs := captureDiagnostic(t)
			got := diagnosticResponse(t, &diagnosticSecretError{secret: "not-readable", state: "23505"})
			if got.Code != http.StatusInternalServerError || logs.Len() != 0 {
				t.Fatal("flag-off changed the response or emitted diagnostics")
			}
		})
	}
}

func TestUnitFailureDiagnosticOrdinaryResponses(t *testing.T) {
	for name, tc := range map[string]struct {
		cause  error
		status int
	}{
		"unavailable": {kernel.ErrUnavailable, http.StatusServiceUnavailable},
		"invalid":     {InvalidRequest.Error("invalid input"), http.StatusBadRequest},
		"conflict":    {InvalidState.Error("not ended"), http.StatusConflict},
	} {
		t.Run(name, func(t *testing.T) {
			logs := captureDiagnostic(t)
			t.Setenv(diagnosticEnv, "0")
			want := diagnosticResponse(t, tc.cause)
			t.Setenv(diagnosticEnv, "1")
			got := diagnosticResponse(t, tc.cause)
			if got.Code != tc.status || got.Code != want.Code || !reflect.DeepEqual(got.Header(), want.Header()) ||
				got.Body.String() != want.Body.String() || logs.Len() != 0 {
				t.Fatal("diagnostic changed an ordinary response or logged it")
			}
		})
	}
}

func TestUnitFailureDiagnosticSQLState(t *testing.T) {
	for _, state := range []string{"23505", "40P01", "P0001", "", "123456", "abcde", "AB'12", "A\n123"} {
		t.Run(state, func(t *testing.T) {
			t.Setenv(diagnosticEnv, "1")
			logs := captureDiagnostic(t)
			diagnosticResponse(t, &diagnosticSecretError{secret: "state-secret", state: state})
			var record struct {
				SQLState string `json:"sqlstate"`
			}
			if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
				t.Fatal("diagnostic record is not JSON")
			}
			want := ""
			if slices.Contains([]string{"23505", "40P01", "P0001"}, state) {
				want = state
			}
			if record.SQLState != want {
				t.Fatal("diagnostic accepted a malformed SQLSTATE or lost a valid one")
			}
		})
	}
}

type diagnosticCycle struct{ calls int }

func (*diagnosticCycle) Error() string   { panic("diagnostics must not call Error") }
func (e *diagnosticCycle) Unwrap() error { e.calls++; return e }

type diagnosticJoinCycle struct{ calls int }

func (*diagnosticJoinCycle) Error() string { panic("diagnostics must not call Error") }
func (*diagnosticJoinCycle) As(any) bool   { panic("diagnostics must inspect only the current node") }

func (e *diagnosticJoinCycle) Unwrap() []error {
	e.calls++
	return []error{e, context.Canceled, &diagnosticSecretError{state: "23505"}, context.DeadlineExceeded}
}

func TestUnitFailureDiagnosticBounds(t *testing.T) {
	cycle := &diagnosticCycle{}
	meta := diagnosticErrors(cycle)
	if len(meta.types) != diagnosticTypeLimit || cycle.calls > diagnosticTypeLimit || meta.state != "" || !meta.truncated {
		t.Fatal("diagnostic did not bound a cyclic error chain")
	}
	joined := &diagnosticJoinCycle{}
	meta = diagnosticErrors(joined)
	if joined.calls > diagnosticTypeLimit || len(meta.types) > diagnosticTypeLimit ||
		meta.state != "23505" || !meta.canceled || !meta.deadline || !meta.truncated {
		t.Fatal("diagnostic recursed through a joined cycle or lost its siblings")
	}
	children := make([]error, 1000)
	for i := range children {
		children[i] = &diagnosticSecretError{secret: "joined-secret", state: "23505"}
	}
	meta = diagnosticErrors(errors.Join(children...))
	if len(meta.types) != diagnosticTypeLimit || meta.state != "23505" || !meta.truncated {
		t.Fatal("diagnostic did not bound joined errors or retain their SQLSTATE")
	}
	if got := diagnosticSymbol(strings.Repeat("symbol", 1000)); len(got) > diagnosticSymbolMax {
		t.Fatal("diagnostic symbol is unbounded")
	}
	meta = diagnosticErrors(nil)
	if len(meta.types) != 0 || meta.state != "" || meta.truncated {
		t.Fatal("nil cause invented error metadata")
	}
}

type diagnosticPanickingMetadata struct{}

func (diagnosticPanickingMetadata) Error() string    { panic("diagnostics must not call Error") }
func (diagnosticPanickingMetadata) SQLState() string { panic("private metadata value") }

func TestUnitFailureDiagnosticMetadataPanic(t *testing.T) {
	t.Setenv(diagnosticEnv, "1")
	logs := captureDiagnostic(t)
	got := diagnosticResponse(t, diagnosticPanickingMetadata{})
	if got.Code != http.StatusInternalServerError || logs.Len() != 0 {
		t.Fatal("failing metadata observation changed the original 500")
	}
}

type diagnosticPanickingHandler struct{}

func (diagnosticPanickingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (diagnosticPanickingHandler) Handle(context.Context, slog.Record) error {
	panic("private logger value")
}
func (h diagnosticPanickingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h diagnosticPanickingHandler) WithGroup(string) slog.Handler      { return h }

func TestUnitFailureDiagnosticLoggerPanic(t *testing.T) {
	t.Setenv(diagnosticEnv, "1")
	previous := slog.Default()
	slog.SetDefault(slog.New(diagnosticPanickingHandler{}))
	t.Cleanup(func() { slog.SetDefault(previous) })
	got := diagnosticResponse(t, &diagnosticSecretError{state: "23505"})
	if got.Code != http.StatusInternalServerError {
		t.Fatal("failing diagnostic logger replaced the original 500")
	}
}

type diagnosticGenericError[T any] struct{}

func (diagnosticGenericError[T]) Error() string { panic("diagnostics must not call Error") }

func TestUnitFailureDiagnosticTypePrivacy(t *testing.T) {
	t.Setenv(diagnosticEnv, "1")
	for name, cause := range map[string]error{
		"unnamed": struct {
			error
			Payload string `secret:"private-tag"`
		}{error: &diagnosticSecretError{state: "23505"}, Payload: "private-payload"},
		"generic": diagnosticGenericError[struct {
			Payload string `secret:"private-generic-tag"`
		}]{},
	} {
		t.Run(name, func(t *testing.T) {
			logs := captureDiagnostic(t)
			got := diagnosticResponse(t, cause)
			if got.Code != http.StatusInternalServerError || logs.Len() == 0 {
				t.Fatal("type privacy observation failed or changed the response")
			}
			for _, value := range []string{"Payload", "private-tag", "private-generic-tag", "private-payload", "secret:"} {
				if strings.Contains(logs.String(), value) {
					t.Fatal("diagnostic type identity exposed structure, tags or values")
				}
			}
		})
	}
}
