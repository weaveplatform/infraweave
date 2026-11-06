package breakglass

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The hash's parameters (decision 0006): OWASP's PBKDF2-HMAC-SHA256
// iteration count, the salt NIST SP 800-132 asks for, and SHA-256's
// output length.
const (
	Iterations = 600_000
	SaltLen    = 16
	KeyLen     = 32
	// Prefix starts every hash: the scheme, then the iteration field's
	// name.
	Prefix = "$pbkdf2-sha256$i="
)

// ErrInvalid reports a hash in the wrong format or below the parameters;
// the error reads "<field>: <reason>", as organization's field errors do,
// so a 422 lists every field the same way.
var ErrInvalid = errors.New("invalid password hash")

type fieldError struct{ field, reason string }

func (e *fieldError) Error() string        { return e.field + ": " + e.reason }
func (e *fieldError) Is(target error) bool { return target == ErrInvalid }

// Hash is a parsed break-glass password hash.
type Hash struct {
	Iterations int
	Salt, Key  []byte
}

// b64 is unpadded standard base64, as the format writes salt and hash.
var b64 = base64.RawStdEncoding

// New hashes password under a fresh salt.
func New(password string) (Hash, error) {
	salt := make([]byte, SaltLen)
	_, _ = rand.Read(salt) // crypto/rand never fails
	key, err := pbkdf2.Key(sha256.New, password, salt, Iterations, KeyLen)
	if err != nil {
		return Hash{}, fmt.Errorf("hash password: %w", err)
	}
	return Hash{Iterations: Iterations, Salt: salt, Key: key}, nil
}

// String is the hash in decision 0006's format,
// $pbkdf2-sha256$i=<iterations>$<salt>$<hash>.
func (h Hash) String() string {
	return Prefix + strconv.Itoa(h.Iterations) + "$" + b64.EncodeToString(h.Salt) + "$" +
		b64.EncodeToString(h.Key)
}

// Parse reads s in decision 0006's format. An unknown prefix, fewer than
// Iterations iterations, a salt shorter than SaltLen or a hash that is not
// KeyLen bytes is ErrInvalid naming field; more iterations are accepted,
// so the cost can rise without a release.
func Parse(field, s string) (Hash, error) {
	invalid := func(why string) (Hash, error) {
		return Hash{}, &fieldError{field, why}
	}
	rest, ok := strings.CutPrefix(s, Prefix)
	if !ok {
		return invalid("does not start with " + Prefix)
	}
	parts := strings.Split(rest, "$")
	if len(parts) != 3 {
		return invalid("is not " + Prefix + "<iterations>$<salt>$<hash>")
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil || n < Iterations {
		return invalid(fmt.Sprintf("has %q iterations, fewer than %d", parts[0], Iterations))
	}
	salt, err := b64.DecodeString(parts[1])
	if err != nil || len(salt) < SaltLen {
		return invalid(fmt.Sprintf("has a salt that is not %d or more bytes in unpadded base64",
			SaltLen))
	}
	key, err := b64.DecodeString(parts[2])
	if err != nil || len(key) != KeyLen {
		return invalid(fmt.Sprintf("has a hash that is not %d bytes in unpadded base64", KeyLen))
	}
	return Hash{Iterations: n, Salt: salt, Key: key}, nil
}

// Verify reports whether password hashes to h, comparing in constant time.
func (h Hash) Verify(password string) bool {
	key, err := pbkdf2.Key(sha256.New, password, h.Salt, h.Iterations, len(h.Key))
	return err == nil && subtle.ConstantTimeCompare(key, h.Key) == 1
}

// Credential is an organization's break-glass credential: the login the
// break-glass admin signs in with and the hash of its password. A
// credential read back for the operator has a zero Hash, as no answer
// carries it.
type Credential struct {
	Login     string
	Hash      Hash
	UpdatedAt time.Time
}
