package serve

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// text is a handler that answers 200 with body.
func text(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	})
}

// start starts c with handlers that name the listener they answer on.
func start(t *testing.T, c Config) (*Running, error) {
	t.Helper()
	return Start(t.Context(), c, discard(), text("api"), text("admin"))
}

func local() Config {
	c := Defaults()
	c.Listen, c.AdminListen = "127.0.0.1:0", "127.0.0.1:0"
	c.Shutdown.ReadyDelay = 0
	return c
}

func nothing() {}

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
			"negative ready delay",
			func(c *Config) { c.Shutdown.ReadyDelay = -time.Second },
			"server.shutdown.ready_delay must not be negative",
		},
		{
			"no timeout",
			func(c *Config) { c.Shutdown.Timeout = 0 },
			"server.shutdown.timeout must be positive",
		},
		{
			"no read timeout",
			func(c *Config) { c.Timeouts.Read = 0 },
			"server.timeouts.read must be positive, got 0s",
		},
		{
			"a negative idle timeout",
			func(c *Config) { c.Timeouts.Idle = -time.Second },
			"server.timeouts.idle must be positive, got -1s",
		},
		{
			"no write timeout",
			func(c *Config) { c.Timeouts.Write = 0 },
			"server.timeouts.write must be positive",
		},
		{
			"no header timeout",
			func(c *Config) { c.Timeouts.Header = 0 },
			"server.timeouts.header must be positive",
		},
		{
			"headers longer than the request",
			func(c *Config) { c.Timeouts.Header = time.Minute },
			"server.timeouts.header 1m0s is longer than server.timeouts.read 30s",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Defaults()
			tc.edit(&c)
			err := c.Validate()
			if !errors.Is(err, ErrConfig) || !strings.Contains(err.Error(), tc.want) {
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
	r, err := start(t, local())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
	if code, body := get(t, "http://"+r.AdminAddr()+"/"); code != http.StatusOK || body != "admin" {
		t.Errorf("admin listener: %d %q", code, body)
	}
	if code, body := get(t, "http://"+r.APIAddr()+"/"); code != http.StatusOK || body != "api" {
		t.Errorf("API listener: %d %q", code, body)
	}
}

// TestUnitStartAdminOnly checks that a mode with no API binds only the
// admin listener.
func TestUnitStartAdminOnly(t *testing.T) {
	busy, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	c := local()
	c.Listen = busy.Addr().String() // never bound, so never in the way
	r, err := Start(t.Context(), c, discard(), nil, text("admin"))
	if err != nil {
		t.Fatal(err)
	}
	if code, body := get(
		t,
		"http://"+r.AdminAddr()+"/",
	); code != http.StatusOK ||
		body != "admin" ||
		r.APIAddr() != "" {
		t.Errorf("admin listener: %d %q, API %q", code, body, r.APIAddr())
	}
	if err := r.Shutdown(context.Background()); err != nil {
		t.Error(err)
	}
	c = local()
	c.AdminListen = busy.Addr().String()
	if _, err := Start(t.Context(), c, discard(), nil, text("admin")); err == nil {
		t.Error("started on a busy admin address")
	}
}

func TestUnitStartErrors(t *testing.T) {
	busy, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
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
			if r, err := start(t, c); err == nil {
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
	r, err := Start(t.Context(), local(), discard(), slow, text("admin"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	// net/http closes the listeners before it runs the shutdown hooks, so
	// once this fires new connections are refused.
	shuttingDown := make(chan struct{})
	r.api.RegisterOnShutdown(func() { close(shuttingDown) })
	done := make(chan error, 1)
	go func() { done <- r.Wait(ctx, discard(), nothing) }()

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
	<-shuttingDown
	if c, err := (&net.Dialer{Timeout: time.Second}).DialContext(
		t.Context(),
		"tcp",
		r.APIAddr(),
	); err == nil {
		_ = c.Close()
		t.Error("a new connection was accepted during shutdown")
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
	r, err := Start(t.Context(), c, discard(), stuck, text("admin"))
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
	if err := r.Wait(ctx, discard(), nothing); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("got %v, want the shutdown deadline", err)
	}
}

func TestUnitRunListenerFails(t *testing.T) {
	r, err := start(t, local())
	if err != nil {
		t.Fatal(err)
	}
	_ = r.apiLn.Close()
	if err := r.Wait(
		t.Context(),
		discard(),
		nothing,
	); err == nil ||
		!strings.Contains(err.Error(), "serve") {
		t.Errorf("got %v, want the listener's error", err)
	}
}

func TestUnitRun(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := Run(ctx, local(), discard(), text("api"), text("admin"), nothing); err != nil {
		t.Errorf("Run: %v", err)
	}
	c := local()
	c.Listen = "nowhere"
	if err := Run(
		t.Context(),
		c,
		discard(),
		text("api"),
		text("admin"),
		nothing,
	); !errors.Is(
		err,
		ErrConfig,
	) {
		t.Errorf("Run with a bad listener: %v", err)
	}
}

// TestUnitRunReadyDelay checks that a stop makes readiness fail at once and
// keeps accepting for the ready delay, which a failing listener cuts short.
func TestUnitRunReadyDelay(t *testing.T) {
	c := local()
	c.Shutdown.ReadyDelay = time.Hour
	r, err := Start(t.Context(), c, discard(), text("api"), text("admin"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- r.Wait(ctx, discard(), func() { close(stopped) }) }()
	cancel()
	<-stopped
	if code, body := get(t, "http://"+r.APIAddr()+"/"); code != http.StatusOK || body != "api" {
		t.Errorf("API during the ready delay: %d %q", code, body)
	}
	_ = r.apiLn.Close()
	if err := <-done; err == nil || !strings.Contains(err.Error(), "serve") {
		t.Errorf("got %v, want the listener's error", err)
	}

	c.Shutdown.ReadyDelay = time.Millisecond
	ctx, cancel = context.WithCancel(t.Context())
	cancel()
	if err := Run(ctx, c, discard(), text("api"), text("admin"), nothing); err != nil {
		t.Errorf("Run after a short delay: %v", err)
	}
}

// TestUnitTimeouts checks the bounds on each listener: a client that
// sends its headers and then trickles its body is cut off at the read
// timeout, an idle keep-alive connection is closed at the idle timeout,
// and a handler that outlives the write timeout loses its connection.
func TestUnitTimeouts(t *testing.T) {
	c := local()
	c.Timeouts = Timeouts{
		Header: 200 * time.Millisecond, Read: 300 * time.Millisecond,
		Write: 300 * time.Millisecond, Idle: 300 * time.Millisecond,
	}
	h := http.NewServeMux()
	h.HandleFunc("POST /", func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	h.HandleFunc("GET /", func(http.ResponseWriter, *http.Request) {})
	h.HandleFunc("GET /slow", func(w http.ResponseWriter, _ *http.Request) {
		<-time.After(time.Second)
		_, _ = w.Write([]byte("late"))
	})
	r, err := Start(t.Context(), c, discard(), h, h)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
	for name, addr := range map[string]string{"API": r.APIAddr(), "admin": r.AdminAddr()} {
		t.Run(name, func(t *testing.T) {
			conn := dial(t, addr)
			_, _ = io.WriteString(conn, "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 10\r\n\r\na")
			// The server cuts the request off: a 400 from the handler's
			// failed read, or a closed connection, never the client's own
			// deadline.
			code, err := status(bufio.NewReader(conn))
			if ne, ok := errors.AsType[net.Error](err); ok && ne.Timeout() ||
				err == nil && code != http.StatusBadRequest {
				t.Errorf("a trickled body = %d, %v; want it cut off", code, err)
			}

			conn = dial(t, addr)
			rd := bufio.NewReader(conn)
			_, _ = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
			if code, err := status(rd); err != nil || code != http.StatusOK {
				t.Fatalf("GET = %d, %v", code, err)
			}
			if _, err := rd.ReadByte(); !errors.Is(err, io.EOF) {
				t.Errorf("an idle connection = %v, want closed by the server", err)
			}

			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet,
				"http://"+addr+"/slow", nil)
			if resp, err := http.DefaultClient.Do(req); err == nil {
				body, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				t.Errorf("a handler past the write timeout answered %d %q", resp.StatusCode, body)
			}
		})
	}
}

// status reads one response from rd and closes its body.
func status(rd *bufio.Reader) (int, error) {
	resp, err := http.ReadResponse(rd, nil)
	if err != nil {
		return 0, err //nolint:wrapcheck // the test reads the connection's own error
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

// dial opens a connection to addr that gives up after five seconds, far
// past every timeout under test.
func dial(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	return conn
}
