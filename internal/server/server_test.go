package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func local() Config {
	c := Defaults()
	c.Listen, c.AdminListen = "127.0.0.1:0", "127.0.0.1:0"
	return c
}

func TestUnitValidate(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	cases := []struct {
		name string
		edit func(*Config)
		want string
	}{
		{"no port", func(c *Config) { c.Listen = "localhost" }, `server.listen "localhost"`},
		{"port out of range", func(c *Config) { c.AdminListen = ":70000" }, "server.admin_listen"},
		{"port not a number", func(c *Config) { c.Listen = ":http" }, "port must be a number"},
		{
			"no timeout",
			func(c *Config) { c.Shutdown.Timeout = 0 },
			"server.shutdown.timeout must be positive",
		},
		{
			"empty bucket",
			func(c *Config) { c.RateLimit.Expensive.Burst = 0 },
			"server.rate_limit.expensive: rate must be positive",
		},
		{
			"no key lifetime",
			func(c *Config) { c.Idempotency.TTL = 0 },
			"server.idempotency.ttl must be positive",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Defaults()
			tc.edit(&c)
			err := c.Validate()
			if !errors.Is(err, errConfig) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an invalid configuration naming %q", err, tc.want)
			}
		})
	}
}

func get(t *testing.T, url string) (int, string) {
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
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b)
}

func TestUnitStartServesBothListeners(t *testing.T) {
	r, err := Start(t.Context(), local(), discard())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
	if code, _ := get(t, "http://"+r.AdminAddr()+"/livez"); code != http.StatusOK {
		t.Errorf("admin /livez: %d", code)
	}
	if code, _ := get(t, "http://"+r.APIAddr()+"/livez"); code != http.StatusNotFound {
		t.Errorf("API /livez: %d, want 404", code)
	}
	if code, _ := get(
		t,
		"http://"+r.APIAddr()+"/api/v1/stacks",
	); code != http.StatusNotImplemented {
		t.Errorf("API /api/v1/stacks: %d, want 501", code)
	}
}

func TestUnitStartErrors(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	for name, edit := range map[string]func(*Config){
		"invalid":    func(c *Config) { c.Listen = "nowhere" },
		"API busy":   func(c *Config) { c.Listen = busy.Addr().String() },
		"admin busy": func(c *Config) { c.AdminListen = busy.Addr().String() },
	} {
		t.Run(name, func(t *testing.T) {
			c := local()
			edit(&c)
			if r, err := Start(t.Context(), c, discard()); err == nil {
				_ = r.Shutdown(context.Background())
				t.Fatal("started")
			}
		})
	}
}

// TestUnitRunDrains checks that cancelling Run, as SIGTERM does, lets an
// in-flight request finish and then returns nil.
func TestUnitRunDrains(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	slow := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte("done"))
	})
	r, err := start(t.Context(), local(), discard(), slow, Admin(discard()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- r.wait(ctx, discard()) }()

	// The request runs off the test goroutine, so it reports through body
	// rather than t.Fatal.
	body := make(chan string, 1)
	go func() {
		req, _ := http.NewRequestWithContext(
			context.Background(),
			http.MethodGet,
			"http://"+r.APIAddr()+"/",
			nil,
		)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			body <- err.Error()
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		body <- string(b)
	}()
	<-entered
	cancel()
	// Shutdown has begun once new connections are refused.
	for {
		c, err := net.DialTimeout("tcp", r.APIAddr(), time.Second)
		if err != nil {
			break
		}
		_ = c.Close()
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	if b := <-body; b != "done" {
		t.Errorf("in-flight request got %q", b)
	}
	if err := <-done; err != nil {
		t.Errorf("Run returned %v after a clean shutdown", err)
	}
}

func TestUnitRunShutdownTimeout(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	stuck := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(entered)
		<-release
	})
	c := local()
	c.Shutdown.Timeout = 50 * time.Millisecond
	r, err := start(t.Context(), c, discard(), stuck, Admin(discard()))
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		req, _ := http.NewRequestWithContext(
			context.Background(),
			http.MethodGet,
			"http://"+r.APIAddr()+"/",
			nil,
		)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-entered
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := r.wait(ctx, discard()); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("got %v, want the shutdown deadline", err)
	}
}

func TestUnitRunListenerFails(t *testing.T) {
	r, err := Start(t.Context(), local(), discard())
	if err != nil {
		t.Fatal(err)
	}
	_ = r.apiLn.Close()
	if err := r.wait(
		t.Context(),
		discard(),
	); err == nil ||
		!strings.Contains(err.Error(), "serve") {
		t.Errorf("got %v, want the listener's error", err)
	}
}

func TestUnitRun(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := Run(ctx, local(), discard()); err != nil {
		t.Errorf("Run: %v", err)
	}
	c := local()
	c.Listen = "nowhere"
	if err := Run(t.Context(), c, discard()); !errors.Is(err, errConfig) {
		t.Errorf("Run with a bad listener: %v", err)
	}
}
