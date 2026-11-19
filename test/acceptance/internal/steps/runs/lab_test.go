package runs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

func TestUnitLabRunProgressBoundsAndRedacts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const private = "private-response-note-username-state"
		var body strings.Builder
		body.WriteString(`{"state":"` + private + `","username":"` + private + `","history":[`)
		for i := range 40 {
			if i != 0 {
				body.WriteByte(',')
			}
			state := private
			if i == 39 {
				state = "FAILED"
			}
			fmt.Fprintf(&body, `{"state":%q,"note":%q,"username":%q,"terminal":true,"hasLogs":true}`, state, private, private)
		}
		body.WriteString(`]}`)
		polls := 0
		start := time.Now()
		err := waitLabRun(t.Context(), "run to finish", "FINISHED", func(context.Context) (harness.Response, error) {
			polls++
			return harness.Response{Status: 200, Body: []byte(body.String())}, nil
		})
		if !errors.Is(err, deps.ErrTimedOut) || time.Since(start) != deps.WaitLimit || polls < 2 {
			t.Fatal("the lab wait changed the existing deadline or polling")
		}
		if strings.Count(err.Error(), `"terminal":true`) != 16 || !strings.Contains(err.Error(), `"state":"UNKNOWN"`) ||
			!strings.Contains(err.Error(), `"state":"FAILED"`) || !strings.Contains(err.Error(), `"historyTruncated":true`) {
			t.Fatal("the lab wait lost its bounded canonical recent history")
		}
		for _, rendered := range []string{fmt.Sprint(err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
			if strings.Contains(rendered, private) || len(rendered) > 2500 {
				t.Fatal("the lab wait disclosed private values or unbounded history")
			}
		}
	})
}

type privateLabError struct{ permanent bool }

func (privateLabError) Error() string { panic("the lab wait must not format a transport error") }

func (e privateLabError) Unwrap() error {
	if e.permanent {
		return deps.ErrPermanent
	}
	return nil
}

func TestUnitLabRunFailuresAreSanitizedBeforePollingFormatsThem(t *testing.T) {
	for _, tc := range []struct {
		name, category string
		status         int
		body           string
		err            error
	}{
		{name: "transport", category: "transport", err: privateLabError{}},
		{name: "unauthorized", category: "unauthorized", status: 401},
		{name: "forbidden", category: "forbidden", status: 403},
		{name: "not found", category: "not_found", status: 404},
		{name: "throttled", category: "throttled", status: 429},
		{name: "server error", category: "server_error", status: 503},
		{name: "last server error", category: "server_error", status: 599},
		{name: "unknown status", category: "other_status", status: 700},
		{name: "malformed JSON", category: "invalid_json", status: 200, body: "private-unfinished-json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				body := tc.body
				if body == "" {
					body = `{"detail":"private-provider-response"}`
				}
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				err := waitLabRun(ctx, "run to finish", "FINISHED", func(context.Context) (harness.Response, error) {
					return harness.Response{Status: tc.status, Body: []byte(body), Err: privateLabError{}}, tc.err
				})
				if !errors.Is(err, deps.ErrTimedOut) || !errors.Is(err, harness.ErrStep) {
					t.Fatal("the lab wait lost its timeout and sanitized step-error identities")
				}
				var private privateLabError
				if errors.As(err, &private) {
					t.Fatal("the lab wait retained a raw transport cause")
				}
				if !strings.Contains(err.Error(), `"request":"`+tc.category+`"`) {
					t.Fatal("the lab wait lost its fixed failure category")
				}
				for _, rendered := range []string{fmt.Sprint(err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
					if strings.Contains(rendered, "private") {
						t.Fatal("the lab wait exposed a raw failure or lost its fixed category")
					}
				}
			})
		})
	}
}

