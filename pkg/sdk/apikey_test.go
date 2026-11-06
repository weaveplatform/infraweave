package sdk

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-chi/chi/v5"
)

// exchanger is a server that answers each exchange with a new token valid
// for life, and records the bearer of every other request.
type exchanger struct {
	mu        sync.Mutex
	life      time.Duration
	exchanges int
	bearers   []string
	refuse    bool
}

func (e *exchanger) handler(t *testing.T) http.Handler {
	r := chi.NewRouter()
	r.Post(ExchangePath, func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ID     string `json:"id"`
			Secret string `json:"secret"`
		}
		if err := json.NewDecoder(r.Body).
			Decode(&in); err != nil || in.ID != "key" ||
			in.Secret != "iwk_s" {
			t.Errorf("exchanged %+v, %v", in, err)
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.refuse {
			writeProblem(w, http.StatusUnauthorized, "unauthenticated", nil)
			return
		}
		e.exchanges++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jwt":        fmt.Sprintf("token-%d", e.exchanges),
			"validUntil": time.Now().Add(e.life).Unix(),
		})
	})
	r.Get(APIPrefix+"/viewer", func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		e.bearers = append(e.bearers, r.Header.Get("Authorization"))
		e.mu.Unlock()
		_, _ = w.Write([]byte("{}"))
	})
	return r
}

// TestUnitSDKAPIKey checks, in virtual time, that the first request
// exchanges the key, later ones reuse the token, and the client exchanges
// again once the token is within RefreshBefore of its validUntil.
func TestUnitSDKAPIKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := &exchanger{life: 10 * time.Hour}
		c := client(t, e.handler(t), WithAPIKey("key", "iwk_s"))
		get := func() {
			t.Helper()
			if _, err := c.Do(t.Context(), http.MethodGet, APIPrefix+"/viewer", nil); err != nil {
				t.Fatal(err)
			}
		}
		get()
		get()
		<-time.After(10*time.Hour - RefreshBefore - time.Second)
		get()
		<-time.After(2 * time.Second)
		get()
		get()
		want := []string{
			"Bearer token-1",
			"Bearer token-1",
			"Bearer token-1",
			"Bearer token-2",
			"Bearer token-2",
		}
		if e.exchanges != 2 || fmt.Sprint(e.bearers) != fmt.Sprint(want) {
			t.Errorf("%d exchanges, bearers %v, want %v", e.exchanges, e.bearers, want)
		}
		// A caller's own Authorization header is sent as it is.
		if _, err := c.Do(t.Context(), http.MethodGet, APIPrefix+"/viewer", nil,
			Header{"Authorization", "Bearer mine"}); err != nil || e.bearers[5] != "Bearer mine" {
			t.Errorf("a caller's header: %v, %v", e.bearers, err)
		}
	})
}

// TestUnitSDKAPIKeyRefused checks that a refused exchange is the
// request's typed error and sends no request, and that an answer with no
// token or a server that cannot be reached is an error too.
func TestUnitSDKAPIKeyRefused(t *testing.T) {
	e := &exchanger{life: time.Hour, refuse: true}
	c := client(t, e.handler(t), WithAPIKey("key", "iwk_s"))
	_, err := c.Do(t.Context(), http.MethodGet, APIPrefix+"/viewer", nil)
	var p Unauthenticated
	if !errors.As(err, &p) || len(e.bearers) != 0 {
		t.Errorf("a refused exchange: %v, %v sent", err, e.bearers)
	}

	empty := chi.NewRouter()
	empty.Post(
		ExchangePath,
		func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) },
	)
	if _, err := client(
		t,
		empty,
		WithAPIKey("key", "iwk_s"),
	).Do(t.Context(), http.MethodGet, "/x", nil); err == nil {
		t.Error("an answer with no token")
	}
	gone, err := New("http://infraweave.test", WithHTTPClient(failing{errors.New("no route")}),
		WithRetry(Retry{Attempts: 1}), WithAPIKey("key", "iwk_s"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gone.Do(t.Context(), http.MethodGet, "/x", nil); err == nil {
		t.Error("an unreachable server")
	}
	bad, err := New("http://infraweave.test\x7f", WithAPIKey("key", "iwk_s"))
	if err == nil {
		if _, err := bad.Do(t.Context(), http.MethodGet, "/x", nil); err == nil {
			t.Error("a base URL no request can be made for")
		}
	}
}
