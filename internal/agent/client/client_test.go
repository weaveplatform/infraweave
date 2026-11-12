package client_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/agent/agenttest"
	"github.com/weaveplatform/infraweave/internal/agent/client"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// doer answers every request with fn.
type doer func(*http.Request) (*http.Response, error)

func (d doer) Do(r *http.Request) (*http.Response, error) { return d(r) }

func TestUnitClientBase(t *testing.T) {
	for _, public := range []string{"https://iw.example.com", "https://iw.example.com/"} {
		if got := client.Base(public); got != "https://iw.example.com/worker/v1" {
			t.Errorf("Base(%q) = %q", public, got)
		}
	}
	c := client.New("http://[::1", http.DefaultClient)
	if err := c.Deregister(t.Context(), "s"); err == nil || client.Status(err) != 0 {
		t.Fatalf("a call to a base that is no URL: %v", err)
	}
}

func TestUnitClientCalls(t *testing.T) {
	pool, err := agenttest.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	srv := agenttest.NewServer(pool)
	c := client.New(client.Base(agenttest.Endpoint), srv.Client())
	ctx := t.Context()
	run := agenttest.Run{Assignment: wire.Assignment{
		Run: "run-1", RunToken: "token", State: wire.AssignmentStatePREPARING, StateVersion: 2,
	}}
	if err := srv.Assign(run); err != nil {
		t.Fatal(err)
	}
	spec, err := c.Spec(ctx, "token", "run-1")
	if err != nil || len(spec.Sealed) == 0 {
		t.Fatalf("Spec: %+v, %v", spec, err)
	}
	step, err := c.Report(ctx, "token", "run-1", wire.RunReport{
		StateVersion: 2, State: wire.WorkerStateINITIALIZING, Event: wire.Started,
	})
	if err != nil || step.Action != wire.Execute || step.StateVersion != 3 {
		t.Fatalf("Report: %+v, %v", step, err)
	}

	for op, call := range map[string]func() error{
		"register":   func() error { _, err := c.Register(ctx, "not a JWS"); return err },
		"poll":       func() error { _, err := c.Poll(ctx, "nobody", wire.PollRequest{}); return err },
		"deregister": func() error { return c.Deregister(ctx, "nobody") },
		"fetch the run's specification": func() error {
			_, err := c.Spec(ctx, "wrong", "run-1")
			return err
		},
		"report INITIALIZING started": func() error {
			_, err := c.Report(ctx, "wrong", "run-1", wire.RunReport{
				State: wire.WorkerStateINITIALIZING, Event: wire.Started,
			})
			return err
		},
	} {
		err := call()
		var e *client.Error
		if !errors.As(err, &e) || e.Op != op || e.Status != http.StatusUnauthorized ||
			e.Detail == "" ||
			client.Status(err) != http.StatusUnauthorized ||
			!strings.HasPrefix(err.Error(), op+": 401 ") {
			t.Errorf("%s: %v", op, err)
		}
	}
}

func TestUnitClientFailures(t *testing.T) {
	ctx := t.Context()
	down := client.New(
		"http://iw.invalid/worker/v1",
		doer(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("unreachable")
		}),
	)
	plain := client.New(
		"http://iw.invalid/worker/v1",
		doer(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Header:     http.Header{"Content-Type": {"text/html"}},
				Body:       http.NoBody,
				Request:    r,
			}, nil
		}),
	)
	for _, c := range []*client.Client{down, plain} {
		for name, err := range map[string]error{
			"register":   func() error { _, err := c.Register(ctx, "jws"); return err }(),
			"poll":       func() error { _, err := c.Poll(ctx, "s", wire.PollRequest{}); return err }(),
			"deregister": c.Deregister(ctx, "s"),
			"spec":       func() error { _, err := c.Spec(ctx, "t", "r"); return err }(),
			"report":     func() error { _, err := c.Report(ctx, "t", "r", wire.RunReport{}); return err }(),
		} {
			if err == nil {
				t.Errorf("%s succeeded", name)
			}
		}
	}
	err := plain.Deregister(ctx, "s")
	if client.Status(err) != http.StatusBadGateway || err.Error() != "deregister: 502 Bad Gateway" {
		t.Fatalf("a refusal without a problem: %v", err)
	}
	if client.Status(errors.New("other")) != 0 {
		t.Fatal("Status of an error that is no refusal")
	}
}
