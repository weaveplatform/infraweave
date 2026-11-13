package runner_test

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"

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
		srv.Client(),
		slog.New(slog.NewTextHandler(&log, nil)),
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
// report that keeps failing, which the runner sends again for ten minutes
// of the bubble's time before it fails.
func TestUnitRunnerFailures(t *testing.T) {
	synctest.Test(t, runnerFailures)
}

func runnerFailures(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	if err := runner.Run(
		t.Context(),
		strings.NewReader("{"),
		nil,
		log,
	); !errors.Is(
		err,
		handoff.ErrHandoff,
	) {
		t.Fatalf("a bad handoff: %v", err)
	}
	var b bytes.Buffer
	if err := handoff.Write(&b, handoff.Handoff{
		Endpoint: "://", Run: "r", RunToken: "t", Assigned: wire.AssignmentStatePREPARING,
	}); err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(t.Context(), &b, nil, log); err == nil {
		t.Fatal("a handoff whose endpoint is no URL")
	}
	for n := range 2 {
		srv, h := assigned(t)
		d := &failAfter{n: n, next: srv.Client()}
		if err := runner.Run(t.Context(), bytes.NewReader(h), d, log); err == nil {
			t.Fatalf("report %d failed, and Run succeeded", n+1)
		}
	}
}

// failAfter passes n requests to next and fails the rest.
type failAfter struct {
	n    int
	next wire.HttpRequestDoer
}

func (f *failAfter) Do(r *http.Request) (*http.Response, error) {
	if f.n == 0 {
		return nil, errors.New("unreachable")
	}
	f.n--
	return f.next.Do(r) //nolint:wrapcheck // a test double
}
