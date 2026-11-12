package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/ratelimit"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/serve"
)

// TestUnitHeldResponses checks, on a real listener whose read and write
// timeouts are shorter than a hold, that an operation with a Hold answers
// 200 after it, its request's context live throughout, while an operation
// without one that takes as long loses its connection; and that only the
// worker poll holds, for the hold withPollHold gives it.
func TestUnitHeldResponses(t *testing.T) {
	c := config.Defaults().Server
	c.Listen, c.AdminListen = "127.0.0.1:0", "127.0.0.1:0"
	c.Timeouts = serve.Timeouts{
		Header: 200 * time.Millisecond, Read: 300 * time.Millisecond,
		Write: 300 * time.Millisecond, Idle: time.Second,
	}
	r := Guarded(discard(), c, Operations{
		"POST /poll": {Cost: ratelimit.Standard, Public: true, Hold: time.Second},
		"POST /slow": {Cost: ratelimit.Standard, Public: true},
	}, memory(c), nil, nil, nil)
	// Each reads its body first, as the poll's decoder does, after which
	// net/http watches the connection for the client leaving.
	wait := func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		select {
		case <-time.After(800 * time.Millisecond):
			_, _ = w.Write([]byte("held"))
		case <-r.Context().Done():
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}
	r.Post("/poll", wait)
	r.Post("/slow", wait)
	srv, err := serve.Start(t.Context(), c.Config, discard(), r, http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	post := func(path string) (int, string, error) {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost,
			"http://"+srv.APIAddr()+path, strings.NewReader("{}"))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, "", err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), err
	}
	if code, body, err := post("/poll"); err != nil || code != http.StatusOK || body != "held" {
		t.Errorf("a held response = %d %q, %v", code, body, err)
	}
	if code, body, err := post("/slow"); err == nil {
		t.Errorf("an unheld response past the write timeout = %d %q", code, body)
	}
	ops := withPollHold(apiOperations(), 42*time.Second)
	if got := ops[http.MethodPost+" "+WorkerBaseURL+"/poll"].Hold; got != 42*time.Second {
		t.Errorf("the poll's hold = %s", got)
	}
	if apiOperations()[http.MethodPost+" "+WorkerBaseURL+"/poll"].Hold != 0 {
		t.Error("withPollHold changed the shared route table")
	}
	for key, op := range ops {
		if op.Hold != 0 && key != http.MethodPost+" "+WorkerBaseURL+"/poll" {
			t.Errorf("%s holds for %s; only the worker poll holds its response", key, op.Hold)
		}
	}
}
