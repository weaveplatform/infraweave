package vcslab

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// Ingress is the lab's stable webhook-only listener. A tunnel or a local
// provider container reaches it; each sequential scenario attaches its own
// current server without exposing the API, admin listener or prior scenario.
type Ingress struct {
	server   *http.Server
	mu       sync.RWMutex
	target   *url.URL
	listener net.Listener
}

var intakePath = regexp.MustCompile(`^/webhooks/vcs/[a-z][a-z0-9_]*/[A-Za-z0-9_-]+$`)

// StartIngress starts an unbound webhook destination. Until Attach, all calls
// return503. Direct container labs explicitly bind the runner's bridge address;
// hosted labs listen only on loopback behind their quick tunnel.
func StartIngress(ctx context.Context, address string) (*Ingress, error) {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("%w: start lab webhook ingress", ErrHook)
	}
	in := &Ingress{listener: listener}
	proxy := &httputil.ReverseProxy{Rewrite: func(req *httputil.ProxyRequest) {
		in.mu.RLock()
		target := in.target
		in.mu.RUnlock()
		if target != nil {
			req.SetURL(target)
		}
	}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) { w.WriteHeader(http.StatusServiceUnavailable) }}
	r := chi.NewRouter()
	r.Handle("/webhooks/vcs/*", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost || req.URL.RawPath != "" || req.URL.EscapedPath() != req.URL.Path || strings.Contains(req.URL.Path, "%") ||
			!intakePath.MatchString(req.URL.Path) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		in.mu.RLock()
		ready := in.target != nil
		in.mu.RUnlock()
		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		proxy.ServeHTTP(w, req)
	}))
	in.server = &http.Server{Handler: r, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = in.server.Serve(listener) }()
	return in, nil
}

// Address is the actual listener address, useful for a local contract fixture.
func (in *Ingress) Address() string { return in.listener.Addr().String() }

// Attach accepts only a local scenario's API origin. Provider-controlled URLs
// and tunnel URLs are never accepted as upstream destinations.
func (in *Ingress) Attach(origin string) error {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" || u.Port() == "" {
		return fmt.Errorf("%w: invalid local ingress target", ErrHook)
	}
	ip := net.ParseIP(u.Hostname())
	if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("%w: nonlocal ingress target", ErrHook)
	}
	in.mu.Lock()
	in.target = u
	in.mu.Unlock()
	return nil
}

// Close stops public delivery before the scenario's servers and credentials
// disappear. A new scenario must attach its own destination explicitly.
func (in *Ingress) Close(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := in.server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("%w: stop lab ingress", ErrHook)
	}
	return nil
}
