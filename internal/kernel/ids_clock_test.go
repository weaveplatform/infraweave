package kernel

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
)

func TestUnitULIDSerializesDelayedClockCapture(t *testing.T) {
	// Each new timestamp gets a known entropy value. Returning to an already
	// generated millisecond must not reset its entropy below the earlier ID.
	entropy := append(bytes.Repeat([]byte{255}, 10), bytes.Repeat([]byte{128}, 10)...)
	entropy = append(entropy, make([]byte, 10)...)
	ulidMu.Lock()
	original := ulidEntropy
	ulidEntropy = ulid.Monotonic(bytes.NewReader(entropy), 1)
	ulidMu.Unlock()
	defer func() { ulidMu.Lock(); ulidEntropy = original; ulidMu.Unlock() }()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	captured, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseClock := func() { releaseOnce.Do(func() { close(release) }) }
	var calls sync.WaitGroup
	defer calls.Wait()
	defer releaseClock()
	earlier, later := time.UnixMilli(1795185400000), time.UnixMilli(1795185400001)
	earlyDone := make(chan ULID, 1)
	calls.Go(func() { earlyDone <- newULID(func() time.Time { close(captured); <-release; return earlier }) })
	select {
	case <-captured:
	case <-ctx.Done():
		t.Fatal("generator did not reach the controlled clock capture")
	}

	// Observe the timing seam solely to avoid forcing a deadlock after the fix.
	// An unserialized clock allows a later call to finish before the old capture;
	// a serialized clock queues that call until the captured value is released.
	serialized := !ulidMu.TryLock()
	if !serialized {
		ulidMu.Unlock()
	}
	var firstLater ULID
	laterDone := make(chan ULID, 1)
	if serialized {
		calls.Go(func() { laterDone <- newULID(func() time.Time { return later }) })
	} else {
		firstLater = newULID(func() time.Time { return later })
	}
	releaseClock()
	var early ULID
	select {
	case early = <-earlyDone:
	case <-ctx.Done():
		t.Fatal("delayed clock generation did not finish")
	}
	if serialized {
		select {
		case firstLater = <-laterDone:
		case <-ctx.Done():
			t.Fatal("queued clock generation did not finish")
		}
	}
	secondLater := newULID(func() time.Time { return later })
	if !early.Time().Equal(earlier) || !firstLater.Time().Equal(later) || !secondLater.Time().Equal(later) {
		t.Fatal("generation changed the observed clock timestamp", early, firstLater, secondLater)
	}
	if bytes.Compare(secondLater[:], firstLater[:]) <= 0 {
		t.Fatalf("same-millisecond ID regressed after a delayed older clock capture: first=%s later=%s", firstLater, secondLater)
	}
}

func TestUnitULIDPreservesActualBackwardsClockTimestamp(t *testing.T) {
	newer, older := time.UnixMilli(1795185500001), time.UnixMilli(1795185500000)
	first := newULID(func() time.Time { return newer })
	second := newULID(func() time.Time { return older })
	if !first.Time().Equal(newer) || !second.Time().Equal(older) || bytes.Compare(second[:], first[:]) >= 0 {
		t.Fatal("actual backwards clock changed timestamp semantics", first, second)
	}
}
