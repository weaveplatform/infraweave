package rego

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestUnitWorkerCapabilityAndCacheIsolation(t *testing.T) {
	s := Defaults()
	s.Timeouts.Initialization = 60 * time.Second
	worker, err := NewWorker(s)
	if err != nil {
		t.Fatal(err)
	}
	worker.cache = newCache()
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
	for _, engine := range []*Engine{worker, server} {
		bad := p
		bad.Body = "package infraweave\ndeny contains \"bad\" if exec([\"touch\", \"unreachable\"]).status == 0"
		if err := engine.Check(t.Context(), bad); err == nil {
			t.Fatal("exec compiled")
		}
		if _, err := engine.Evaluate(t.Context(), Request{Policy: bad, Input: json.RawMessage(`{}`), Simulation: true}); err == nil {
			t.Fatal("simulation compiled exec")
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
