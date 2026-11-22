package token_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestUnitWorkerSerialIsSignedPositiveAndWorkerOnly(t *testing.T) {
	svc, store, _ := setup(t)
	r := request()
	r.Kind, r.Subject, r.Audience, r.WorkerSerial = token.KindWorker, kernel.NewULID().String(), "infraweave-worker", 7
	raw, issued, err := svc.Issue(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := svc.Verify(t.Context(), raw, r.Audience)
	if err != nil || verified.WorkerSerial != 7 || issued.WorkerSerial != 7 {
		t.Fatalf("worker generation round trip: %+v %v", verified, err)
	}
	if got := segment(t, raw, 1)["iw_worker_serial"]; got != float64(7) {
		t.Fatal("worker generation was not signed")
	}
	for _, serial := range []int64{0, -1} {
		bad := r
		bad.WorkerSerial = serial
		if _, _, err := svc.Issue(t.Context(), bad); !errors.Is(err, token.ErrRequest) {
			t.Fatal("issued unversioned worker token")
		}
	}
	for _, kind := range token.Kinds {
		if kind == token.KindWorker {
			continue
		}
		bad := r
		bad.Kind = kind
		if _, _, err := svc.Issue(t.Context(), bad); !errors.Is(err, token.ErrRequest) {
			t.Fatalf("serial admitted for %s", kind)
		}
	}
	kid := segment(t, raw, 0)["kid"].(string)
	for name, serial := range map[string]any{"null": nil, "zero": 0, "negative": -1, "fraction": 1.5, "text": "7", "overflow": json.Number("9223372036854775808")} {
		claims := segment(t, raw, 1)
		claims["iw_worker_serial"] = serial
		forged := forge(t, store, kid, jose.ES256, nil, claims)
		if _, err := svc.Verify(t.Context(), forged, r.Audience); !errors.Is(err, token.ErrToken) {
			t.Fatalf("%s serial accepted: %v", name, err)
		}
	}
	legacy := segment(t, raw, 1)
	delete(legacy, "iw_worker_serial")
	if _, err := svc.Verify(t.Context(), forge(t, store, kid, jose.ES256, nil, legacy), r.Audience); !errors.Is(err, token.ErrToken) {
		t.Fatal("legacy unversioned worker token accepted")
	}
	for _, serial := range []any{0, 7, nil} {
		claims := segment(t, raw, 1)
		claims["iw_kind"] = "runner"
		claims["aud"] = "infraweave-runner"
		claims["iw_worker_serial"] = serial
		_, err := svc.Verify(t.Context(), forge(t, store, kid, jose.ES256, nil, claims), "infraweave-runner")
		if !errors.Is(err, token.ErrToken) {
			t.Fatal("worker serial accepted for runner")
		}
	}
	// Renewal changes only times/jti, retaining the generation supplied by its owner.
	r.TTL = 2 * time.Hour
	_, renewed, err := svc.Issue(t.Context(), r)
	if err != nil || renewed.WorkerSerial != 7 {
		t.Fatal("renewal changed credential generation")
	}
}
