package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/idempotency"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/ratelimit"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/serve"
	"github.com/weaveplatform/infraweave/internal/platform/observe"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
)

func local() config.Config {
	c := config.Defaults()
	c.Server.Listen, c.Server.AdminListen = "127.0.0.1:0", "127.0.0.1:0"
	c.Server.Shutdown.ReadyDelay = 0
	return c
}

// memory is the controls' state in process memory, for tests that serve one
// handler and need no database.
func memory(c config.Server) Guards {
	return Guards{
		Switch:  ratelimit.NewCallers(c.RateLimit.DisabledCallers),
		Buckets: ratelimit.NewMemory(time.Now),
		Keys:    idempotency.NewMemory(c.Idempotency, time.Now),
	}
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

// TestUnitServerListeners checks which listeners each mode binds: the
// probes always, on the admin listener alone, and the API only in the modes
// that serve it.
func TestUnitServerListeners(t *testing.T) {
	for _, m := range []Mode{ModeServe, ModeScheduler, ModeAll} {
		t.Run(m.String(), func(t *testing.T) {
			c := local()
			api, admin := handlers(
				discard(),
				c.Server,
				m,
				memory(c.Server),
				observe.NewHealth(time.Now),
			)
			r, err := serve.Start(t.Context(), c.Server.Config, discard(), api, admin)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
			for _, probe := range []string{"/livez", "/startupz", "/readyz"} {
				if code := get(t, "http://"+r.AdminAddr()+probe); code != http.StatusOK {
					t.Errorf("admin %s: %d", probe, code)
				}
			}
			if !m.API {
				if r.APIAddr() != "" {
					t.Errorf("%s bound an API listener on %s", m, r.APIAddr())
				}
				return
			}
			if code := get(t, "http://"+r.APIAddr()+"/livez"); code != http.StatusNotFound {
				t.Errorf("API /livez: %d, want 404", code)
			}
			if code := get(
				t,
				"http://"+r.APIAddr()+"/api/v1/stacks",
			); code != http.StatusNotImplemented {
				t.Errorf("API /api/v1/stacks: %d, want 501", code)
			}
		})
	}
	if ModeAll.String() != "serve,scheduler" || ModeScheduler.String() != "scheduler" {
		t.Errorf("mode fields %q %q", ModeAll, ModeScheduler)
	}
}

func TestUnitServeNeedsADatabase(t *testing.T) {
	if err := Run(t.Context(), local(), discard(), ModeServe); !errors.Is(err, sql.ErrConfig) ||
		!strings.Contains(err.Error(), "database.url is required") {
		t.Errorf("Serve: %v", err)
	}
}

// TestUnitServeValidates checks that a setting only the API's controls read
// fails before the database opens or a listener binds.
func TestUnitServeValidates(t *testing.T) {
	c := local()
	c.Server.Idempotency.TTL = 0
	if err := Run(t.Context(), c, discard(), ModeServe); !errors.Is(err, serve.ErrConfig) {
		t.Errorf("Run: %v", err)
	}
}

// TestUnitServeDatabase checks that a bad database setting, or a server that
// does not answer, stops the mode before either listener binds.
func TestUnitServeDatabase(t *testing.T) {
	c := local()
	c.Database.MaxConns = 0
	if err := Run(t.Context(), c, discard(), ModeServe); !errors.Is(err, sql.ErrConfig) {
		t.Errorf("invalid setting: %v", err)
	}
	// Port 1 refuses at once, so this needs no server.
	c = local()
	c.Database.URL = "postgres://u:p@127.0.0.1:1/x?sslmode=disable&connect_timeout=2"
	if err := Run(
		t.Context(),
		c,
		discard(),
		ModeServe,
	); err == nil ||
		!strings.Contains(err.Error(), "database.url") {
		t.Errorf("unreachable server: %v", err)
	}
}
