package signed

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Kind is what a token lets its bearer do, and the first byte of its
// encoding.
type Kind byte

// The kinds of token. Each signs under a key derived for its own purpose,
// so a token of one kind never verifies as the other.
//
// ref: decision 0013 § "Derived keys"
const (
	// Upload lets its bearer PUT one state file for import.
	Upload Kind = 'U'
	// Download lets its bearer GET one state version.
	Download Kind = 'D'
)

// The purposes the two kinds derive their keys for.
const (
	UploadPurpose   = "state-upload-url"
	DownloadPurpose = "state-download-url"
)

func (k Kind) purpose() (string, bool) {
	switch k {
	case Upload:
		return UploadPurpose, true
	case Download:
		return DownloadPurpose, true
	}
	return "", false
}

// Token is what a signed URL grants. Subject is the principal that asked
// for it, as kernel.Principal.Caller names one. An upload's target is
// Object, the ID its import names; a download's is Stack and Version, the
// stack's current version when the URL was signed.
type Token struct {
	Kind    Kind
	Account kernel.AccountID
	Subject string
	Object  kernel.ULID
	Stack   kernel.ULID
	Version kernel.ULID
	Expires time.Time
}

// ErrToken reports a token that is malformed, altered, expired or of
// another kind.
var ErrToken = errors.New("invalid signed state URL")

// Keys derive the tokens' keys; crypt.Keyring is one. DerivedKey takes
// the data key ID a token carries, so a token signed before a rotation
// verifies until it expires.
type Keys interface {
	DeriveKey(purpose string) ([]byte, uint32, error)
	DerivedKey(purpose string, id uint32) ([]byte, error)
}

// Signer signs and verifies tokens under keys, at the time now gives.
type Signer struct {
	keys Keys
	now  func() time.Time
}

// New returns a Signer over keys at the time now gives.
func New(keys Keys, now func() time.Time) Signer { return Signer{keys: keys, now: now} }

// header is kind (1) | data key ID (4) | account (16) | subject length (2).
const header = 1 + 4 + 16 + 2

// tail is expiry (8, Unix seconds) | HMAC-SHA-256 (32).
const tail = 8 + sha256.Size

// targetSize is the bytes of k's target: an upload's object ID, or a
// download's stack and version IDs.
func targetSize(k Kind) int {
	if k == Download {
		return 32
	}
	return 16
}

// Sign encodes t, expiring ttl from now, as kind | data key ID | account |
// subject | target | expiry, then the HMAC-SHA-256 of all of it under
// the key t.Kind derives, in unpadded base64url. Integers are big-endian
// and the subject carries its length.
//
// ref: decision 0010 § "Upload, import and download URLs"
func (s Signer) Sign(t Token, ttl time.Duration) (string, error) {
	purpose, ok := t.Kind.purpose()
	if !ok {
		return "", fmt.Errorf("%w: kind %q", ErrToken, byte(t.Kind))
	}
	if len(t.Subject) > math.MaxUint16 {
		return "", fmt.Errorf("%w: a %d-byte subject", ErrToken, len(t.Subject))
	}
	key, id, err := s.keys.DeriveKey(purpose)
	if err != nil {
		return "", fmt.Errorf("state: sign a URL: %w", err)
	}
	b := make([]byte, 0, header+len(t.Subject)+targetSize(t.Kind)+tail)
	b = append(b, byte(t.Kind))
	b = binary.BigEndian.AppendUint32(b, id)
	b = append(b, t.Account.ULID[:]...)
	b = binary.BigEndian.AppendUint16(b, uint16(len(t.Subject))) //nolint:gosec // bounded above
	b = append(b, t.Subject...)
	if t.Kind == Download {
		b = append(b, t.Stack[:]...)
		b = append(b, t.Version[:]...)
	} else {
		b = append(b, t.Object[:]...)
	}
	b = binary.BigEndian.AppendUint64(b, uint64(s.now().Add(ttl).Unix())) //nolint:gosec // two's complement
	h := hmac.New(sha256.New, key)
	h.Write(b)
	return base64.RawURLEncoding.EncodeToString(h.Sum(b)), nil
}

// Verify decodes raw as a token of kind, checking its MAC under the key
// derived from the data key it names, active or retiring, and its expiry.
// The decoding is strict, so a token has one spelling: a lenient decoder
// ignores the unused bits of the last character, and a token differing
// there would verify.
func (s Signer) Verify(kind Kind, raw string) (Token, error) {
	purpose, ok := kind.purpose()
	if !ok {
		return Token{}, ErrToken
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(b) < header+tail || Kind(b[0]) != kind {
		return Token{}, ErrToken
	}
	body, sum := b[:len(b)-sha256.Size], b[len(b)-sha256.Size:]
	key, err := s.keys.DerivedKey(purpose, binary.BigEndian.Uint32(body[1:]))
	if err != nil {
		return Token{}, ErrToken
	}
	h := hmac.New(sha256.New, key)
	h.Write(body)
	if !hmac.Equal(h.Sum(nil), sum) {
		return Token{}, ErrToken
	}
	n := int(binary.BigEndian.Uint16(body[21:]))
	if len(body) != header+n+targetSize(kind)+8 {
		return Token{}, ErrToken
	}
	t := Token{Kind: kind, Subject: string(body[header : header+n])}
	copy(t.Account.ULID[:], body[5:21])
	at := body[header+n:]
	if kind == Download {
		copy(t.Stack[:], at[:16])
		copy(t.Version[:], at[16:32])
	} else {
		copy(t.Object[:], at[:16])
	}
	t.Expires = time.Unix(int64(binary.BigEndian.Uint64(body[len(body)-8:])), 0) //nolint:gosec // two's complement
	if !s.now().Before(t.Expires) {
		return Token{}, fmt.Errorf("%w: expired", ErrToken)
	}
	return t, nil
}
