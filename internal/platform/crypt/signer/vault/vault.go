package vault

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	kekvault "github.com/weaveplatform/infraweave/internal/platform/crypt/kek/vault"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
)

// keyType is the Transit key type that signs ES256.
const keyType = "ecdsa-p256"

// Signer signs with one version of an ecdsa-p256 Transit key.
type Signer struct {
	client      *kekvault.Client
	mount, name string
	version     int
	ref         string
	jwk         jose.JSONWebKey
}

var _ signer.KMS = (*Signer)(nil)

// Options are what a test changes: the KEK provider's.
type Options = kekvault.Options

// New opens ref, <mount>/<key name>/<version>, on the Vault c's address
// and auth method name: the vault KEK provider's settings, whose Transit
// key it does not need. It reads the version's public key once. A
// reference without a version is signer.ErrRef, since a Transit key's
// latest version changes when it rotates while signing_keys holds the
// public half of one; a key that is not ecdsa-p256, or has no such
// version, is signer.ErrKey.
func New(ctx context.Context, c kek.VaultConfig, ref string, o Options) (*Signer, error) {
	s := &Signer{ref: kek.Vault + ":" + ref}
	i := strings.LastIndex(ref, "/")
	j := strings.LastIndex(ref[:max(i, 0)], "/")
	var err error
	if i < 0 || j <= 0 || ref[j+1:i] == "" {
		err = signer.ErrRef
	} else if s.version, err = strconv.Atoi(ref[i+1:]); err != nil || s.version < 1 {
		err = signer.ErrRef
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %s: want <mount>/<key name>/<version>", signer.ErrRef, s.ref)
	}
	s.mount, s.name = ref[:j], ref[j+1:i]
	if s.client, err = kekvault.NewClient(c, o); err != nil {
		return nil, err //nolint:wrapcheck // names its key
	}
	// A key's versions are objects only for an asymmetric type; a
	// symmetric key lists each version's creation time.
	var key struct {
		Type string                     `json:"type"`
		Keys map[string]json.RawMessage `json:"keys"`
	}
	if err := s.client.Do(ctx, http.MethodGet, s.path("keys"), nil, &key); err != nil {
		return nil, fmt.Errorf("%s: read key: %w", s.ref, err)
	}
	if key.Type != keyType {
		return nil, fmt.Errorf("%w: %s is %s, want %s", signer.ErrKey, s.ref, key.Type, keyType)
	}
	//nolint:tagliatelle // Vault's format
	var version struct {
		PublicKey string `json:"public_key"`
	}
	_ = json.Unmarshal(
		key.Keys[strconv.Itoa(s.version)],
		&version,
	) // an absent version is caught below
	block, _ := pem.Decode([]byte(version.PublicKey))
	if block == nil {
		return nil, fmt.Errorf(
			"%w: %s: no public key of version %d",
			signer.ErrKey,
			s.ref,
			s.version,
		)
	}
	pub, err := signer.ParsePKIX(s.ref, block.Bytes)
	if err != nil {
		return nil, err //nolint:wrapcheck // names the reference
	}
	s.jwk = signer.JWK(pub, "")
	return s, nil
}

func (s *Signer) path(op string) string { return s.mount + "/" + op + "/" + url.PathEscape(s.name) }

// Ref is vault:<mount>/<key name>/<version>.
func (s *Signer) Ref() string { return s.ref }

// Public is the key's public JWK, without a kid.
func (s *Signer) Public() jose.JSONWebKey { return s.jwk }

// Sign asks Transit to sign digest as prehashed SHA-256 with the key's
// version, marshaled as JWS's r || s, which Transit answers as
// vault:v<version>:<base64url>.
func (s *Signer) Sign(ctx context.Context, digest []byte) ([]byte, error) {
	if err := signer.CheckDigest(digest); err != nil {
		return nil, err //nolint:wrapcheck // names itself
	}
	var out struct {
		Signature string `json:"signature"`
	}
	if err := s.client.Do(ctx, http.MethodPost, s.path("sign"), map[string]any{
		"input":                base64.StdEncoding.EncodeToString(digest),
		"prehashed":            true,
		"hash_algorithm":       "sha2-256",
		"marshaling_algorithm": "jws",
		"key_version":          s.version,
	}, &out); err != nil {
		return nil, fmt.Errorf("%s: sign: %w", s.ref, err)
	}
	enc, ok := strings.CutPrefix(out.Signature, "vault:v"+strconv.Itoa(s.version)+":")
	if !ok {
		return nil, fmt.Errorf("%s: %w: %q is not of version %d",
			s.ref, signer.ErrSignature, out.Signature, s.version)
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %w", s.ref, signer.ErrSignature, err)
	}
	sig, err := signer.Raw(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.ref, err)
	}
	return sig, nil
}
