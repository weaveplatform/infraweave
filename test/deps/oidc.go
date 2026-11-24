package deps

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
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
//
// Its JWKS carries the signing key's self-signed certificate as x5c and its
// SHA-256 thumbprint as x5t#S256, as Entra ID, Auth0 and GitHub Actions
// publish theirs, so every @oidc scenario parses a key set with certificate
// members; under the certified FIPS mode a parser that digests x5c with
// SHA-1 fails them. It publishes no x5t, which is a SHA-1 digest this
// process cannot compute in that mode.
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
	cert, err := selfSigned(key)
	if err != nil {
		return nil, err
	}
	thumb := sha256.Sum256(cert)
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
		writeJSON(w, map[string]any{"keys": []map[string]any{{
			"kty":      "RSA",
			"use":      "sig",
			"alg":      "RS256",
			"kid":      oidcKeyID,
			"n":        b64(key.N.Bytes()),
			"e":        b64(big.NewInt(int64(key.E)).Bytes()),
			"x5c":      []string{base64.StdEncoding.EncodeToString(cert)},
			"x5t#S256": b64(thumb[:]),
		}}})
	})
	iss.srv = httptest.NewTLSServer(r)
	return iss, nil
}

// selfSigned is the DER of a self-signed certificate for key, the x5c an
// issuer publishes beside the key.
func selfSigned(key *rsa.PrivateKey) ([]byte, error) {
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "acceptance issuer"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("oidc certificate: %w", err)
	}
	return der, nil
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
