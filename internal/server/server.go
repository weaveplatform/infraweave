// Package server serves infraweave's HTTP listeners: the API listener, which
// carries the deploy API under /api/v1, and the admin listener, which carries
// the probes (decision 0017). Every error either listener writes is a problem
// detail, including the router's own 404 and 405.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Config holds the server settings. The keys are decision 0020's and 0017's:
// server.listen, server.admin_listen and server.shutdown.timeout.
type Config struct {
	Listen      string   `mapstructure:"listen"`
	AdminListen string   `mapstructure:"admin_listen"`
	Shutdown    Shutdown `mapstructure:"shutdown"`
}

// Shutdown holds the graceful shutdown settings.
type Shutdown struct {
	// Timeout bounds how long in-flight requests may take to finish.
	Timeout time.Duration `mapstructure:"timeout"`
}

// Defaults are the settings an empty configuration means.
func Defaults() Config {
	return Config{
		Listen:      ":8080",
		AdminListen: ":9090",
		Shutdown:    Shutdown{Timeout: 30 * time.Second},
	}
}

var errConfig = errors.New("invalid server configuration")

// Validate rejects a setting the server could not start with, so a typo fails
// before anything listens rather than as a bind error on one listener.
func (c Config) Validate() error {
	var errs []error
	for _, a := range []struct{ key, addr string }{
		{"server.listen", c.Listen},
		{"server.admin_listen", c.AdminListen},
	} {
		if err := checkAddr(a.addr); err != nil {
			errs = append(errs, fmt.Errorf("%w: %s %q: %w", errConfig, a.key, a.addr, err))
		}
	}
	if c.Shutdown.Timeout <= 0 {
		errs = append(errs, fmt.Errorf("%w: server.shutdown.timeout must be positive, got %s",
			errConfig, c.Shutdown.Timeout))
	}
	return errors.Join(errs...)
}

var errPort = errors.New("port must be a number from 0 to 65535")

func checkAddr(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("want host:port: %w", err)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return errPort
	}
	return nil
}

// Running is a started server.
type Running struct {
	api, admin   *http.Server
	apiLn, admLn net.Listener
	timeout      time.Duration
	errs         chan error
}

// Start listens on both addresses and serves until Shutdown. ctx bounds
// only the binding of the listeners.
func Start(ctx context.Context, c Config, log *slog.Logger) (*Running, error) {
	return start(ctx, c, log, API(log), Admin(log))
}

func start(
	ctx context.Context,
	c Config,
	log *slog.Logger,
	api, admin http.Handler,
) (*Running, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	var lc net.ListenConfig
	apiLn, err := lc.Listen(ctx, "tcp", c.Listen)
	if err != nil {
		return nil, fmt.Errorf("listen on server.listen: %w", err)
	}
	admLn, err := lc.Listen(ctx, "tcp", c.AdminListen)
	if err != nil {
		_ = apiLn.Close()
		return nil, fmt.Errorf("listen on server.admin_listen: %w", err)
	}
	r := &Running{
		api:     newHTTPServer(api, log),
		admin:   newHTTPServer(admin, log),
		apiLn:   apiLn,
		admLn:   admLn,
		timeout: c.Shutdown.Timeout,
		errs:    make(chan error, 2),
	}
	for _, s := range []struct {
		srv *http.Server
		ln  net.Listener
	}{{r.api, apiLn}, {r.admin, admLn}} {
		go func() {
			if err := s.srv.Serve(s.ln); !errors.Is(err, http.ErrServerClosed) {
				r.errs <- err
			}
		}()
	}
	log.Info("listening", "api", apiLn.Addr().String(), "admin", admLn.Addr().String())
	return r, nil
}

// newHTTPServer bounds the time a client may take to send its headers, so
// a slow client cannot hold a connection open indefinitely.
func newHTTPServer(h http.Handler, log *slog.Logger) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelError),
	}
}

// APIAddr is the address the API listener is bound to.
func (r *Running) APIAddr() string { return r.apiLn.Addr().String() }

// AdminAddr is the address the admin listener is bound to.
func (r *Running) AdminAddr() string { return r.admLn.Addr().String() }

// Shutdown stops accepting connections on both listeners and waits for
// in-flight requests, for at most the configured timeout.
func (r *Running) Shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	return errors.Join(r.api.Shutdown(ctx), r.admin.Shutdown(ctx))
}

// Run starts the server and serves until ctx is done, then shuts down
// gracefully. It returns nil after a clean shutdown and the error when a
// listener fails or in-flight requests outlast the timeout.
func Run(ctx context.Context, c Config, log *slog.Logger) error {
	r, err := Start(ctx, c, log)
	if err != nil {
		return err
	}
	return r.wait(ctx, log)
}

func (r *Running) wait(ctx context.Context, log *slog.Logger) error {
	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-r.errs:
	}
	log.Info("shutting down", "timeout", r.timeout.String())
	// The shutdown has its own deadline: ctx is already done.
	err := errors.Join(serveErr, r.Shutdown(context.WithoutCancel(ctx)))
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}
