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
	// Take refills key's bucket under l to now, by the store's clock, then
	// takes one token if one is there. A bucket never seen starts full.
	Take(ctx context.Context, key Key, l Limit) (State, error)
}

// stateOf is the State of a bucket under l left holding tokens.
func stateOf(l Limit, tokens float64, allowed bool) State {
	st := State{
		Allowed:   allowed,
		Remaining: int(tokens),
		Reset:     duration((float64(l.Burst) - tokens) / l.Rate),
	}
	if !allowed {
		st.RetryAfter = duration((1 - tokens) / l.Rate)
	}
	return st
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
		b.at = now
	}
	allowed := b.tokens >= 1
	if allowed {
		b.tokens--
	}
	st := stateOf(l, b.tokens, allowed)
	b.full = now.Add(st.Reset)
	return st
}

func duration(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}

// sweepEvery bounds how often Memory scans for full buckets. The scan holds
// the lock, so it runs at most once a minute however busy the server is.
const sweepEvery = time.Minute

// Memory is a Store in process memory, for tests. Each instance keeps its
// own buckets, so two servers on two Memory stores would each grant the
// whole budget. Buckets that have refilled are dropped as Take passes the
// sweep interval.
type Memory struct {
	now func() time.Time

	mu      sync.Mutex
	buckets map[Key]*bucket
	swept   time.Time
}

// NewMemory is an empty Memory that refills by the clock now.
func NewMemory(now func() time.Time) *Memory {
	return &Memory{now: now, buckets: map[Key]*bucket{}}
}

// Take implements Store.
func (m *Memory) Take(_ context.Context, key Key, l Limit) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
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
