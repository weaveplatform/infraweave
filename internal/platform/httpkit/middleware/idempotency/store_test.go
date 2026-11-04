package idempotency

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// clock is a settable time source, so expiry is tested without sleeping.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestUnitMemoryStateMachine(t *testing.T) {
	ctx := t.Context()
	c := newClock()
	m := NewMemory(time.Hour, c.now)
	k := Key{Caller: "a", Value: "k1"}

	if _, found, _ := m.Get(ctx, k); found {
		t.Fatal("a new store has the key")
	}
	if err := m.Complete(ctx, k, Response{Status: 201}); !errors.Is(err, ErrNotReserved) {
		t.Errorf("Complete before Reserve: %v", err)
	}
	if ok, _ := m.Reserve(ctx, k, "fp"); !ok {
		t.Fatal("first Reserve failed")
	}
	if ok, _ := m.Reserve(ctx, k, "fp"); ok {
		t.Error("a held key was reserved twice")
	}
	if ok, _ := m.Reserve(ctx, Key{Caller: "b", Value: "k1"}, "fp"); !ok {
		t.Error("another caller's key collided")
	}
	rec, found, _ := m.Get(ctx, k)
	if !found || rec.Fingerprint != "fp" || rec.Response != nil {
		t.Errorf("in flight: %+v %v", rec, found)
	}

	// Completing restarts the lifetime.
	c.advance(50 * time.Minute)
	if err := m.Complete(ctx, k, Response{Status: 201, Body: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	c.advance(50 * time.Minute)
	rec, found, _ = m.Get(ctx, k)
	if !found || rec.Response == nil || rec.Response.Status != 201 {
		t.Errorf("completed: %+v %v", rec, found)
	}
	c.advance(10 * time.Minute)
	if _, found, _ := m.Get(ctx, k); found {
		t.Error("the key outlived its ttl")
	}
	if ok, _ := m.Reserve(ctx, k, "fp2"); !ok {
		t.Error("an expired key could not be reserved again")
	}

	if err := m.Release(ctx, k); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := m.Get(ctx, k); found {
		t.Error("a released key is still held")
	}
}

func TestUnitMemorySweeps(t *testing.T) {
	ctx := t.Context()
	c := newClock()
	m := NewMemory(time.Second, c.now)
	_, _ = m.Reserve(ctx, Key{Value: "old"}, "fp")
	c.advance(sweepEvery)
	_, _ = m.Reserve(ctx, Key{Value: "new"}, "fp")
	if _, kept := m.records[Key{Value: "old"}]; kept {
		t.Error("an expired key survived the sweep")
	}
	if _, kept := m.records[Key{Value: "new"}]; !kept {
		t.Error("a live key was swept")
	}
}

// TestUnitMemoryConcurrentReserve races many reservations of one key: the
// atomic Reserve is what stops two retries from both running the operation.
func TestUnitMemoryConcurrentReserve(t *testing.T) {
	m := NewMemory(time.Hour, time.Now)
	k := Key{Caller: "a", Value: "k"}
	var (
		wg   sync.WaitGroup
		won  atomic.Int32
		errs atomic.Int32
	)
	start := make(chan struct{})
	for range 200 {
		wg.Go(func() {
			<-start
			ok, err := m.Reserve(context.Background(), k, "fp")
			if err != nil {
				errs.Add(1)
			}
			if ok {
				won.Add(1)
			}
		})
	}
	close(start)
	wg.Wait()
	if won.Load() != 1 || errs.Load() != 0 {
		t.Errorf(
			"%d reservations won and %d failed, want exactly one winner",
			won.Load(),
			errs.Load(),
		)
	}
}

func TestUnitValidate(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Config{}).Validate(); !errors.Is(err, errTTL) {
		t.Errorf("zero ttl: %v", err)
	}
}
