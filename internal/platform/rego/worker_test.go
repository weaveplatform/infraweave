package rego

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

const execPolicy = `package infraweave
import future.keywords
out := exec(["verify", input.token])
deny contains "unverified" if { out.status != 0 }
deny contains out.stdout if { out.stderr == "say" }
`

// commands records each call and answers a fixed result or error.
type commands struct {
	mu     sync.Mutex
	calls  [][]string
	result Command
	err    error
}

func (c *commands) Exec(_ context.Context, argv []string) (Command, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, argv)
	return c.result, c.err
}

func workerEngine(t *testing.T) *Engine {
	t.Helper()
	s := Defaults()
	s.Timeouts.Initialization = 60 * time.Second
	worker, err := NewWorker(s)
	if err != nil {
		t.Fatal(err)
	}
	worker.cache = newCache()
	worker.slots = make(chan struct{}, Concurrency)
	return worker
}

func denials(t *testing.T, r Result) []string {
	t.Helper()
	var d struct {
		Deny []string `json:"deny"`
	}
	if err := json.Unmarshal(r.Decision, &d); err != nil {
		t.Fatal(err)
	}
	return d.Deny
}

func TestUnitWorkerCapabilityAndCacheIsolation(t *testing.T) {
	worker := workerEngine(t)
	server := testEngine(t)
	server.cache = worker.cache
	p := Policy{Type: Initialization, Engine: V1, Body: "package infraweave"}
	if err := worker.Check(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if err := server.Check(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if len(worker.cache.entries) != 2 || worker.settings.Timeouts.Initialization != 60*time.Second || server.settings.Timeouts.Initialization != 5*time.Second {
		t.Fatal("worker and server cache or timeout aliased")
	}
	exec := p
	exec.Body = execPolicy
	// The worker compiles exec first, so the shared cache holds its
	// query; the server must still refuse the same body.
	if err := worker.Check(t.Context(), exec); err != nil {
		t.Fatalf("the worker refused exec: %v", err)
	}
	if err := server.Check(t.Context(), exec); err == nil {
		t.Fatal("the server compiled exec after the worker cached it")
	}
	for _, simulation := range []bool{false, true} {
		if _, err := server.Evaluate(t.Context(), Request{Policy: exec, Input: json.RawMessage(`{}`), Simulation: simulation}); err == nil {
			t.Fatal("a server evaluation compiled exec")
		}
	}
	if _, err := server.Evaluate(t.Context(), Request{Policy: p, Input: json.RawMessage(`{}`), Commands: &commands{}}); !errors.Is(err, ErrPolicy) {
		t.Fatalf("a server evaluation took commands: %v", err)
	}
	for _, builtin := range capabilities().Builtins {
		if builtin.Name == commandName {
			t.Fatal("the server capabilities hold exec")
		}
	}
	p.Type = Plan
	if err := worker.Check(t.Context(), p); !errors.Is(err, ErrPolicy) {
		t.Fatal(err)
	}
	invalid := Defaults()
	invalid.MaxInputBytes = 0
	if _, err := NewWorker(invalid); !errors.Is(err, ErrSettings) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := worker.Evaluate(
		ctx,
		Request{Policy: Policy{Type: Initialization, Engine: V1, Body: "package infraweave"}, Input: json.RawMessage(`{}`)},
	); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatal(err)
	}
}

func TestUnitWorkerExecReachesOnlyItsEvaluationsCommands(t *testing.T) {
	worker := workerEngine(t)
	p := Policy{Type: Initialization, Engine: V1, Body: execPolicy}
	input := json.RawMessage(`{"token":"t0k3n"}`)

	allowed := &commands{result: Command{Status: 0, Stdout: "fine", Stderr: ""}}
	r, err := worker.Evaluate(t.Context(), Request{Policy: p, Input: input, Commands: allowed})
	if err != nil || len(denials(t, r)) != 0 {
		t.Fatalf("an exiting 0 verifier: %s, %v", r.Decision, err)
	}
	if len(allowed.calls) != 1 || !slices.Equal(allowed.calls[0], []string{"verify", "t0k3n"}) {
		t.Fatalf("calls = %q", allowed.calls)
	}

	denied := &commands{result: Command{Status: 3, Stdout: "said", Stderr: "say"}}
	r, err = worker.Evaluate(t.Context(), Request{Policy: p, Input: input, Commands: denied})
	if err != nil || !slices.Equal(denials(t, r), []string{"said", "unverified"}) {
		t.Fatalf("a failing verifier: %s, %v", r.Decision, err)
	}

	// The prepared query is cached now; an evaluation without commands
	// must not reach either earlier evaluation's.
	_, err = worker.Evaluate(t.Context(), Request{Policy: p, Input: input})
	var failed *CommandError
	if !errors.As(err, &failed) || !errors.Is(err, ErrNoCommands) || failed.Error() != ErrNoCommands.Error() {
		t.Fatalf("an evaluation without commands: %v", err)
	}
	if len(allowed.calls) != 1 || len(denied.calls) != 1 {
		t.Fatal("a cached query reached another evaluation's commands")
	}

	refused := errors.New("helper refused")
	_, err = worker.Evaluate(t.Context(), Request{Policy: p, Input: input, Commands: &commands{err: refused}})
	if !errors.As(err, &failed) || !errors.Is(err, refused) {
		t.Fatalf("a failed call: %v", err)
	}

	words := Policy{Type: Initialization, Engine: V1, Body: "package infraweave\ndeny contains \"x\" if exec(input.argv).status == 0"}
	for _, argv := range []string{`[1]`, `"verify"`} {
		_, err := worker.Evaluate(t.Context(), Request{Policy: words, Input: json.RawMessage(`{"argv":` + argv + `}`), Commands: &commands{}})
		if !errors.Is(err, ErrInput) || !errors.As(err, &failed) {
			t.Fatalf("exec(%s) = %v", argv, err)
		}
	}
	typed := Policy{Type: Initialization, Engine: V1, Body: "package infraweave\ndeny contains \"x\" if exec(\"verify\").status == 0"}
	if err := worker.Check(t.Context(), typed); err == nil {
		t.Fatal("exec compiled with a string argument")
	}
}

// TestUnitWorkerAndServerEvaluationsInterleave mixes worker evaluations
// that call exec with server evaluations of the same body in one process,
// sharing the process cache, so a capability or cache leak would let a
// server evaluation compile exec.
func TestUnitWorkerAndServerEvaluationsInterleave(t *testing.T) {
	s := Defaults()
	s.Timeouts.Initialization = 60 * time.Second
	worker, err := NewWorker(s)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(Defaults())
	if err != nil {
		t.Fatal(err)
	}
	p := Policy{Type: Initialization, Engine: V0, Body: "package infraweave\nimport future.keywords\nout := exec([\"verify\"])\ndeny contains \"no\" if { out.status != 0 }"}
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			if i%2 == 0 {
				c := &commands{result: Command{Status: i}}
				r, err := worker.Evaluate(context.Background(), Request{Policy: p, Input: json.RawMessage(`{}`), Commands: c})
				var d struct {
					Deny []string `json:"deny"`
				}
				if err != nil || json.Unmarshal(r.Decision, &d) != nil || (i != 0) != (len(d.Deny) == 1) || len(c.calls) != 1 {
					t.Errorf("worker evaluation %d: %s, %v", i, r.Decision, err)
				}
				return
			}
			if _, err := server.Evaluate(context.Background(), Request{Policy: p, Input: json.RawMessage(`{}`)}); err == nil {
				t.Errorf("server evaluation %d compiled exec", i)
			}
		})
	}
	wg.Wait()
}
