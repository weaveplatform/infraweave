package token_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
)

const day = 24 * time.Hour

// kidOf is the kid in raw's header.
func kidOf(t *testing.T, raw string) string {
	t.Helper()
	kid, _ := segment(t, raw, 0)["kid"].(string)
	return kid
}

// TestUnitKeyRotation runs the daily authn.keys.rotate over two key
// lifetimes in virtual time.
func TestUnitKeyRotation(t *testing.T) {
	ctx := context.Background()
	svc, store, c := setup(t)
	rotate := func() {
		t.Helper()
		if err := svc.Rotate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	issue := func() string {
		t.Helper()
		raw, _, err := svc.Issue(ctx, request())
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	verifies := func(raw string) bool {
		t.Helper()
		_, err := svc.Verify(ctx, raw, audience)
		if err != nil && !errors.Is(err, token.ErrToken) {
			t.Fatal(err)
		}
		return err == nil
	}

	rotate()
	first := issue()
	a := kidOf(t, first)

	// Day 83: seven days before the key turns 90, its successor appears.
	for range 82 {
		c.advance(day)
		rotate()
	}
	if live, _ := store.Keys(ctx, token.APIToken); len(live) != 1 {
		t.Fatalf("day 82: %d keys, want 1", len(live))
	}
	c.advance(day)
	rotate()
	live, _ := store.Keys(ctx, token.APIToken)
	if len(live) != 2 || live[1].State != token.StateNext {
		t.Fatalf("day 83: keys %+v, want a next key", live)
	}
	b := live[1].Public.KeyID

	// The next key verifies at once, but the active key keeps signing.
	if !verifies(forge(t, store, b, "ES256", nil, claimsNow(c.now()))) {
		t.Error("a token under the next key does not verify")
	}
	c.advance(token.SignerRefresh)
	if kidOf(t, issue()) != a {
		t.Error("the next key signs before it takes over")
	}

	// Day 90: the next key, published for seven days, takes over; a token
	// the old key signed 30 seconds before verifies until it expires.
	lifetime := 90 * day
	c.at(lifetime - 30*time.Second)
	rotate()
	last := issue()
	if kidOf(t, last) != a || store.state(b) != token.StateNext {
		t.Fatal("the key changed before day 90")
	}
	c.at(lifetime)
	rotate()
	if store.state(a) != token.StateRetiring || store.state(b) != token.StateActive {
		t.Fatalf("day 90: a %s, b %s", store.state(a), store.state(b))
	}
	// This replica signs with the old key until its refresh.
	if kidOf(t, issue()) != a {
		t.Error("the signer refreshed before SignerRefresh")
	}
	c.at(lifetime - 30*time.Second + token.SignerRefresh)
	if kidOf(t, issue()) != b {
		t.Error("the new key does not sign after SignerRefresh")
	}
	c.at(lifetime - 30*time.Second + 10*time.Hour)
	if !verifies(last) {
		t.Error("the old key's last token stopped verifying before it expired")
	}

	// The old key retires 24 hours after its last possible token expires.
	retired := lifetime + token.SignerRefresh + 10*time.Hour + token.RetireGrace
	c.at(retired - time.Second)
	rotate()
	if store.state(a) != token.StateRetiring {
		t.Fatalf("a second early: a is %s", store.state(a))
	}
	c.at(retired)
	rotate()
	if store.state(a) != token.StateRetired {
		t.Fatalf("on time: a is %s, want retired", store.state(a))
	}
	if verifies(first) {
		t.Error("a token of the retired key verifies")
	}
}

func claimsNow(now time.Time) any {
	return map[string]any{
		"iss":     issuer,
		"aud":     audience,
		"sub":     "api::k",
		"jti":     "01JC0000000000000000000000",
		"iat":     now.Unix(),
		"nbf":     now.Unix(),
		"exp":     now.Add(time.Hour).Unix(),
		"iw_kind": "api_key",
	}
}

func TestUnitManualRotation(t *testing.T) {
	ctx := context.Background()
	store, c := newMemStore(), &clock{t: start}
	svc := token.New(store, issuer, token.Policy{Manual: true, TokenLife: time.Hour}, c.now)
	for range 2 * 365 {
		if err := svc.Rotate(ctx); err != nil {
			t.Fatal(err)
		}
		c.advance(day)
	}
	if live, _ := store.Keys(
		ctx,
		token.APIToken,
	); len(live) != 1 ||
		live[0].State != token.StateActive {
		t.Errorf("after two years of manual rotation: %+v, want the first key alone", live)
	}
}

func TestUnitPlan(t *testing.T) {
	p := token.Policy{TokenLife: time.Hour}
	next := token.Key{ID: newID(), State: token.StateNext, CreatedAt: start}
	// A next key with no active key, which only a failed promotion leaves,
	// is promoted at once.
	if s := p.Plan(
		[]token.Key{next},
		start,
	); len(s) != 1 || s[0].ID != next.ID ||
		s[0].State != token.StateActive {
		t.Errorf("orphaned next key: %+v", s)
	}
	// A next key not yet published for 48 hours waits, however old the
	// active key.
	active := token.Key{ID: newID(), State: token.StateActive, ActivatedAt: start.Add(-200 * day)}
	if s := p.Plan([]token.Key{active, next}, start.Add(47*time.Hour)); len(s) != 0 {
		t.Errorf("young next key: %+v", s)
	}
	s := p.Plan([]token.Key{active, next}, start.Add(48*time.Hour))
	if len(s) != 2 || s[0].State != token.StateRetiring || s[1].State != token.StateActive ||
		!s[0].RetireAfter.Equal(start.Add(48*time.Hour+token.SignerRefresh+time.Hour)) {
		t.Errorf("due promotion: %+v", s)
	}
}

// TestUnitKMSRotation runs authn.keys.rotate under the kms backend, where
// a key is replaced only when the operator adds a reference: the first
// reference signs from the start and for as long as it is the newest, and
// an added one is published as the next key for MinPublished before it
// takes over.
func TestUnitKMSRotation(t *testing.T) {
	ctx := context.Background()
	store, c := newMemStore(), &clock{t: start}
	policy := token.Policy{TokenLife: time.Hour, Refs: []string{"vault:transit/a/1"}}
	svc := token.New(store, issuer, policy, c.now)
	first, _, err := svc.Issue(ctx, request())
	if err != nil {
		t.Fatal(err)
	}
	for range 200 {
		c.advance(day)
		if err := svc.Rotate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	live, _ := store.Keys(ctx, token.APIToken)
	if len(live) != 1 || live[0].Ref != "vault:transit/a/1" || live[0].State != token.StateActive {
		t.Fatalf("after 200 days on one reference: %+v", live)
	}

	// The operator adds a reference and restarts.
	policy.Refs = append(policy.Refs, "vault:transit/b/1")
	svc = token.New(store, issuer, policy, c.now)
	if err := svc.Rotate(ctx); err != nil {
		t.Fatal(err)
	}
	live, _ = store.Keys(ctx, token.APIToken)
	if len(live) != 2 || live[1].Ref != "vault:transit/b/1" || live[1].State != token.StateNext {
		t.Fatalf("the added reference: %+v", live)
	}
	c.advance(token.MinPublished - time.Hour)
	if err := svc.Rotate(ctx); err != nil {
		t.Fatal(err)
	}
	if store.state(live[1].ID.String()) != token.StateNext {
		t.Fatal("the next key signed before it was published for MinPublished")
	}
	before, _, err := svc.Issue(ctx, request())
	if err != nil || kidOf(t, before) != kidOf(t, first) {
		t.Fatalf("before the swap: %v", err)
	}
	c.advance(time.Hour)
	if err := svc.Rotate(ctx); err != nil {
		t.Fatal(err)
	}
	if store.state(live[0].ID.String()) != token.StateRetiring ||
		store.state(live[1].ID.String()) != token.StateActive {
		t.Fatalf("after MinPublished: %+v", store.live(token.APIToken))
	}
	if _, err := svc.Verify(ctx, before, audience); err != nil {
		t.Errorf("a token of the retiring key: %v", err)
	}
	c.advance(token.SignerRefresh)
	after, _, err := svc.Issue(ctx, request())
	if err != nil || kidOf(t, after) != live[1].ID.String() {
		t.Errorf("after the swap: kid %s, %v", kidOf(t, after), err)
	}
	if err := svc.Rotate(ctx); err != nil {
		t.Fatal(err)
	}
	if live, _ := store.Keys(ctx, token.APIToken); len(live) != 2 {
		t.Errorf("a reference a key holds made another: %+v", live)
	}
}

func TestUnitPlanKMS(t *testing.T) {
	p := token.Policy{TokenLife: time.Hour, Refs: []string{"awskms:a", "awskms:b"}}
	if s := p.Plan(
		nil,
		start,
	); len(s) != 1 || s[0].State != token.StateActive ||
		s[0].Ref != "awskms:b" {
		t.Errorf("no keys: %+v", s)
	}
	next := token.Key{ID: newID(), State: token.StateNext, CreatedAt: start, Ref: "awskms:b"}
	if s := p.Plan([]token.Key{next}, start); len(s) != 1 || s[0].ID != next.ID ||
		s[0].State != token.StateActive {
		t.Errorf("orphaned next key: %+v", s)
	}
	// A local key the operator moves to kms from is replaced like any
	// other, however young.
	local := token.Key{ID: newID(), State: token.StateActive, ActivatedAt: start}
	if s := p.Plan([]token.Key{local}, start); len(s) != 1 || s[0].State != token.StateNext ||
		s[0].Ref != "awskms:b" {
		t.Errorf("a local key: %+v", s)
	}
	manual := p
	manual.Manual = true
	if s := manual.Plan([]token.Key{local}, start); len(s) != 0 {
		t.Errorf("bootstrapping replaced an active key: %+v", s)
	}
}
