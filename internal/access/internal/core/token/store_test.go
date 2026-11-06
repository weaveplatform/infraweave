package token_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
)

var errDown = errors.New("store down")

// memStore is token.Store in memory, which applies steps as the
// PostgreSQL store does, generating local keys.
type memStore struct {
	mu      sync.Mutex
	keys    []token.Key
	signers map[kernel.ULID]*signer.Local
	// reads counts Keys calls; err fails every call; inert makes Rotate
	// apply nothing.
	reads int
	err   error
	inert bool
}

func newMemStore() *memStore { return &memStore{signers: map[kernel.ULID]*signer.Local{}} }

func (m *memStore) Keys(_ context.Context, p token.Purpose) ([]token.Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reads++
	if m.err != nil {
		return nil, m.err
	}
	return m.live(p), nil
}

func (m *memStore) live(p token.Purpose) []token.Key {
	var out []token.Key
	for _, k := range m.keys {
		if k.Purpose == p && k.State != token.StateRetired {
			out = append(out, k)
		}
	}
	return out
}

func (m *memStore) Signer(_ context.Context, id kernel.ULID) (signer.Signer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	return m.signers[id], nil
}

func (m *memStore) Rotate(
	_ context.Context,
	p token.Purpose,
	plan func([]token.Key) []token.Step,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	steps := plan(m.live(p))
	if m.inert {
		return nil
	}
	for _, s := range steps {
		if s.ID.IsZero() {
			id := kernel.NewULID()
			l, err := signer.GenerateLocal(id.String())
			if err != nil {
				return err //nolint:wrapcheck // a test store
			}
			k := token.Key{ID: id, Purpose: p, Public: l.Public(), State: s.State, CreatedAt: s.At}
			if s.State == token.StateActive {
				k.ActivatedAt = s.At
			}
			m.keys, m.signers[id] = append(m.keys, k), l
			continue
		}
		i := slices.IndexFunc(m.keys, func(k token.Key) bool { return k.ID == s.ID })
		m.keys[i].State = s.State
		switch s.State {
		case token.StateActive:
			m.keys[i].ActivatedAt = s.At
		case token.StateRetiring:
			m.keys[i].RetireAfter = s.RetireAfter
		case token.StateNext, token.StateRetired:
		}
	}
	return nil
}

// state is the state of the key id, or "" for none.
func (m *memStore) state(id string) token.State {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range m.keys {
		if k.ID.String() == id {
			return k.State
		}
	}
	return ""
}

// clock is virtual time.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// at sets the clock to d after start.
func (c *clock) at(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = start.Add(d)
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newID() kernel.ULID { return kernel.NewULID() }
