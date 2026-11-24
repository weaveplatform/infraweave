package http

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	jose "github.com/go-jose/go-jose/v4"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// publishedKeys is FederationKeys over a fixed answer.
type publishedKeys struct {
	keys []jose.JSONWebKey
	err  error
}

func (p publishedKeys) FederationKeys(context.Context) ([]jose.JSONWebKey, error) {
	return p.keys, p.err
}

func federationKey(t *testing.T, kid string) jose.JSONWebKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return jose.JSONWebKey{Key: &k.PublicKey, KeyID: kid, Algorithm: "ES256", Use: "sig"}
}

// TestUnitFederationDiscoveryRoutes checks both documents at the root and
// under the issuer's path, for GET and HEAD, cacheable JSON, and the
// problem a failed key read answers, which no cache may keep.
func TestUnitFederationDiscoveryRoutes(t *testing.T) {
	keys := publishedKeys{keys: []jose.JSONWebKey{federationKey(t, "b"), federationKey(t, "a")}}
	r := chi.NewRouter()
	NewFederationDiscovery(keys, "https://iw.example.com/federation", "").Routes(r)
	for _, prefix := range []string{"", "/federation"} {
		rec := serveFederation(r, http.MethodGet, prefix+"/.well-known/openid-configuration")
		var doc struct {
			Issuer  string   `json:"issuer"`
			JWKSURI string   `json:"jwks_uri"`                              //nolint:tagliatelle // the discovery document's name
			Algs    []string `json:"id_token_signing_alg_values_supported"` //nolint:tagliatelle // as above
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || rec.Code != http.StatusOK ||
			doc.Issuer != "https://iw.example.com/federation" ||
			doc.JWKSURI != "https://iw.example.com/federation/.well-known/jwks" ||
			fmt.Sprint(doc.Algs) != "[ES256]" {
			t.Errorf("GET %s discovery: %d %s %v", prefix, rec.Code, rec.Body, err)
		}
		cacheable(t, rec)
		rec = serveFederation(r, http.MethodGet, prefix+"/.well-known/jwks")
		var set jose.JSONWebKeySet
		if err := json.Unmarshal(rec.Body.Bytes(), &set); err != nil || rec.Code != http.StatusOK ||
			len(set.Keys) != 2 || set.Keys[0].KeyID != "a" || set.Keys[1].KeyID != "b" {
			t.Errorf("GET %s jwks: %d %s %v", prefix, rec.Code, rec.Body, err)
		}
		cacheable(t, rec)
		for _, doc := range []string{"/.well-known/openid-configuration", "/.well-known/jwks"} {
			if rec := serveFederation(r, http.MethodHead, prefix+doc); rec.Code != http.StatusOK {
				t.Errorf("HEAD %s%s: %d", prefix, doc, rec.Code)
			}
		}
	}

	r = chi.NewRouter()
	NewFederationDiscovery(keys, "https://iw.example.com", "https://keys.example.net/jwks").Routes(r)
	if rec := serveFederation(r, http.MethodGet, "/federation/.well-known/jwks"); rec.Code != http.StatusNotFound {
		t.Errorf("an issuer at the root serves a path: %d", rec.Code)
	}
	if rec := serveFederation(r, http.MethodGet, "/.well-known/openid-configuration"); !json.Valid(rec.Body.Bytes()) ||
		!strings.Contains(rec.Body.String(), `"jwks_uri":"https://keys.example.net/jwks"`) {
		t.Errorf("a custom jwks_uri: %s", rec.Body)
	}

	down := publishedKeys{err: fmt.Errorf("read: %w", kernel.ErrUnavailable)}
	bad := publishedKeys{keys: []jose.JSONWebKey{{KeyID: "none"}}}
	for name, tc := range map[string]struct {
		keys publishedKeys
		path string
		want int
	}{
		"discovery, database down": {down, "/.well-known/openid-configuration", http.StatusServiceUnavailable},
		"jwks, database down":      {down, "/.well-known/jwks", http.StatusServiceUnavailable},
		"jwks, no public half":     {bad, "/.well-known/jwks", http.StatusInternalServerError},
	} {
		r := chi.NewRouter()
		NewFederationDiscovery(tc.keys, "https://iw.example.com", "").Routes(r)
		rec := serveFederation(r, http.MethodGet, tc.path)
		if rec.Code != tc.want || rec.Header().Get("Content-Type") != "application/problem+json" ||
			rec.Header().Get("Cache-Control") == federationCache {
			t.Errorf("%s: %d %v", name, rec.Code, rec.Header())
		}
	}
}

func serveFederation(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, nil))
	return rec
}

func cacheable(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=3600" {
		t.Errorf("Cache-Control %q", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type %q", got)
	}
}
