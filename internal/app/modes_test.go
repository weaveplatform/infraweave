package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/serve"
)

func local() config.Config {
	c := config.Defaults()
	c.Server.Listen, c.Server.AdminListen = "127.0.0.1:0", "127.0.0.1:0"
	return c
}

func get(t *testing.T, url string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func TestUnitStartServesBothListeners(t *testing.T) {
	r, err := Start(t.Context(), local(), discard())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
	if code := get(t, "http://"+r.AdminAddr()+"/livez"); code != http.StatusOK {
		t.Errorf("admin /livez: %d", code)
	}
	if code := get(t, "http://"+r.APIAddr()+"/livez"); code != http.StatusNotFound {
		t.Errorf("API /livez: %d, want 404", code)
	}
	if code := get(t, "http://"+r.APIAddr()+"/api/v1/stacks"); code != http.StatusNotImplemented {
		t.Errorf("API /api/v1/stacks: %d, want 501", code)
	}
}

func TestUnitServe(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := Serve(ctx, local(), discard()); err != nil {
		t.Errorf("Serve: %v", err)
	}
}

// TestUnitServeValidates checks that a setting only the API's controls read
// fails before either listener binds.
func TestUnitServeValidates(t *testing.T) {
	c := local()
	c.Server.Idempotency.TTL = 0
	if r, err := Start(t.Context(), c, discard()); !errors.Is(err, serve.ErrConfig) {
		if r != nil {
			_ = r.Shutdown(context.Background())
		}
		t.Errorf("Start: %v", err)
	}
	if err := Serve(t.Context(), c, discard()); !errors.Is(err, serve.ErrConfig) {
		t.Errorf("Serve: %v", err)
	}
}
