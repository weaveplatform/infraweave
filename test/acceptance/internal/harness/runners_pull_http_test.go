package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
)

const pullReset = `failed to copy: httpReadSeeker: failed open: failed to do request: Get "https://pkg-containers.githubusercontent.com/ghcrblobs/blobs/sha256:fixture": read tcp 10.0.0.1:1234->185.199.111.154:443: read: connection reset by peer`

// This service injects local Docker daemon-stream faults through the actual
// Moby HTTP client. Released-image acquisition is verified separately on GHCR.
type runnerPullHTTP struct {
	mu              sync.Mutex
	mode            string
	failures        int
	pulls, inspects int
	present         bool
	protocolError   error
	closed          chan struct{}
	fault           func()
}

func runnerPullService(t *testing.T, mode string, failures int) (*Launchers, *runnerPullHTTP) {
	t.Helper()
	p := &runnerPullHTTP{mode: mode, failures: failures, closed: make(chan struct{}, 8)}
	r := chi.NewRouter()
	r.Route("/v1.57", func(r chi.Router) {
		r.Get("/images/*", func(w http.ResponseWriter, r *http.Request) {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.inspects++
			if r.URL.Path != "/v1.57/images/"+RunnerOpenTofu+"/json" {
				p.protocolError = fmt.Errorf("unexpected inspect path: %s", r.URL.Path)
			}
			if p.mode == "inspect failure" {
				cleanupHTTPError(w, http.StatusInternalServerError, "inspect failed")
				return
			}
			if !p.present {
				cleanupHTTPError(w, http.StatusNotFound, "No such image")
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": "sha256:fixture", "RepoDigests": []string{RunnerOpenTofu}})
		})
		r.Post("/images/create", p.pull)
	})
	srv := httptest.NewServer(r)
	transport := &http.Transport{}
	httpClient := srv.Client()
	httpClient.Transport = transport
	cli, err := client.New(client.WithHost(srv.URL), client.WithHTTPClient(httpClient), client.WithScheme("http"), client.WithAPIVersion("1.57"))
	if err != nil {
		srv.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cli.Close()
		transport.CloseIdleConnections()
		srv.Close()
	})
	return &Launchers{cli: &testcontainers.DockerClient{Client: cli}}, p
}

func (p *runnerPullHTTP) pull(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.pulls++
	attempt, mode, failures, fault := p.pulls, p.mode, p.failures, p.fault
	repo, digest, _ := strings.Cut(RunnerOpenTofu, "@")
	if r.URL.Query().Get("fromImage") != repo || r.URL.Query().Get("tag") != digest {
		p.protocolError = fmt.Errorf("pull changed the immutable reference: %s", r.URL.RawQuery)
	}
	failed := attempt <= failures
	if !failed {
		p.present = true
	}
	p.mu.Unlock()
	if !failed {
		_ = json.NewEncoder(w).Encode(jsonstream.Message{Status: "Downloaded pinned image"})
		return
	}
	defer func() { p.closed <- struct{}{} }()
	if fault != nil {
		fault()
	}
	switch mode {
	case "native reset":
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_, err = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 4096\r\n\r\n{\"status\":\"pulling\"}\n{\"status\":")
			if err == nil {
				err = buf.Flush()
			}
			if tcp, ok := conn.(*net.TCPConn); ok && err == nil {
				err = tcp.SetLinger(0)
			}
			_ = conn.Close()
		}
		if err != nil {
			p.mu.Lock()
			p.protocolError = err
			p.mu.Unlock()
		}
	case "blocked request":
		<-r.Context().Done()
	case "unauthorized":
		cleanupHTTPError(w, http.StatusUnauthorized, "registry authentication required")
	case "not found":
		cleanupHTTPError(w, http.StatusNotFound, "manifest unknown")
	default:
		message, code := pullReset, 0
		switch mode {
		case "digest":
			message = "digest verification failed"
		case "manifest":
			message = "no matching manifest for linux/amd64"
		case "permission":
			message = "permission denied"
		case "coded reset":
			code = http.StatusForbidden
		}
		_ = json.NewEncoder(w).Encode(jsonstream.Message{Error: &jsonstream.Error{Code: code, Message: message}})
		w.(http.Flusher).Flush()
		// Wait must close the failed progress response before another attempt.
		<-r.Context().Done()
	}
}

func (p *runnerPullHTTP) check(t *testing.T, pulls, inspects int) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.protocolError != nil || p.pulls != pulls || p.inspects != inspects {
		t.Fatalf("pull protocol: pulls=%d/%d inspects=%d/%d error=%v", p.pulls, pulls, p.inspects, inspects, p.protocolError)
	}
}

func (p *runnerPullHTTP) waitClosed(t *testing.T, n int) {
	t.Helper()
	for range n {
		select {
		case <-p.closed:
		case <-t.Context().Done():
			t.Fatal("failed image response was not closed")
		}
	}
}

func TestUnitRunnerPullRecoversOnlyOwnedTransportResets(t *testing.T) {
	for _, mode := range []string{"stream reset", "native reset"} {
		t.Run(mode, func(t *testing.T) {
			owner, p := runnerPullService(t, mode, 1)
			if err := owner.EnsureRunnerImage(t.Context(), RunnerOpenTofu); err != nil {
				t.Fatal("temporary image acquisition failed", err)
			}
			p.waitClosed(t, 1)
			p.check(t, 2, 2)
			if err := owner.EnsureRunnerImage(t.Context(), RunnerOpenTofu); err != nil {
				t.Fatal(err)
			}
			p.check(t, 2, 2)
			if err := owner.EnsureRunnerImage(t.Context(), "unapproved:fixture"); !errors.Is(err, ErrRunnerImage) {
				t.Fatalf("image catalogue validation was lost: %v", err)
			}
			p.check(t, 2, 2)
		})
	}
}

func TestUnitRunnerPullBoundsFailuresWithoutCachingThem(t *testing.T) {
	for _, mode := range []string{"stream reset", "unauthorized", "not found", "digest", "manifest", "permission", "coded reset", "inspect failure"} {
		t.Run(mode, func(t *testing.T) {
			owner, p := runnerPullService(t, mode, 10)
			if err := owner.EnsureRunnerImage(t.Context(), RunnerOpenTofu); err == nil {
				t.Fatal("failed image acquisition was accepted")
			}
			pulls, inspects := 1, 1
			if mode == "stream reset" {
				pulls, inspects = 3, 3
			}
			if mode == "inspect failure" {
				pulls = 0
			}
			p.check(t, pulls, inspects)
			p.waitClosed(t, pulls)
			p.mu.Lock()
			p.mode, p.failures = "healthy", 0
			p.mu.Unlock()
			if err := owner.EnsureRunnerImage(t.Context(), RunnerOpenTofu); err != nil {
				t.Fatal("failed acquisition was cached", err)
			}
			p.check(t, pulls+1, inspects+1)
		})
	}
}

func TestUnitRunnerPullPreservesCallerCancellation(t *testing.T) {
	for _, mode := range []string{"stream reset", "blocked request"} {
		t.Run(mode, func(t *testing.T) {
			owner, p := runnerPullService(t, mode, 10)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			p.mu.Lock()
			p.fault = cancel
			p.mu.Unlock()
			if err := owner.EnsureRunnerImage(ctx, RunnerOpenTofu); !errors.Is(err, context.Canceled) {
				t.Fatalf("caller cancellation was lost: %v", err)
			}
			p.waitClosed(t, 1)
			p.check(t, 1, 1)
		})
	}
}
