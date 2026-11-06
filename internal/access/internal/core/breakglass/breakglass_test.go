package breakglass

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
)

func TestUnitHash(t *testing.T) {
	h, err := New("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	s := h.String()
	if !strings.HasPrefix(s, "$pbkdf2-sha256$i=600000$") || strings.Contains(s, "=$") ||
		strings.HasSuffix(s, "=") {
		t.Errorf("String = %s", s)
	}
	got, err := Parse("hash", s)
	if err != nil || !got.Verify("correct horse") || got.Verify("correct horse ") {
		t.Errorf("Parse(%s) = %+v, %v", s, got, err)
	}
	// A higher cost is accepted and still verifies.
	more := Hash{Iterations: Iterations + 1, Salt: h.Salt}
	more.Key = mustKey(t, "pw", more)
	if p, err := Parse("hash", more.String()); err != nil || !p.Verify("pw") {
		t.Errorf("more iterations: %v", err)
	}
}

func mustKey(t *testing.T, pw string, h Hash) []byte {
	t.Helper()
	k, err := pbkdf2.Key(sha256.New, pw, h.Salt, h.Iterations, KeyLen)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestUnitParseRefuses(t *testing.T) {
	salt16 := b64.EncodeToString(make([]byte, SaltLen))
	salt15 := b64.EncodeToString(make([]byte, SaltLen-1))
	key := b64.EncodeToString(make([]byte, KeyLen))
	for name, s := range map[string]string{
		"unknown prefix":   "$argon2id$i=600000$" + salt16 + "$" + key,
		"too few parts":    Prefix + "600000$" + salt16,
		"fewer iterations": Prefix + "599999$" + salt16 + "$" + key,
		"not a number":     Prefix + "many$" + salt16 + "$" + key,
		"short salt":       Prefix + "600000$" + salt15 + "$" + key,
		"padded salt":      Prefix + "600000$" + salt16 + "==$" + key,
		"short hash":       Prefix + "600000$" + salt16 + "$" + b64.EncodeToString(make([]byte, 31)),
	} {
		_, err := Parse("breakGlass.passwordHash", s)
		if !errors.Is(err, ErrInvalid) ||
			!strings.Contains(err.Error(), "breakGlass.passwordHash: ") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
