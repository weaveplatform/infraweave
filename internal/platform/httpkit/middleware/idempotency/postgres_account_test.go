package idempotency

import (
	"bytes"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestAccPostgresAccountNamespacesBindSealedResponsesWithoutReinterpretingLegacyRows(t *testing.T) {
	p := pool(t)
	s := store(t, p)
	actor := "user:" + kernel.NewULID().String()
	legacy := Key{Caller: actor, Value: "same-key"}
	a := Key{Caller: "account/" + kernel.NewAccountID().String() + "/" + actor, Value: legacy.Value}
	b := Key{Caller: "account/" + kernel.NewAccountID().String() + "/" + actor, Value: legacy.Value}
	for _, k := range []Key{legacy, a, b} {
		if _, ok, err := s.Claim(t.Context(), k, "same-fingerprint"); err != nil || !ok {
			t.Fatalf("distinct namespace claim %s: %v/%v", k.Caller, ok, err)
		}
		if err := s.Complete(t.Context(), k, Response{Status: 201, Body: []byte(k.Caller)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range []Key{legacy, a, b} {
		got, claimed, err := s.Claim(t.Context(), k, "same-fingerprint")
		if err != nil || claimed || got.Response == nil || string(got.Response.Body) != k.Caller {
			t.Fatalf("owned namespace replay %s: %+v/%v/%v", k.Caller, got, claimed, err)
		}
	}
	var sealed []byte
	if err := p.QueryRow(t.Context(), `SELECT body_sealed FROM idempotency_keys WHERE caller=$1 AND key=$2`, a.Caller, a.Value).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte(a.Caller)) {
		t.Fatal("account response persisted in plaintext")
	}
	if _, err := p.Exec(t.Context(), `UPDATE idempotency_keys SET body_sealed=$1 WHERE caller=$2 AND key=$3`, sealed, b.Caller, b.Value); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Claim(t.Context(), b, "same-fingerprint"); err == nil || !strings.Contains(err.Error(), "open idempotency key's body") {
		t.Fatalf("cross-account sealed response substitution accepted: %v", err)
	}
	got, _, err := s.Claim(t.Context(), a, "same-fingerprint")
	if err != nil || got.Response == nil || string(got.Response.Body) != a.Caller {
		t.Fatalf("source replay changed after rejected substitution: %+v/%v", got, err)
	}
}
