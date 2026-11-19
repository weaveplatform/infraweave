package runs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

func TestUnitLabRunWaitRetainsSafeHistory(t *testing.T) {
	const private = "private-note-body-credential"
	router := chi.NewRouter()
	router.Get("/api/v1/runs/kept-run", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"state":"PREPARING","history":[`+
			`{"state":"QUEUED","hasLogs":false,"terminal":false},`+
			`{"state":"PREPARING","hasLogs":false,"terminal":false,"note":"`+private+`"}]}`)
	})
	server := httptest.NewServer(router)
	defer server.Close()
	world := &harness.World{
		Tags:    []string{"@vcs-lab"},
		Servers: []*harness.Server{{API: strings.TrimPrefix(server.URL, "http://")}},
		Tokens:  map[string]string{"h": "fixture-token"},
		IDs:     map[string]string{"tracked": "kept-run"},
		Answers: map[string][][]byte{},
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	err := (&runSteps{w: world}).eventually(ctx, "h", "tracked", "FINISHED", "A")
	if !errors.Is(err, deps.ErrTimedOut) || len(world.Sent) == 0 {
		t.Fatal("the existing GET wait did not time out after observing the run")
	}
	if !strings.Contains(err.Error(), `"state":"PREPARING"`) || !strings.Contains(err.Error(), `"note":true`) {
		t.Fatal("the lab timeout lost its safe observed state/history")
	}
	for _, rendered := range []string{fmt.Sprint(err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
		if strings.Contains(rendered, private) {
			t.Fatal("the lab timeout disclosed a private run note")
		}
	}
}

func TestUnitRunWaitPreservesNonLabBehavior(t *testing.T) {
	const body = "ordinary fixture body"
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "success", status: 200, body: `{"state":"FINISHED"}`},
		{name: "timeout", status: 200, body: `{"state":"PREPARING"}`, want: "waiting for run tracked to be FINISHED: timed out"},
		{name: "bad response", status: 403, body: body, want: "waiting for run tracked to be FINISHED: timed out (last: step failed: no run in 403 " + body + ")"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := chi.NewRouter()
			router.Get("/api/v1/runs/kept-run", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprint(w, tc.body)
			})
			server := httptest.NewServer(router)
			defer server.Close()
			world := &harness.World{
				Servers: []*harness.Server{{API: strings.TrimPrefix(server.URL, "http://")}},
				Tokens:  map[string]string{"h": "fixture-token"},
				IDs:     map[string]string{"tracked": "kept-run"},
				Answers: map[string][][]byte{},
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			err := (&runSteps{w: world}).eventually(ctx, "h", "tracked", "FINISHED", "A")
			if tc.want == "" {
				if err != nil || len(world.Sent) != 1 {
					t.Fatal("the non-lab success changed")
				}
			} else if err == nil || err.Error() != tc.want {
				t.Fatal("the existing non-lab timeout/error text changed")
			}
		})
	}
}

func TestUnitLabRunWaitPreservesReplicaAndToken(t *testing.T) {
	var calls atomic.Int32
	router := chi.NewRouter()
	router.Get("/api/v1/runs/kept-run", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("the lab poll changed authentication")
		}
		_, _ = fmt.Fprint(w, `{"state":"FINISHED","history":[]}`)
	})
	server := httptest.NewServer(router)
	defer server.Close()
	world := &harness.World{
		Tags:    []string{"@vcs-lab"},
		Servers: []*harness.Server{{API: "unreachable.invalid"}, {API: strings.TrimPrefix(server.URL, "http://")}},
		Tokens:  map[string]string{"h": "fixture-token"},
		IDs:     map[string]string{"tracked": "kept-run"},
		Answers: map[string][][]byte{},
	}
	if err := (&runSteps{w: world}).eventually(t.Context(), "h", "tracked", "FINISHED", "B"); err != nil || calls.Load() != 1 {
		t.Fatal("the lab success changed replica or request count")
	}
}
