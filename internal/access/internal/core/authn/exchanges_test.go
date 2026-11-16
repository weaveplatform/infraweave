package authn_test

import (
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authn"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// TestUnitExchanges checks the kept exchanges: a key's requests share one
// session until ExchangeMargin before its token expires, when the key is
// exchanged again; another secret for the key is exchanged, and refused,
// rather than served the kept token; a token whose session is revoked is
// dropped and the key exchanged again, which a disabled key fails; and a
// store that cannot be read fails rather than refuses.
func TestUnitExchanges(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	e := authn.NewExchanges(w.s)
	auth := func(secret string) (kernel.Principal, error) {
		t.Helper()
		p, c, err := e.Authenticate(ctx, w.key.String(), secret, "192.0.2.1")
		if err == nil && c.Session != p.SessionID {
			t.Errorf("the claims' session %s is not the principal's %s", c.Session, p.SessionID)
		}
		return p, err
	}
	p, err := auth(w.secret)
	if err != nil || p.Caller() != "api_key:"+w.key.String() || len(w.f.sessions) != 1 {
		t.Fatalf("a first request = %+v, %v with %d sessions", p, err, len(w.f.sessions))
	}
	first := p.SessionID
	if w.f.sessions[first].IPAddress != "192.0.2.1" {
		t.Errorf("the session's address is %q", w.f.sessions[first].IPAddress)
	}
	if _, err := auth(apikey.NewSecret()); !errors.Is(err, authn.ErrUnauthenticated) {
		t.Errorf("another secret = %v", err)
	}
	w.f.now = t0.Add(10*time.Hour - authn.ExchangeMargin - time.Second)
	for range 3 {
		if p, err := auth(w.secret); err != nil || p.SessionID != first || len(w.f.sessions) != 1 {
			t.Fatalf("a kept token = %+v, %v with %d sessions", p, err, len(w.f.sessions))
		}
	}

	w.f.now = t0.Add(10*time.Hour - authn.ExchangeMargin)
	p, err = auth(w.secret)
	if err != nil || p.SessionID == first || len(w.f.sessions) != 2 {
		t.Fatalf("a token due for renewal = %+v, %v with %d sessions", p, err, len(w.f.sessions))
	}

	if _, err := w.s.Revoke(ctx, p, p.SessionID); err != nil {
		t.Fatal(err)
	}
	revoked := p.SessionID
	if p, err = auth(w.secret); err != nil || p.SessionID == revoked || len(w.f.sessions) != 3 {
		t.Fatalf("after a revocation = %+v, %v with %d sessions", p, err, len(w.f.sessions))
	}

	w.f.now = w.f.now.Add(session.CacheTTL + time.Second)
	w.f.fail = "Session"
	if _, err := auth(w.secret); !errors.Is(err, errDown) {
		t.Errorf("a session store down = %v", err)
	}
	w.f.fail = ""

	k := w.f.keys[w.key]
	k.DisabledAt = w.f.now
	w.f.keys[w.key] = k
	if _, err := w.s.Revoke(ctx, p, p.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := auth(w.secret); !errors.Is(err, authn.ErrUnauthenticated) {
		t.Errorf("a disabled key = %v", err)
	}
}

// TestUnitExchangesRefused checks that an ID that is no ULID, an operator
// key and an organization key are refused before any exchange, since no
// token of theirs reaches an account.
func TestUnitExchangesRefused(t *testing.T) {
	w, s, op := operatorWorld(t, operatorAudience)
	e := authn.NewExchanges(s)
	for name, c := range map[string]struct{ id, secret string }{
		"not a ULID":          {"nope", w.secret},
		"an operator key":     {op.String(), "op-secret"},
		"an organization key": {kernel.NewULID().String(), apikey.OrganizationPrefix + "abc"},
	} {
		if _, _, err := e.Authenticate(t.Context(), c.id, c.secret, ""); !errors.Is(
			err, authn.ErrUnauthenticated) {
			t.Errorf("%s = %v", name, err)
		}
	}
	if len(w.f.sessions) != 0 || w.f.touched != 0 {
		t.Errorf("a refusal exchanged: %d sessions, %d touches", len(w.f.sessions), w.f.touched)
	}
}
