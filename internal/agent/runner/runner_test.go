package runner_test

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/agenttest"
	"github.com/weaveplatform/infraweave/internal/agent/client"
	"github.com/weaveplatform/infraweave/internal/agent/handoff"
	"github.com/weaveplatform/infraweave/internal/agent/runner"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// assigned is a run the server assigned, and its handoff.
func assigned(t *testing.T) (*agenttest.Server, []byte) {
	t.Helper()
	pool, err := agenttest.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	srv := agenttest.NewServer(pool)
	a := wire.Assignment{
		Run:          "run-1",
		RunToken:     "token",
		State:        wire.AssignmentStatePREPARINGAPPLY,
		StateVersion: 5,
	}
	if err := srv.Assign(agenttest.Run{Assignment: a}); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := handoff.Write(&b, handoff.Handoff{
		Endpoint: client.Base(agenttest.Endpoint), Run: a.Run, RunToken: a.RunToken,
		Assigned: a.State, StateVersion: a.StateVersion,
	}); err != nil {
		t.Fatal(err)
	}
	return srv, b.Bytes()
}

// TestUnitRunnerExecutesTheRun checks that the runner executes the run
// its handoff names through the worker API: a specification that names
// no tool fails the first phase with a note that says so.
func TestUnitRunnerExecutesTheRun(t *testing.T) {
	srv, h := assigned(t)
	var log bytes.Buffer
	if err := runner.Run(
		t.Context(),
		bytes.NewReader(h),
		fixed(srv.Client()),
		slog.New(slog.NewTextHandler(&log, nil)),
		nil,
	); err != nil {
		t.Fatal(err)
	}
	reps := srv.Reports()
	if len(reps) != 2 {
		t.Fatalf("reports: %+v", reps)
	}
	started, failed := reps[0].Report, reps[1].Report
	if reps[0].Token != "token" || started.Event != wire.Started ||
		started.State != wire.WorkerStateAPPLYING ||
		started.StateVersion != 5 {
		t.Fatalf("started: %+v", started)
	}
	if failed.Event != wire.Failed || failed.State != wire.WorkerStateAPPLYING ||
		failed.StateVersion != 6 ||
		*failed.Details != "The run's specification names no tool to run." {
		t.Fatalf("failed: %+v", failed)
	}
	if !strings.Contains(log.String(), `msg="run ended" run=run-1 state=FAILED`) {
		t.Fatalf("log: %s", log.String())
	}
}

// TestUnitRunnerFailures checks a handoff the runner cannot read, and a
// report the server refuses, which fails the runner.
func TestUnitRunnerFailures(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	if err := runner.Run(
		t.Context(),
		strings.NewReader("{"),
		fixed(nil),
		log,
		nil,
	); !errors.Is(
		err,
		handoff.ErrHandoff,
	) {
		t.Fatalf("a bad handoff: %v", err)
	}
	for n := range 2 {
		srv, h := assigned(t)
		d := &failAfter{n: n, next: srv.Client()}
		if err := runner.Run(t.Context(), bytes.NewReader(h), fixed(d), log, nil); err == nil {
			t.Fatalf("report %d failed, and Run succeeded", n+1)
		}
	}
}

// failAfter passes n requests to next and refuses the rest with 400.
type failAfter struct {
	n    int
	next wire.HttpRequestDoer
}

func (f *failAfter) Do(r *http.Request) (*http.Response, error) {
	if f.n == 0 {
		return &http.Response{
			StatusCode: http.StatusBadRequest, Body: http.NoBody, Request: r,
			Header: http.Header{"Content-Type": {"text/plain"}},
		}, nil
	}
	f.n--
	return f.next.Do(r) //nolint:wrapcheck // a test double
}

// fixed makes d whatever the roots.
func fixed(d wire.HttpRequestDoer) func(*x509.CertPool) wire.HttpRequestDoer {
	return func(*x509.CertPool) wire.HttpRequestDoer { return d }
}

// TestUnitRunnerTrustsTheHandoffRoots checks that the runner's client
// trusts the handoff's roots, and that roots holding no certificate fail
// the runner before it calls the server.
func TestUnitRunnerTrustsTheHandoffRoots(t *testing.T) {
	tlsSrv := httptest.NewTLSServer(http.NotFoundHandler())
	defer tlsSrv.Close()
	bundle := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tlsSrv.Certificate().Raw})
	handoffWith := func(roots []byte) []byte {
		var b bytes.Buffer
		if err := handoff.Write(&b, handoff.Handoff{
			Endpoint: client.Base(agenttest.Endpoint), Run: "run-1", RunToken: "token",
			Assigned: wire.AssignmentStatePREPARINGAPPLY, StateVersion: 5, Roots: roots,
		}); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	var got *x509.CertPool
	doer := func(roots *x509.CertPool) wire.HttpRequestDoer {
		got = roots
		return &failAfter{}
	}
	log := slog.New(slog.DiscardHandler)
	_ = runner.Run(t.Context(), bytes.NewReader(handoffWith(bundle)), doer, log, nil)
	if got == nil {
		t.Fatal("the runner's client trusts the system's roots alone")
	}
	if _, err := tlsSrv.Certificate().Verify(x509.VerifyOptions{Roots: got}); err != nil {
		t.Fatalf("the handoff's root is not trusted: %v", err)
	}
	got = x509.NewCertPool()
	_ = runner.Run(t.Context(), bytes.NewReader(handoffWith(nil)), doer, log, nil)
	if got != nil {
		t.Fatal("a handoff without roots changed the system's")
	}
	err := runner.Run(t.Context(), bytes.NewReader(handoffWith([]byte("not PEM"))), doer, log, nil)
	if !errors.Is(err, runner.ErrRoots) {
		t.Fatalf("roots without a certificate: %v", err)
	}
}

// TestUnitClientTrustsItsRoots checks the runner's HTTP client against a
// server a private authority certifies: trusted with its roots, refused
// with the system's.
func TestUnitClientTrustsItsRoots(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	get := func(d wire.HttpRequestDoer) error {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := d.Do(req)
		if err == nil {
			resp.Body.Close()
		}
		return err //nolint:wrapcheck // a test's own
	}
	if err := get(runner.Client(time.Second)(roots)); err != nil {
		t.Fatalf("with its roots: %v", err)
	}
	if err := get(runner.Client(time.Second)(nil)); err == nil {
		t.Fatal("the system's roots trusted a private authority")
	}
}
