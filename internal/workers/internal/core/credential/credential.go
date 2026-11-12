package credential

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

var (
	// ErrCSR reports a certificate signing request that does not parse or
	// whose signature does not verify.
	ErrCSR = errors.New("not a certificate signing request")
	// ErrKey reports a request for a key that is not EC P-256.
	ErrKey = errors.New("the pool key is not EC P-256")
	// ErrToken reports a pool token that does not decode.
	ErrToken = errors.New("not a pool token")
	// ErrCA reports a pool CA whose key is not its certificate's.
	ErrCA = errors.New("the pool CA's key is not its certificate's P-256 key")
	// ErrPool reports a pool certificate that names another pool.
	ErrPool = errors.New("the pool certificate names another pool")
)

// Lifetime is how long the pool CA's certificate is valid. A pool's
// certificate ends with it, and a reset, not expiry, is how a pool's
// credentials are replaced.
const Lifetime = 30 * 365 * 24 * time.Hour

// CA is the installation's pool CA: its certificate and private key.
type CA struct {
	Certificate *x509.Certificate
	Key         *ecdsa.PrivateKey
}

// NewCA creates a pool CA with a new P-256 key, valid from now for
// Lifetime.
func NewCA(now time.Time) (CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return CA{}, fmt.Errorf("pool CA key: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "infraweave worker pool CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(Lifetime),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return CA{}, fmt.Errorf("pool CA certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return CA{}, fmt.Errorf("pool CA certificate: %w", err)
	}
	return CA{Certificate: cert, Key: key}, nil
}

// Marshal is the CA as the store keeps it: its certificate's DER and its
// key's PKCS #8 DER, which the store seals.
func (ca CA) Marshal() (cert, key []byte, err error) {
	key, err = x509.MarshalPKCS8PrivateKey(ca.Key)
	if err != nil {
		return nil, nil, fmt.Errorf("pool CA key: %w", err)
	}
	return ca.Certificate.Raw, key, nil
}

// ParseCA is the CA Marshal kept.
func ParseCA(cert, key []byte) (CA, error) {
	c, err := x509.ParseCertificate(cert)
	if err != nil {
		return CA{}, fmt.Errorf("pool CA certificate: %w", err)
	}
	k, err := x509.ParsePKCS8PrivateKey(key)
	if err != nil {
		return CA{}, fmt.Errorf("pool CA key: %w", err)
	}
	ec, ok := k.(*ecdsa.PrivateKey)
	if !ok || !ec.PublicKey.Equal(c.PublicKey) {
		return CA{}, ErrCA
	}
	return CA{Certificate: c, Key: ec}, nil
}

// ParseCSR reads a certificate signing request given as base64 of its PEM
// or DER, or as PEM, and checks its signature; ErrCSR when it is none,
// ErrKey when its key is not EC P-256.
//
// ref: decision 0008 § "The worker API"
func ParseCSR(in string) (*x509.CertificateRequest, error) {
	raw := []byte(strings.TrimSpace(in))
	if !bytes.HasPrefix(raw, []byte("-----BEGIN")) {
		dec, err := base64.StdEncoding.DecodeString(string(raw))
		if err != nil {
			return nil, fmt.Errorf("%w: neither PEM nor base64", ErrCSR)
		}
		// DER is not trimmed: its last byte may be one TrimSpace drops.
		raw = dec
	}
	if b, _ := pem.Decode(raw); b != nil {
		if b.Type != "CERTIFICATE REQUEST" && b.Type != "NEW CERTIFICATE REQUEST" {
			return nil, fmt.Errorf("%w: a PEM %s block", ErrCSR, b.Type)
		}
		raw = b.Bytes
	}
	csr, err := x509.ParseCertificateRequest(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCSR, err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCSR, err)
	}
	if k, ok := csr.PublicKey.(*ecdsa.PublicKey); !ok || k.Curve != elliptic.P256() {
		return nil, ErrKey
	}
	return csr, nil
}

// Sign issues the pool's certificate for csr's key, with serial, valid
// from now until the CA's certificate ends. Its subject names the pool,
// so a certificate signed for one pool is never another's.
func (ca CA) Sign(
	csr *x509.CertificateRequest,
	pool kernel.ULID,
	serial int64,
	now time.Time,
) (*x509.Certificate, error) {
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: pool.String()},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     ca.Certificate.NotAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyAgreement,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Certificate, csr.PublicKey, ca.Key)
	if err != nil {
		return nil, fmt.Errorf("sign pool %s's certificate: %w", pool, err)
	}
	return x509.ParseCertificate(der) //nolint:wrapcheck // DER CreateCertificate made
}

// Verify checks that cert chains to the CA and names pool; the caller
// checks its serial against the pool's.
func (ca CA) Verify(cert *x509.Certificate, pool kernel.ULID, now time.Time) error {
	roots := x509.NewCertPool()
	roots.AddCert(ca.Certificate)
	if _, err := cert.Verify(x509.VerifyOptions{
		Roots: roots, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		return fmt.Errorf("pool certificate: %w", err)
	}
	if cert.Subject.CommonName != pool.String() {
		return fmt.Errorf("%w: %q, not %s", ErrPool, cert.Subject.CommonName, pool)
	}
	return nil
}

// TokenVersion is the pool token format's version.
const TokenVersion = 1

// Token is a pool token's content: the server's public URL its launchers
// reach, the pool, and the pool's certificate in PEM.
//
// ref: decision 0008 § "The worker API"
type Token struct {
	Version     int    `json:"version"`
	Endpoint    string `json:"endpoint"`
	Pool        string `json:"pool"`
	Certificate string `json:"certificate"`
}

// NewToken is the pool token of pool's certificate der, reached at
// endpoint.
func NewToken(endpoint string, pool kernel.ULID, der []byte) Token {
	return Token{
		Version: TokenVersion, Endpoint: endpoint, Pool: pool.String(),
		Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
	}
}

// Encode is t as the API answers it: base64 of its JSON.
func (t Token) Encode() string {
	// A struct of strings and an int always marshals.
	b, _ := json.Marshal(t) //nolint:errchkjson // as above
	return base64.StdEncoding.EncodeToString(b)
}

// DecodeToken reads a pool token Encode wrote and parses its certificate.
func DecodeToken(s string) (Token, *x509.Certificate, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return Token{}, nil, fmt.Errorf("%w: %w", ErrToken, err)
	}
	var t Token
	if err := json.Unmarshal(b, &t); err != nil {
		return Token{}, nil, fmt.Errorf("%w: %w", ErrToken, err)
	}
	block, _ := pem.Decode([]byte(t.Certificate))
	if block == nil || block.Type != "CERTIFICATE" {
		return Token{}, nil, fmt.Errorf("%w: no certificate", ErrToken)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return Token{}, nil, fmt.Errorf("%w: %w", ErrToken, err)
	}
	return t, cert, nil
}
