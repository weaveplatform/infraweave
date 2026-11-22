package controller

import (
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

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// The fixed keys of a WorkerPool's credential Secret.
const (
	TokenKey      = "token"
	PrivateKeyKey = "privateKey"
)

// tokenVersion is the pool token format the controller reads, as the
// launcher does.
const tokenVersion = 1

// ErrCredentials marks a pool token or private key the controller refuses
// to act with: missing, malformed, or not the pair the resource names.
var ErrCredentials = errors.New("worker controller: pool credentials")

// poolToken is a pool token's content, base64 of this JSON.
type poolToken struct {
	Version     int    `json:"version"`
	Endpoint    string `json:"endpoint"`
	Pool        string `json:"pool"`
	Certificate string `json:"certificate"`
}

// identity is a pool's signing identity: its ID, the certificate its token
// carries and the private key that certificate certifies.
type identity struct {
	pool string
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

// privateKey parses a P-256 private key from SEC 1 or PKCS #8 PEM.
func privateKey(b []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, fmt.Errorf("%w: the private key is not PEM", ErrCredentials)
	}
	var key any
	var err error
	switch block.Type {
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(block.Bytes)
	case "PRIVATE KEY":
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	default:
		return nil, fmt.Errorf("%w: a %s is not a P-256 private key", ErrCredentials, block.Type)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: the private key: %w", ErrCredentials, err)
	}
	ec, ok := key.(*ecdsa.PrivateKey)
	if !ok || ec.Curve != elliptic.P256() {
		return nil, fmt.Errorf("%w: the pool key is not a P-256 key", ErrCredentials)
	}
	return ec, nil
}

// newIdentity reads token, a pool token, for key, and refuses a token of
// another pool than want, when want is set, or whose certificate certifies
// another key.
func newIdentity(token string, key *ecdsa.PrivateKey, want string) (identity, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil {
		return identity{}, fmt.Errorf("%w: the pool token is not base64", ErrCredentials)
	}
	var t poolToken
	if err := json.Unmarshal(raw, &t); err != nil || t.Version != tokenVersion || t.Pool == "" {
		return identity{}, fmt.Errorf("%w: the pool token is not a version %d token naming its pool", ErrCredentials, tokenVersion)
	}
	if want != "" && t.Pool != want {
		return identity{}, fmt.Errorf("%w: the pool token names another pool", ErrCredentials)
	}
	block, _ := pem.Decode([]byte(t.Certificate))
	if block == nil || block.Type != "CERTIFICATE" {
		return identity{}, fmt.Errorf("%w: the pool token holds no certificate", ErrCredentials)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return identity{}, fmt.Errorf("%w: the pool token's certificate: %w", ErrCredentials, err)
	}
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || !pub.Equal(key.Public()) {
		return identity{}, fmt.Errorf("%w: the private key is not the one the pool token certifies", ErrCredentials)
	}
	return identity{pool: t.Pool, cert: cert, key: key}, nil
}

// csr is the base64 DER certificate signing request of key, which a
// Managed pool's creation sends; the private key never leaves the cluster.
func csr(key *ecdsa.PrivateKey) (string, error) {
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	if err != nil {
		return "", fmt.Errorf("%w: certificate signing request: %w", ErrCredentials, err)
	}
	return base64.StdEncoding.EncodeToString(der), nil
}

// claims are the payload of a registration or an ownership proof.
type claims struct {
	Pool         string             `json:"pool"`
	IssuedAt     int64              `json:"iat"`
	ID           string             `json:"jti"`
	Owner        kernel.WorkerOwner `json:"owner"`
	Capabilities []string           `json:"capabilities,omitempty"`
	Version      string             `json:"version,omitempty"`
	OS           string             `json:"os,omitempty"`
	Arch         string             `json:"arch,omitempty"`
	Metadata     map[string]string  `json:"metadata,omitempty"`
	Slot         *int32             `json:"slot,omitempty"`
}

// sign is the compact ES256 JWS of c with the pool certificate in x5c, a
// fresh jti and now as its iat, so every retry is a new proof the server
// has not seen.
func (id identity) sign(c claims, now time.Time) (string, error) {
	jti := make([]byte, 16)
	_, _ = rand.Read(jti) // crypto/rand.Read never fails
	c.Pool, c.IssuedAt, c.ID = id.pool, now.Unix(), hex.EncodeToString(jti)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: id.key},
		(&jose.SignerOptions{}).WithHeader("x5c", []string{base64.StdEncoding.EncodeToString(id.cert.Raw)}))
	if err != nil {
		return "", fmt.Errorf("worker controller: sign: %w", err)
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("worker controller: sign: %w", err)
	}
	obj, err := signer.Sign(payload)
	if err != nil {
		return "", fmt.Errorf("worker controller: sign: %w", err)
	}
	return obj.CompactSerialize() //nolint:wrapcheck // go-jose's serialization of a signed object does not fail
}
