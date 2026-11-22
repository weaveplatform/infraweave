package protocol_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
)

func (s *store) Binding(_ context.Context, id kernel.ULID) (kernel.WorkerOwner, bool, error) {
	owner, ok := s.bindings[id]
	return owner, ok, nil
}

func (s *store) Bind(_ context.Context, p pool.Pool, owner kernel.WorkerOwner) error {
	s.bindings[p.ID] = owner
	return nil
}

func (s *store) Unbind(_ context.Context, id kernel.ULID) error {
	delete(s.bindings, id)
	return nil
}

func (*store) ManagedPool(context.Context, kernel.WorkerOwner) (*kernel.ULID, bool, error) {
	return nil, false, nil
}

func (s *store) HasWorkers(context.Context, kernel.ULID) (bool, error) {
	return len(s.workers) > 0, nil
}

type idlePool struct{}

func (idlePool) ActivePool(context.Context, kernel.ULID) (bool, error) { return false, nil }

// proof is an ownership proof of the fixture's pool for owner.
func (f *fixture) proof(t *testing.T, owner kernel.WorkerOwner) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: f.key},
		(&jose.SignerOptions{}).WithHeader("x5c",
			[]string{base64.StdEncoding.EncodeToString(f.cert.Raw)}))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]any{
		"pool": f.store.pool.ID.String(), "iat": time.Now().Unix(), "jti": kernel.NewULID().String(), "owner": owner,
	})
	if err != nil {
		t.Fatal(err)
	}
	obj, err := signer.Sign(b)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := obj.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestUnitProtocolPoolOwnership checks that bind and release answer 204,
// that another owner answers 409 invalid-state, that a launcher cannot
// register into a bound pool, and that an unsigned proof answers 401.
func TestUnitProtocolPoolOwnership(t *testing.T) {
	f := newFixture(t)
	f.svc.BindAssignments(idlePool{})
	owner := kernel.WorkerOwner{ClusterID: "cluster", Namespace: "workers", ResourceUID: "uid"}
	other := kernel.WorkerOwner{ClusterID: "cluster", Namespace: "workers", ResourceUID: "other"}
	if code, body := f.call(t, "/pools/bind", "", map[string]any{"proof": f.proof(t, owner)}); code != http.StatusNoContent {
		t.Fatalf("bind = %d %v", code, body)
	}
	if code, body := f.call(t, "/pools/bind", "", map[string]any{"proof": f.proof(t, other)}); code != http.StatusConflict ||
		body["type"] != "urn:infraweave:problem:invalid-state" {
		t.Fatalf("competing bind = %d %v", code, body)
	}
	if code, body := f.call(t, "/register", "", map[string]any{"registration": f.registration(t, "")}); code != http.StatusConflict {
		t.Fatalf("ownerless registration = %d %v", code, body)
	}
	if code, body := f.call(t, "/pools/release", "", map[string]any{"proof": "garbage"}); code != http.StatusUnauthorized {
		t.Fatalf("unsigned release = %d %v", code, body)
	}
	if code, body := f.call(t, "/pools/bind", "", map[string]any{"proof": "garbage"}); code != http.StatusUnauthorized {
		t.Fatalf("unsigned bind = %d %v", code, body)
	}
	if code, body := f.call(t, "/pools/release", "", map[string]any{"proof": f.proof(t, owner)}); code != http.StatusNoContent {
		t.Fatalf("release = %d %v", code, body)
	}
	if len(f.store.bindings) != 0 {
		t.Fatalf("binding kept: %v", f.store.bindings)
	}
}
