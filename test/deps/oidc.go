package deps

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/go-chi/chi/v5"
)

// oidcKeyID names the issuer's one signing key in its JWKS and its tokens.
const oidcKeyID = "acceptance"

// Issuer is the @oidc dependency: an OpenID Connect issuer on loopback,
// over TLS as infraweave fetches issuers over HTTPS only, that publishes
// its discovery document and JWKS and mints RS256 tokens with whatever
// claims a scenario chooses. A server trusts it through CertPEM. It runs in
// process, so @oidc scenarios need no Docker.
type Issuer struct {
	srv *httptest.Server
	key *rsa.PrivateKey
}

// StartIssuer starts an issuer with a fresh 2048-bit key.
func StartIssuer() (*Issuer, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("oidc key: %w", err)
	}
	iss := &Issuer{key: key}
	r := chi.NewRouter()
	r.Get("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                                iss.URL(),
			"jwks_uri":                              iss.URL() + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"response_types_supported":              []string{"id_token"},
			"subject_types_supported":               []string{"public"},
		})
	})
	r.Get("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"keys": []map[string]string{{
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"kid": oidcKeyID,
			"n":   b64(key.N.Bytes()),
			"e":   b64(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	iss.srv = httptest.NewTLSServer(r)
	return iss, nil
}

// URL is the issuer identifier, which is also its base URL.
func (i *Issuer) URL() string { return i.srv.URL }

// CertPEM is the issuer's certificate, which a client must trust to reach
// it.
func (i *Issuer) CertPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: i.srv.Certificate().Raw})
}

// Client is an HTTP client that trusts the issuer.
func (i *Issuer) Client() *http.Client { return i.srv.Client() }

// Close stops the issuer.
func (i *Issuer) Close() { i.srv.Close() }

// Mint signs a token with the standard claims (iss, iat, exp an hour on)
// and claims, which override them.
func (i *Issuer) Mint(claims map[string]any) (string, error) {
	now := time.Now()
	all := map[string]any{"iss": i.URL(), "iat": now.Unix(), "exp": now.Add(time.Hour).Unix()}
	for k, v := range claims {
		all[k] = v
	}
	header, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": oidcKeyID})
	if err != nil {
		return "", fmt.Errorf("oidc header: %w", err)
	}
	payload, err := json.Marshal(all)
	if err != nil {
		return "", fmt.Errorf("oidc claims: %w", err)
	}
	signing := b64(header) + "." + b64(payload)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, i.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("oidc sign: %w", err)
	}
	return signing + "." + b64(sig), nil
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
