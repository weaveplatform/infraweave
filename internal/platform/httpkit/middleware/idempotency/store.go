package idempotency

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Key names one idempotency key: keys are scoped to the caller, so two
// callers never see each other's.
type Key struct {
	Caller string
	Value  string
}

// Response is what a replay sends: the status, the two headers a replay
// repeats, Content-Type and Location, and the body.
//
// ref: decision 0003
type Response struct {
	Status      int
	ContentType string
	Location    string
	Body        []byte
}

// Record is the state of one key. Response is nil while the first request
// is in flight.
type Record struct {
	Fingerprint string
	Response    *Response
}

// Store holds the keys. Claim is atomic: of any number of concurrent
// requests claiming one key, exactly one succeeds.
type Store interface {
	// Claim records key as in flight for a request with fingerprint and
	// reports true, or reports false with the record that holds key. A key
	// that has expired, or whose request has held it past the lock without
	// finishing, is claimed afresh: its replica is taken to have crashed.
	Claim(ctx context.Context, key Key, fingerprint string) (Record, bool, error)
	// Complete stores the response of key's request, which keeps the key for
	// the TTL from now.
	Complete(ctx context.Context, key Key, resp Response) error
	// Release forgets key while its request is in flight, so the next
	// request with it runs; a stored response is never released.
	Release(ctx context.Context, key Key) error
}

// ErrNotReserved is Complete's error for a key that is not in flight.
var ErrNotReserved = errors.New("idempotency key is not reserved")

// sweepEvery bounds how often Memory scans for expired keys. The scan holds
// the lock, so it runs at most once a minute however busy the server is.
const sweepEvery = time.Minute

// Memory is a Store in process memory, for tests. Each instance keeps its
// own keys, so two servers on two Memory stores would each run a retry the
// other already ran. Expired records are dropped as Claim passes the sweep
// interval.
type Memory struct {
	ttl, lock time.Duration
	now       func() time.Time

	mu      sync.Mutex
	records map[Key]entry
	swept   time.Time
}

type entry struct {
	Record
	lockedUntil, expires time.Time
}

// NewMemory keeps keys as c says by the clock now.
func NewMemory(c Config, now func() time.Time) *Memory {
	return &Memory{ttl: c.TTL, lock: c.Lock, now: now, records: map[Key]entry{}}
}

// live is key's entry unless it has expired. The caller holds m.mu.
func (m *Memory) live(key Key, now time.Time) (entry, bool) {
	e, ok := m.records[key]
	if !ok || !now.Before(e.expires) {
		return entry{}, false
	}
	return e, true
}

// Claim implements Store.
func (m *Memory) Claim(_ context.Context, key Key, fingerprint string) (Record, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if now.Sub(m.swept) >= sweepEvery {
		m.sweep(now)
	}
	if e, ok := m.live(key, now); ok && (e.Response != nil || now.Before(e.lockedUntil)) {
		return e.Record, false, nil
	}
	m.records[key] = entry{Record{Fingerprint: fingerprint}, now.Add(m.lock), now.Add(m.ttl)}
	return Record{}, true, nil
}

// Complete implements Store.
func (m *Memory) Complete(_ context.Context, key Key, resp Response) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	e, ok := m.live(key, now)
	if !ok || e.Response != nil {
		return ErrNotReserved
	}
	e.Response, e.expires = &resp, now.Add(m.ttl)
	m.records[key] = e
	return nil
}

// Release implements Store.
func (m *Memory) Release(_ context.Context, key Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.records[key]; ok && e.Response == nil {
		delete(m.records, key)
	}
	return nil
}

func (m *Memory) sweep(now time.Time) {
	for k, e := range m.records {
		if !now.Before(e.expires) {
			delete(m.records, k)
		}
	}
	m.swept = now
}