func TestUnitLabRunWaitKeepsLastReadableProgress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		calls := 0
		err := waitLabRun(ctx, "run to finish", "FINISHED", func(context.Context) (harness.Response, error) {
			calls++
			if calls == 1 {
				return harness.Response{Status: 200, Body: []byte(`{"state":"PLANNING","history":[{"state":"PLANNING","hasLogs":true,"note":"private"}]}`)}, nil
			}
			return harness.Response{Status: 503, Body: []byte("private")}, nil
		})
		if !errors.Is(err, deps.ErrTimedOut) || !strings.Contains(err.Error(), `"state":"PLANNING"`) ||
			!strings.Contains(err.Error(), `"request":"server_error"`) || !strings.Contains(err.Error(), `"logs":true,"note":true`) {
			t.Fatal("a later unreadable response erased the last safe execution progress")
		}
		if calls != 7 {
			t.Fatal("diagnostics changed the original one-second polling schedule or added a final request")
		}
	})
}

func TestUnitLabRunWaitSucceedsWithoutExtraReads(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		err := waitLabRun(t.Context(), "run to finish", "FINISHED", func(context.Context) (harness.Response, error) {
			calls++
			if calls == 1 {
				return harness.Response{}, privateLabError{}
			}
			state := "INITIALIZING"
			if calls == 3 {
				state = "FINISHED"
			}
			return harness.Response{Status: 200, Body: []byte(`{"state":"` + state + `","history":[{"state":"` + state + `","note":"private"}]}`)}, nil
		})
		if err != nil || calls != 3 {
			t.Fatal("diagnostics changed success after a transient failure or added a request")
		}
	})
}

func TestUnitLabRunWaitDoesNotLetDiagnosticHistoryChangeSuccess(t *testing.T) {
	for _, tc := range []struct{ name, history string }{
		{"object", `{"private":"value"}`},
		{"note object", `[{"note":{"private":"value"}}]`},
		{"state number", `[{"state":7}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				err := waitLabRun(t.Context(), "run to finish", "FINISHED", func(context.Context) (harness.Response, error) {
					calls++
					return harness.Response{Status: 200, Body: []byte(`{"state":"FINISHED","history":` + tc.history + `}`)}, nil
				})
				if err != nil || calls != 1 {
					t.Fatal("diagnostic-only fields blocked the original state-only success condition")
				}
			})
		})
	}
}

func TestUnitLabRunWaitMalformedHistoryRemainsBestEffort(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		calls := 0
		err := waitLabRun(ctx, "run to finish", "FINISHED", func(context.Context) (harness.Response, error) {
			calls++
			return harness.Response{Status: 200, Body: []byte(`{"state":"PREPARING","history":[{"note":{"private":"value"}}]}`)}, nil
		})
		if !errors.Is(err, deps.ErrTimedOut) || errors.Is(err, harness.ErrStep) || calls != 7 ||
			!strings.Contains(err.Error(), `"state":"PREPARING"`) || !strings.Contains(err.Error(), `"request":"invalid_history"`) ||
			strings.Contains(err.Error(), "private") {
			t.Fatal("unreadable diagnostic history changed polling or disclosed raw values")
		}
	})
}

func TestUnitLabRunWaitPreservesPermanentFailureWithoutItsCause(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		err := waitLabRun(t.Context(), "run to finish", "FINISHED", func(context.Context) (harness.Response, error) {
			calls++
			return harness.Response{}, privateLabError{permanent: true}
		})
		if !errors.Is(err, deps.ErrPermanent) || errors.Is(err, deps.ErrTimedOut) || calls != 1 {
			t.Fatal("sanitization changed the existing permanent-error stop condition")
		}
		var raw privateLabError
		if errors.As(err, &raw) {
			t.Fatal("the lab wait retained a raw permanent error")
		}
		for _, rendered := range []string{fmt.Sprint(err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
			if strings.Contains(rendered, "private") || !strings.Contains(rendered, "transport") {
				t.Fatal("the lab wait disclosed its cause or lost the fixed transport category")
			}
		}
	})
}
