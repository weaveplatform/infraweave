package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

var t0 = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func TestUnitLive(t *testing.T) {
	s := Session{ExpiresAt: t0.Add(time.Hour)}
	if !s.Live(t0) || s.Live(t0.Add(time.Hour)) {
		t.Error("the absolute end is wrong")
	}
	s.IdleExpiresAt = t0.Add(time.Minute)
	if s.Live(t0.Add(time.Minute)) {
		t.Error("an idle session is live")
	}
	s.IdleExpiresAt, s.RevokedAt = time.Time{}, t0
	if s.Live(t0) {
		t.Error("a revoked session is live")
	}
}

func TestUnitPrincipal(t *testing.T) {
	s := Session{
		ID:        kernel.NewULID(),
		ActorKind: kernel.PrincipalAPIKey,
		ActorID:   kernel.NewULID(),
		AccountID: kernel.NewAccountID(),
		Channel:   ChannelUnknown,
		Login:     "k",
		Teams:     []string{"ops"},
	}
	p := s.Principal()
	if p.SessionID != s.ID || p.Kind != kernel.PrincipalAPIKey || p.ActorID != s.ActorID ||
		p.AccountID != s.AccountID || p.Channel != "UNKNOWN" || p.Login != "k" || p.Teams[0] != "ops" {
		t.Errorf("Principal = %+v", p)
	}
}

func TestUnitKey(t *testing.T) {
	id := kernel.NewULID()
	if got, ok := ParseKey(Key(id)); !ok || got != id {
		t.Errorf("ParseKey(Key) = %v, %v", got, ok)
	}
	for _, m := range []string{"api_disabled_callers", "session:nope", ""} {
		if _, ok := ParseKey(m); ok {
			t.Errorf("ParseKey(%q) succeeded", m)
		}
	}
}

// clock is virtual time.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func TestUnitCache(t *testing.T) {
	c := &clock{t: t0}
	id := kernel.NewULID()
	reads, revoked := 0, false
	errDown := errors.New("down")
	var fail bool
	cache := NewCache(func(_ context.Context, got kernel.ULID) (Session, error) {
		reads++
		if fail {
			return Session{}, errDown
		}
		s := Session{ID: got, ExpiresAt: t0.Add(24 * time.Hour)}
		if revoked {
			s.RevokedAt = c.t
		}
		return s, nil
	}, c.now)
	ctx := context.Background()
	get := func() Session {
		t.Helper()
		s, err := cache.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	get()
	get()
	if reads != 1 {
		t.Fatalf("%d reads, want 1", reads)
	}
	// A revocation whose message is lost shows within CacheTTL.
	revoked = true
	c.t = c.t.Add(CacheTTL - time.Second)
	if !get().Live(c.t) {
		t.Error("the cache reread before CacheTTL")
	}
	c.t = c.t.Add(time.Second)
	if get().Live(c.t) {
		t.Error("a revoked session is live after CacheTTL")
	}

	// A message drops the session at once.
	revoked = false
	get()
	n := reads
	cache.Drop(id)
	get()
	if reads != n+1 {
		t.Error("Drop did not force a read")
	}
	cache.Flush()
	get()
	if reads != n+2 {
		t.Error("Flush did not force a read")
	}

	// A failed read is not cached.
	cache.Flush()
	fail = true
	if _, err := cache.Get(ctx, id); !errors.Is(err, errDown) {
		t.Fatalf("Get = %v", err)
	}
	fail = false
	get()
}

func TestUnitCacheSweep(t *testing.T) {
	c := &clock{t: t0}
	cache := NewCache(func(_ context.Context, id kernel.ULID) (Session, error) {
		return Session{ID: id}, nil
	}, c.now)
	for range sweepAt {
		if _, err := cache.Get(context.Background(), kernel.NewULID()); err != nil {
			t.Fatal(err)
		}
	}
	c.t = c.t.Add(CacheTTL)
	if _, err := cache.Get(context.Background(), kernel.NewULID()); err != nil {
		t.Fatal(err)
	}
	if n := len(cache.entries); n != 1 {
		t.Errorf("%d entries after the sweep, want 1", n)
	}
}
