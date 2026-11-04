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

// Response is what a replay sends: the status, the two headers decision 0003
// names, and the body.
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

// Store holds the keys. Reserve is atomic: of any number of concurrent
// requests reserving one key, exactly one succeeds.
type Store interface {
	// Get returns key's record and whether there is one.
	Get(ctx context.Context, key Key) (Record, bool, error)
	// Reserve records key as in flight for a request with fingerprint. It
	// reports false, changing nothing, when key already has a record.
	Reserve(ctx context.Context, key Key, fingerprint string) (bool, error)
	// Complete stores the response of key's request.
	Complete(ctx context.Context, key Key, resp Response) error
	// Release forgets key, so the next request with it runs.
	Release(ctx context.Context, key Key) error
}

// ErrNotReserved is Complete's error for a key with no record.
var ErrNotReserved = errors.New("idempotency key is not reserved")

// sweepEvery bounds how often Memory scans for expired keys. The scan holds
// the lock, so it runs at most once a minute however busy the server is.
const sweepEvery = time.Minute

// Memory is a Store in process memory. Each replica keeps its own keys, so a
// retry that reaches another replica runs again: enough for one replica, not
// for several. A record expires ttl after it is reserved or completed;
// expired records are dropped as Reserve passes the sweep interval.
type Memory struct {
	ttl time.Duration
	now func() time.Time

	mu      sync.Mutex
	records map[Key]entry
	swept   time.Time
}

type entry struct {
	Record
	expires time.Time
}

// NewMemory keeps each key for ttl by the clock now.
func NewMemory(ttl time.Duration, now func() time.Time) *Memory {
	return &Memory{ttl: ttl, now: now, records: map[Key]entry{}}
}

// live is key's entry unless it has expired. The caller holds m.mu.
func (m *Memory) live(key Key, now time.Time) (entry, bool) {
	e, ok := m.records[key]
	if !ok || !now.Before(e.expires) {
		return entry{}, false
	}
	return e, true
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, key Key) (Record, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.live(key, m.now())
	return e.Record, ok, nil
}

// Reserve implements Store.
func (m *Memory) Reserve(_ context.Context, key Key, fingerprint string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if now.Sub(m.swept) >= sweepEvery {
		m.sweep(now)
	}
	if _, ok := m.live(key, now); ok {
		return false, nil
	}
	m.records[key] = entry{Record{Fingerprint: fingerprint}, now.Add(m.ttl)}
	return true, nil
}

// Complete implements Store. The key's lifetime restarts, so a response is
// replayable for the whole ttl however long the request took.
func (m *Memory) Complete(_ context.Context, key Key, resp Response) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	e, ok := m.live(key, now)
	if !ok {
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
	delete(m.records, key)
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
