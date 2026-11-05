package idempotency

import (
	"bytes"
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

// deadline is one of a key's two: its request's lock or its TTL.
type deadline int

const (
	lockLapses deadline = iota
	ttlLapses
)

// contract is what every Store does, run against store; lapse moves key
// past one of its deadlines without waiting for it.
func contract(t *testing.T, store Store, lapse func(key Key, d deadline)) {
	ctx := t.Context()
	k := Key{Caller: "a", Value: "k1"}
	claim := func(key Key, fp string) (Record, bool) {
		t.Helper()
		rec, ok, err := store.Claim(ctx, key, fp)
		if err != nil {
			t.Fatalf("Claim %v: %v", key, err)
		}
		return rec, ok
	}

	if err := store.Complete(ctx, k, Response{Status: 201}); !errors.Is(err, ErrNotReserved) {
		t.Errorf("Complete before Claim: %v", err)
	}
	if _, ok := claim(k, "fp"); !ok {
		t.Fatal("the first Claim lost")
	}
	if rec, ok := claim(k, "fp2"); ok || rec.Fingerprint != "fp" || rec.Response != nil {
		t.Errorf("a held key: %+v %v, want the first request's, in flight", rec, ok)
	}
	if _, ok := claim(Key{Caller: "b", Value: "k1"}, "fp"); !ok {
		t.Error("another caller's key collided")
	}

	want := Response{
		Status:      201,
		ContentType: "application/json",
		Location:    "/things/1",
		Body:        []byte(`{"id":1}`),
	}
	if err := store.Complete(ctx, k, want); err != nil {
		t.Fatal(err)
	}
	if err := store.Complete(ctx, k, want); !errors.Is(err, ErrNotReserved) {
		t.Errorf("a second Complete: %v", err)
	}
	if err := store.Release(ctx, k); err != nil {
		t.Fatal(err)
	}
	rec, ok := claim(k, "fp2")
	if ok || rec.Fingerprint != "fp" || rec.Response == nil {
		t.Fatalf("a completed key after Release: %+v %v", rec, ok)
	}
	if got := *rec.Response; got.Status != want.Status || got.ContentType != want.ContentType ||
		got.Location != want.Location || !bytes.Equal(got.Body, want.Body) {
		t.Errorf("stored response %+v, want %+v", got, want)
	}
	lapse(k, lockLapses)
	if _, ok := claim(k, "fp2"); ok {
		t.Error("a completed key was taken over when its lock lapsed")
	}
	lapse(k, ttlLapses)
	if _, ok := claim(k, "fp3"); !ok {
		t.Error("an expired key could not be claimed again")
	}

	// A crashed replica's key is taken over once its lock lapses.
	lapse(k, lockLapses)
	if _, ok := claim(k, "fp4"); !ok {
		t.Error("a key whose lock lapsed was not taken over")
	}
	if rec, ok := claim(k, "fp5"); ok || rec.Fingerprint != "fp4" {
		t.Errorf("after the takeover: %+v %v", rec, ok)
	}
	if err := store.Release(ctx, k); err != nil {
		t.Fatal(err)
	}
	if _, ok := claim(k, "fp6"); !ok {
		t.Error("a released key is still held")
	}

	// An empty response replays empty.
	if err := store.Complete(ctx, k, Response{Status: 204}); err != nil {
		t.Fatal(err)
	}
	if rec, _ := claim(k, "fp6"); rec.Response == nil || rec.Response.Status != 204 ||
		rec.Response.ContentType != "" || len(rec.Response.Body) != 0 {
		t.Errorf("an empty response: %+v", rec.Response)
	}

	// Of many requests claiming one key at once, exactly one wins.
	var (
		wg   sync.WaitGroup
		won  atomic.Int32
		errs atomic.Int32
	)
	start := make(chan struct{})
	for range 50 {
		wg.Go(func() {
			<-start
			_, ok, err := store.Claim(context.Background(), Key{Caller: "c", Value: "race"}, "fp")
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
		t.Errorf("%d claims won and %d failed, want exactly one winner", won.Load(), errs.Load())
	}
}

func TestUnitMemoryContract(t *testing.T) {
	c := newClock()
	cfg := Config{TTL: time.Hour, Lock: time.Minute}
	contract(t, NewMemory(cfg, c.now), func(_ Key, d deadline) {
		if d == lockLapses {
			c.advance(cfg.Lock)
		} else {
			c.advance(cfg.TTL)
		}
	})
}

func TestUnitMemoryCompleteRestartsTheTTL(t *testing.T) {
	ctx := t.Context()
	c := newClock()
	m := NewMemory(Config{TTL: time.Hour, Lock: time.Hour}, c.now)
	k := Key{Value: "k"}
	_, _, _ = m.Claim(ctx, k, "fp")
	c.advance(50 * time.Minute)
	if err := m.Complete(ctx, k, Response{Status: 201}); err != nil {
		t.Fatal(err)
	}
	c.advance(50 * time.Minute)
	if rec, ok, _ := m.Claim(ctx, k, "fp"); ok || rec.Response == nil {
		t.Errorf("a response expired before its TTL from completion: %+v %v", rec, ok)
	}
}

func TestUnitMemorySweeps(t *testing.T) {
	ctx := t.Context()
	c := newClock()
	m := NewMemory(Config{TTL: time.Second, Lock: time.Second}, c.now)
	_, _, _ = m.Claim(ctx, Key{Value: "old"}, "fp")
	c.advance(sweepEvery)
	_, _, _ = m.Claim(ctx, Key{Value: "new"}, "fp")
	if _, kept := m.records[Key{Value: "old"}]; kept {
		t.Error("an expired key survived the sweep")
	}
	if _, kept := m.records[Key{Value: "new"}]; !kept {
		t.Error("a live key was swept")
	}
}

func TestUnitValidate(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatal(err)
	}
	err := (Config{}).Validate()
	if !errors.Is(err, errTTL) || !errors.Is(err, errLock) {
		t.Errorf("zero ttl and lock: %v", err)
	}
}
