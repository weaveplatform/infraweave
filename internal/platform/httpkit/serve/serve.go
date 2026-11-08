package serve

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

// Config holds the listener settings, server.listen, server.admin_listen
// and server.shutdown.
//
// ref: decisions 0017 and 0020
type Config struct {
	Listen      string   `mapstructure:"listen"`
	AdminListen string   `mapstructure:"admin_listen"`
	Shutdown    Shutdown `mapstructure:"shutdown"`
}

// Shutdown holds the graceful shutdown settings.
type Shutdown struct {
	// ReadyDelay is how long the listeners keep accepting after /readyz
	// starts failing, so load balancers stop routing to the process before
	// it refuses connections.
	ReadyDelay time.Duration `mapstructure:"ready_delay"`
	// Timeout bounds how long in-flight requests may take to finish.
	Timeout time.Duration `mapstructure:"timeout"`
}

// Defaults are the settings an empty configuration means.
func Defaults() Config {
	return Config{
		Listen:      ":8080",
		AdminListen: ":9090",
		Shutdown:    Shutdown{ReadyDelay: 5 * time.Second, Timeout: 30 * time.Second},
	}
}

// ErrConfig marks an invalid setting, so callers that validate more settings
// alongside these report them all alike.
var ErrConfig = errors.New("invalid server configuration")

// Validate rejects a setting the server could not start with, so a typo fails
// before anything listens rather than as a bind error on one listener.
func (c Config) Validate() error {
	var errs []error
	for _, a := range []struct{ key, addr string }{
		{"server.listen", c.Listen},
		{"server.admin_listen", c.AdminListen},
	} {
		if err := checkAddr(a.addr); err != nil {
			errs = append(errs, fmt.Errorf("%w: %s %q: %w", ErrConfig, a.key, a.addr, err))
		}
	}
	if c.Shutdown.ReadyDelay < 0 {
		errs = append(
			errs,
			fmt.Errorf("%w: server.shutdown.ready_delay must not be negative, got %s",
				ErrConfig, c.Shutdown.ReadyDelay),
		)
	}
	if c.Shutdown.Timeout <= 0 {
		errs = append(errs, fmt.Errorf("%w: server.shutdown.timeout must be positive, got %s",
			ErrConfig, c.Shutdown.Timeout))
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

// Running is a process's started listeners: the admin listener and, unless
// the mode serves no API, the API listener.
type Running struct {
	api, admin   *http.Server
	apiLn, admLn net.Listener
	readyDelay   time.Duration
	timeout      time.Duration
	errs         chan error
}

// Start listens on the admin address and, when api is not nil, the API
// address, and serves until Shutdown. A mode that serves no API, such as
// scheduler, passes a nil api and binds no API listener. ctx bounds only
// the binding of the listeners.
func Start(
	ctx context.Context,
	c Config,
	log *slog.Logger,
	api, admin http.Handler,
) (*Running, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	r := &Running{
		readyDelay: c.Shutdown.ReadyDelay,
		timeout:    c.Shutdown.Timeout,
		errs:       make(chan error, 2),
	}
	var lc net.ListenConfig
	if api != nil {
		ln, err := lc.Listen(ctx, "tcp", c.Listen)
		if err != nil {
			return nil, fmt.Errorf("listen on server.listen: %w", err)
		}
		r.api, r.apiLn = newHTTPServer(api, log), ln
	}
	admLn, err := lc.Listen(ctx, "tcp", c.AdminListen)
	if err != nil {
		if r.apiLn != nil {
			_ = r.apiLn.Close()
		}
		return nil, fmt.Errorf("listen on server.admin_listen: %w", err)
	}
	r.admin, r.admLn = newHTTPServer(admin, log), admLn
	attrs := []any{"admin", admLn.Addr().String()}
	if r.api != nil {
		attrs = append([]any{"api", r.apiLn.Addr().String()}, attrs...)
	}
	for _, s := range r.servers() {
		go func() {
			if err := s.srv.Serve(s.ln); !errors.Is(err, http.ErrServerClosed) {
				r.errs <- err
			}
		}()
	}
	log.Info("listening", attrs...)
	return r, nil
}

type served struct {
	srv *http.Server
	ln  net.Listener
}

func (r *Running) servers() []served {
	if r.api == nil {
		return []served{{r.admin, r.admLn}}
	}
	return []served{{r.api, r.apiLn}, {r.admin, r.admLn}}
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

// APIAddr is the address the API listener is bound to, or "" when the mode
// serves no API.
func (r *Running) APIAddr() string {
	if r.apiLn == nil {
		return ""
	}
	return r.apiLn.Addr().String()
}

// AdminAddr is the address the admin listener is bound to.
func (r *Running) AdminAddr() string { return r.admLn.Addr().String() }

// Shutdown stops accepting connections on the listeners and waits for
// in-flight requests, for at most the configured timeout.
func (r *Running) Shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	servers := r.servers()
	errs := make([]error, 0, len(servers))
	for _, s := range servers {
		errs = append(errs, s.srv.Shutdown(ctx))
	}
	return errors.Join(errs...)
}

// Run starts the listeners and serves until ctx is done, then shuts down
// gracefully: it calls stopping, which makes /readyz fail, keeps accepting
// for the ready delay so load balancers stop routing here, then drains
// in-flight requests for at most the timeout. It returns nil after a clean
// shutdown and the error when a listener fails or in-flight requests outlast
// the timeout. A listener that fails skips the ready delay: the process is
// already unreachable on it.
//
// ref: decision 0017
func Run(
	ctx context.Context,
	c Config,
	log *slog.Logger,
	api, admin http.Handler,
	stopping func(),
) error {
	r, err := Start(ctx, c, log, api, admin)
	if err != nil {
		return err
	}
	return r.wait(ctx, log, stopping)
}

func (r *Running) wait(ctx context.Context, log *slog.Logger, stopping func()) error {
	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-r.errs:
	}
	stopping()
	log.Info("shutting down",
		"ready_delay", r.readyDelay.String(), "timeout", r.timeout.String())
	if serveErr == nil && r.readyDelay > 0 {
		delay := time.NewTimer(r.readyDelay)
		select {
		case <-delay.C:
		case serveErr = <-r.errs:
			delay.Stop()
		}
	}
	// The shutdown has its own deadline: ctx is already done.
	err := errors.Join(serveErr, r.Shutdown(context.WithoutCancel(ctx)))
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}
