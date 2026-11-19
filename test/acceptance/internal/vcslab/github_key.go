package vcslab

import (
	"crypto/hkdf"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const appHookContext = "infraweave/vcs-lab/github-app-webhook/v1"

// ErrAppKey refuses malformed or weak lab credentials without revealing them.
var ErrAppKey = errors.New("vcs lab: invalid GitHub App key")

// AppKey is the dedicated App's stable lab signing secret and its public-key
// readiness fingerprint. Formatting never reveals the derived secret.
type AppKey struct {
	secret      string
	Fingerprint string
	Readiness   string
}

func (AppKey) String() string { return "[lab App signing key]" }

// Format also protects nested %#v formatting, which bypasses String methods.
func (AppKey) Format(s fmt.State, _ rune) { _, _ = s.Write([]byte("[lab App signing key]")) }

// Secret is supplied explicitly to the served integration create operation.
// GitHub itself receives this value only during the maintainer's one-time setup.
func (k AppKey) Secret() string { return k.secret }

// DeriveAppKey normalizes PKCS1 and PKCS8 encodings of the same RSA key before
// HKDF-SHA256. The versioned purpose and numeric App identity separate this
// signing key from every other use of the existing credential. Key rotation
// changes the readiness fingerprint and requires deliberate operator setup.
func DeriveAppKey(appID string, encoded []byte) (AppKey, error) {
	id, err := strconv.ParseInt(appID, 10, 64)
	if err != nil || id <= 0 {
		return AppKey{}, ErrAppKey
	}
	block, rest := pem.Decode(encoded)
	if block == nil || strings.TrimSpace(string(rest)) != "" {
		return AppKey{}, ErrAppKey
	}
	var key *rsa.PrivateKey
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		var parsed any
		parsed, err = x509.ParsePKCS8PrivateKey(block.Bytes)
		key, _ = parsed.(*rsa.PrivateKey)
	default:
		return AppKey{}, ErrAppKey
	}
	if err != nil || key == nil || key.N.BitLen() < 2048 || key.Validate() != nil {
		return AppKey{}, ErrAppKey
	}
	canonical, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return AppKey{}, ErrAppKey
	}
	defer clear(canonical)
	derived, err := hkdf.Key(sha256.New, canonical, nil, appHookContext+"/"+strconv.FormatInt(id, 10), 32)
	if err != nil {
		return AppKey{}, ErrAppKey
	}
	defer clear(derived)
	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return AppKey{}, ErrAppKey
	}
	fingerprint := sha256.Sum256(public)
	metadata := append([]byte(appHookContext+"/"+strconv.FormatInt(id, 10)+"\x00"), public...)
	readiness := sha256.Sum256(metadata)
	return AppKey{secret: hex.EncodeToString(derived), Fingerprint: hex.EncodeToString(fingerprint[:]), Readiness: "v1:" + hex.EncodeToString(readiness[:])}, nil
}
