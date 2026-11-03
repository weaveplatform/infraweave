package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Key names one bucket.
type Key struct {
	Caller string
	Class  Class
}

// State is a bucket after a Take.
type State struct {
	// Allowed is whether a token was taken.
	Allowed bool
	// Remaining is the whole tokens left.
	Remaining int
	// Reset is how long until the bucket is full again.
	Reset time.Duration
	// RetryAfter is how long until one token is available; zero when
	// Allowed.
	RetryAfter time.Duration
}

// Store holds the buckets. Take is atomic per key, so concurrent requests
// from one caller never spend the same token twice.
type Store interface {
	// Take refills key's bucket to now under l, then takes one token if
	// one is there. A bucket never seen starts full.
	Take(ctx context.Context, key Key, l Limit, now time.Time) (State, error)
}

// bucket is a token bucket as of at. full is when it refills to its burst,
// after which it holds nothing a new bucket wouldn't.
type bucket struct {
	tokens   float64
	at, full time.Time
}

func (b *bucket) take(l Limit, now time.Time) State {
	// A clock that steps backwards refills nothing rather than draining.
	if elapsed := now.Sub(b.at); elapsed > 0 {
		b.tokens = min(float64(l.Burst), b.tokens+elapsed.Seconds()*l.Rate)
	}
	b.at = now
	var st State
	if b.tokens >= 1 {
		b.tokens--
		st.Allowed = true
	} else {
		st.RetryAfter = duration((1 - b.tokens) / l.Rate)
	}
	st.Remaining = int(b.tokens)
	st.Reset = duration((float64(l.Burst) - b.tokens) / l.Rate)
	b.full = now.Add(st.Reset)
	return st
}

func duration(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}

// sweepEvery bounds how often Memory scans for full buckets. The scan holds
// the lock, so it runs at most once a minute however busy the server is.
const sweepEvery = time.Minute

// Memory is a Store in process memory. Each replica keeps its own buckets,
// so it enforces the budget per replica: enough for one, not for several.
// Buckets that have refilled are dropped as Take passes the sweep interval,
// so memory follows the callers active in the last refill period.
type Memory struct {
	mu      sync.Mutex
	buckets map[Key]*bucket
	swept   time.Time
}

// NewMemory is an empty Memory.
func NewMemory() *Memory { return &Memory{buckets: map[Key]*bucket{}} }

// Take implements Store.
func (m *Memory) Take(_ context.Context, key Key, l Limit, now time.Time) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if now.Sub(m.swept) >= sweepEvery {
		m.sweep(now)
	}
	b, ok := m.buckets[key]
	if !ok {
		b = &bucket{tokens: float64(l.Burst), at: now}
		m.buckets[key] = b
	}
	return b.take(l, now), nil
}

func (m *Memory) sweep(now time.Time) {
	for k, b := range m.buckets {
		if !now.Before(b.full) {
			delete(m.buckets, k)
		}
	}
	m.swept = now
}
