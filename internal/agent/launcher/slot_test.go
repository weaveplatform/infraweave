package launcher_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/weaveplatform/infraweave/internal/agent/agenttest"
	"github.com/weaveplatform/infraweave/internal/agent/client"
	"github.com/weaveplatform/infraweave/internal/agent/handoff"
	"github.com/weaveplatform/infraweave/internal/agent/launcher"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// hosts records the hosts a client's requests go to.
type hosts struct {
	next http.RoundTripper
	mu   sync.Mutex
	seen map[string]bool
}

func (h *hosts) RoundTrip(r *http.Request) (*http.Response, error) {
	h.mu.Lock()
	h.seen[r.URL.Host] = true
	h.mu.Unlock()
	return h.next.RoundTrip(r)
}

// slot is a launcher of a worker controller's slot: it calls the worker
// API at its own endpoint and keeps its worker registered.
func (w *world) slot(t *testing.T, seen *hosts) *launcher.Launcher {
	t.Helper()
	slot := int32(2)
	l, err := launcher.New(launcher.Deps{
		Config: w.config(), Doer: &http.Client{Transport: seen}, Executor: w.exec, Sources: w.sources,
		Tools: w.tools, Log: slog.New(slog.NewTextHandler(w.log, nil)),
		Version: "1.2.3", OS: "linux", Arch: "arm64",
		Endpoint: "http://controller.invalid",
		Owner:    &kernel.WorkerOwner{ClusterID: "lab", Namespace: "workers", ResourceUID: "uid"},
		Slot:     &slot, Retain: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestUnitLauncherServesAControllerSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		seen := &hosts{next: w.srv.Client().Transport, seen: map[string]bool{}}
		l := w.slot(t, seen)
		if s := l.Status(); s.Worker != "" || s.Run != "" {
			t.Fatalf("status before registering: %+v", s)
		}
		done := make(chan error, 1)
		go func() { done <- l.Run(t.Context()) }()
		synctest.Wait()
		c := w.srv.Registrations()[0].Claims
		owner, _ := c["owner"].(map[string]any)
		if c["slot"] != float64(2) || owner["resourceUID"] != "uid" {
			t.Fatalf("claims: %v", c)
		}
		if s := l.Status(); s.Worker != "worker-1" || s.Run != "" {
			t.Fatalf("status once registered: %+v", s)
		}

		w.assign(t, "run-1", wire.AssignmentStatePREPARING, "")
		s := l.Status()
		if s.Run != "run-1" || s.State != wire.AssignmentStatePREPARING || s.StateVersion != 4 ||
			s.Assigned.IsZero() || s.Started.IsZero() || s.Exit != nil {
			t.Fatalf("status of a run: %+v", s)
		}
		h, err := handoff.Read(bytes.NewReader(w.exec.Jobs()[0].Handoff))
		if err != nil || h.Endpoint != client.Base(agenttest.Endpoint) {
			t.Fatalf("the runner reaches the token's endpoint: %+v %v", h.Endpoint, err)
		}
		if !seen.seen["controller.invalid"] || seen.seen["agenttest.invalid"] {
			t.Fatalf("hosts: %v", seen.seen)
		}

		l.Retire()
		w.exec.Container("worker-1").End(ended(0, wire.Exited))
		synctest.Wait()
		if s := l.Status(); s.Exit == nil || s.Exit.Reason != wire.Exited || s.Run != "run-1" {
			t.Fatalf("status of an ended run: %+v", s)
		}
		w.answer(wire.PollResult{})
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if s := l.Status(); s.Run != "" || s.Exit != nil || s.State != "" {
			t.Fatalf("status once reported: %+v", s)
		}
		if got := w.srv.Deregistered(); len(got) != 0 {
			t.Fatalf("a slot deregistered itself: %v", got)
		}
		if _, err := os.Stat(filepath.Join(w.dir, "worker.json")); err != nil {
			t.Fatalf("a slot forgot its worker: %v", err)
		}
		if err := l.Registered(t.Context()); err == nil {
			t.Fatal("an ended slot is registered")
		}
	})
}
