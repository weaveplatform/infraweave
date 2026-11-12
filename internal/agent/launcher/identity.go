package launcher

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// TokenVersion is the pool token format the launcher reads.
const TokenVersion = 1

// ErrIdentity marks a pool token or private key the launcher cannot
// register with.
var ErrIdentity = errors.New("launcher: the pool token or private key")

// identity is the pool a launcher registers with: the server's public
// URL, the pool's ID and certificate from its pool token, and the pool's
// private key, which signs registrations and opens specifications.
type identity struct {
	endpoint string
	pool     string
	cert     *x509.Certificate
	key      *ecdsa.PrivateKey
	open     *ecdh.PrivateKey
}

// poolToken is a pool token's content, base64 of this JSON.
type poolToken struct {
	Version     int    `json:"version"`
	Endpoint    string `json:"endpoint"`
	Pool        string `json:"pool"`
	Certificate string `json:"certificate"`
}

// newIdentity reads the pool token and the private key's PEM, and checks
// that the key is the P-256 key the token's certificate certifies.
func newIdentity(token string, keyPEM []byte) (identity, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil {
		return identity{}, fmt.Errorf("%w: the pool token is not base64: %w", ErrIdentity, err)
	}
	var t poolToken
	if err := json.Unmarshal(raw, &t); err != nil {
		return identity{}, fmt.Errorf("%w: the pool token is not JSON: %w", ErrIdentity, err)
	}
	if t.Version != TokenVersion || t.Endpoint == "" || t.Pool == "" {
		return identity{}, fmt.Errorf("%w: a pool token of version %d names its endpoint and pool",
			ErrIdentity, TokenVersion)
	}
	block, _ := pem.Decode([]byte(t.Certificate))
	if block == nil || block.Type != "CERTIFICATE" {
		return identity{}, fmt.Errorf("%w: the pool token holds no certificate", ErrIdentity)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return identity{}, fmt.Errorf("%w: the pool token's certificate: %w", ErrIdentity, err)
	}
	key, err := privateKey(keyPEM)
	if err != nil {
		return identity{}, err
	}
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || !pub.Equal(key.Public()) {
		return identity{}, fmt.Errorf("%w: the private key is not the one the pool token certifies",
			ErrIdentity)
	}
	open, err := key.ECDH()
	if err != nil {
		return identity{}, fmt.Errorf("%w: %w", ErrIdentity, err)
	}
	return identity{endpoint: t.Endpoint, pool: t.Pool, cert: cert, key: key, open: open}, nil
}

// privateKey parses a P-256 private key from SEC 1 or PKCS #8 PEM.
func privateKey(b []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, fmt.Errorf("%w: the private key is not PEM", ErrIdentity)
	}
	var key any
	var err error
	switch block.Type {
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(block.Bytes)
	case "PRIVATE KEY":
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	default:
		return nil, fmt.Errorf("%w: a %s is not a P-256 private key", ErrIdentity, block.Type)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: the private key: %w", ErrIdentity, err)
	}
	ec, ok := key.(*ecdsa.PrivateKey)
	if !ok || ec.Curve != elliptic.P256() {
		return nil, fmt.Errorf("%w: the pool's key is a P-256 key", ErrIdentity)
	}
	return ec, nil
}

// claims are a registration's payload, as the worker API describes it.
type claims struct {
	Pool     string            `json:"pool"`
	IssuedAt int64             `json:"iat"`
	ID       string            `json:"jti"`
	Version  string            `json:"version"`
	OS       string            `json:"os"`
	Arch     string            `json:"arch"`
	Metadata map[string]string `json:"metadata"`
	Worker   string            `json:"worker,omitempty"`
}

// sign is the compact ES256 JWS of c, with the pool's certificate in x5c
// and a fresh jti, which the server refuses to see twice.
func (id identity) sign(c claims, now time.Time) (string, error) {
	jti := make([]byte, 16)
	_, _ = rand.Read(jti) // crypto/rand.Read never fails
	c.Pool, c.IssuedAt, c.ID = id.pool, now.Unix(), hex.EncodeToString(jti)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: id.key},
		(&jose.SignerOptions{}).WithHeader("x5c",
			[]string{base64.StdEncoding.EncodeToString(id.cert.Raw)}))
	if err != nil {
		return "", fmt.Errorf("sign the registration: %w", err)
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("sign the registration: %w", err)
	}
	obj, err := signer.Sign(payload)
	if err != nil {
		return "", fmt.Errorf("sign the registration: %w", err)
	}
	raw, err := obj.CompactSerialize()
	if err != nil {
		return "", fmt.Errorf("sign the registration: %w", err)
	}
	return raw, nil
}
