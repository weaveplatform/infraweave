package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/idempotency"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/ratelimit"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/serve"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/observe"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	vendorsassembly "github.com/weaveplatform/infraweave/internal/vendors/assembly"
	"github.com/weaveplatform/infraweave/test/acceptance"
)

func local(t testing.TB) config.Config {
	t.Helper()
	c := config.Defaults()
	c.Server.Listen, c.Server.AdminListen = "127.0.0.1:0", "127.0.0.1:0"
	c.Server.Shutdown.ReadyDelay = 0
	c.Secrets.KEK = kek.Config{Provider: kek.Local, Local: kek.LocalConfig{Key: acceptance.KEK}}
	c.ObjectStore.Driver = objectstore.DriverFS
	c.ObjectStore.FS.Root, c.ObjectStore.FS.URL = t.TempDir(), "http://infraweave.test"
	c.Server.PublicURL = "http://infraweave.test"
	// No test reaches the vendors' real indexes: a refresh here fails at
	// once against a closed port and keeps the empty catalogue.
	c.Vendors = vendorsassembly.Settings{
		TerraformIndex: "http://127.0.0.1:1/terraform",
		OpenTofuIndex:  "http://127.0.0.1:1/opentofu",
	}
	return c
}

// testKeyring is db's keyring under the local KEK local configures, the
// one a mode started with local's configuration loads.
// testEvents is the outbox on db with every domain's event kinds, as
// serve constructs it.
func testEvents(t testing.TB, db queue.DB) *outbox.Outbox {
	t.Helper()
	return outbox.New(db, queue.New(db), newKinds())
}

func testKeyring(t testing.TB, db crypt.Querier) *crypt.Keyring {
	t.Helper()
	p, closeKEK, err := openKEK(t.Context(), local(t).Secrets.KEK)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeKEK)
	keys, err := crypt.Load(t.Context(), db, p)
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

// byAddress authenticates every request as an API key of its own client
// address, so tests of what sits behind authentication keep one caller
// per address.
type byAddress struct{}

func (byAddress) Middleware(
	access func(*http.Request) accessapi.Access,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if access(r) == accessapi.Public {
				next.ServeHTTP(w, r)
				return
			}
			p := addressPrincipal(ratelimit.ClientAddr(r))
			next.ServeHTTP(w, r.WithContext(kernel.WithPrincipal(r.Context(), p)))
		})
	}
}

// addressPrincipal is the API key byAddress authenticates addr as.
func addressPrincipal(addr string) kernel.Principal {
	var id kernel.ULID
	h := sha256.Sum256([]byte(addr))
	copy(id[:], h[:])
	return kernel.Principal{Kind: kernel.PrincipalAPIKey, ActorID: id}
}

// addressCaller is the caller key of a request from addr under byAddress.
func addressCaller(addr string) string { return addressPrincipal(addr).Caller() }

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
			c := local(t)
			api, admin := handlers(
				discard(),
				c.Server,
				m,
				memory(c.Server),
				Domains{Auth: byAddress{}},
				Platform{},
				Blobs{},
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
				"http://"+r.APIAddr()+"/api/v1/stacks/infra/attachable-policies",
			); code != http.StatusNotImplemented {
				t.Errorf("API /api/v1/stacks/infra/attachable-policies: %d, want 501", code)
			}
		})
	}
	if ModeAll.String() != "serve,scheduler" || ModeScheduler.String() != "scheduler" {
		t.Errorf("mode fields %q %q", ModeAll, ModeScheduler)
	}
}

func TestUnitServeNeedsADatabase(t *testing.T) {
	if err := Run(t.Context(), local(t), discard(), ModeServe); !errors.Is(err, sql.ErrConfig) ||
		!strings.Contains(err.Error(), "database.url is required") {
		t.Errorf("Serve: %v", err)
	}
}

// TestUnitServeNeedsAKEK checks that a mode that seals refuses to start
// without a KEK, or with one it cannot read, before it reaches the
// database: port 1 would refuse the connection.
func TestUnitServeNeedsAKEK(t *testing.T) {
	for name, kc := range map[string]kek.Config{
		"no provider": {},
		"bad key":     {Provider: kek.Local, Local: kek.LocalConfig{Key: "c2hvcnQ="}},
	} {
		t.Run(name, func(t *testing.T) {
			c := local(t)
			c.Database.URL = "postgres://u:p@127.0.0.1:1/x?sslmode=disable"
			c.Secrets.KEK = kc
			for _, m := range []Mode{ModeServe, ModeScheduler, ModeAll} {
				if err := Run(
					t.Context(),
					c,
					discard(),
					m,
				); !errors.Is(err, kek.ErrNoProvider) &&
					!errors.Is(err, kek.ErrConfig) {
					t.Errorf("%s: %v", m, err)
				}
			}
		})
	}
}

// TestUnitKeyringCheck checks that the startup check, the only one
// registered here, fails on a keyring with no active key, which Load
// never returns.
func TestUnitKeyringCheck(t *testing.T) {
	h := observe.NewHealth(time.Now)
	keyringCheck(h, &crypt.Keyring{})
	rec := httptest.NewRecorder()
	observe.Probes(discard(), h).
		ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/startupz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/startupz %d %s", rec.Code, rec.Body)
	}
}

// TestUnitServeValidates checks that a setting only the API's controls read
// fails before the database opens or a listener binds.
func TestUnitServeValidates(t *testing.T) {
	c := local(t)
	c.Server.Idempotency.TTL = 0
	if err := Run(t.Context(), c, discard(), ModeServe); !errors.Is(err, serve.ErrConfig) {
		t.Errorf("Run: %v", err)
	}
}

// TestUnitServeDatabase checks that a bad database setting, or a server that
// does not answer, stops the mode before either listener binds.
func TestUnitServeDatabase(t *testing.T) {
	c := local(t)
	c.Database.MaxConns = 0
	if err := Run(t.Context(), c, discard(), ModeServe); !errors.Is(err, sql.ErrConfig) {
		t.Errorf("invalid setting: %v", err)
	}
	// Port 1 refuses at once, so this needs no server.
	c = local(t)
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
