package github

import (
	"crypto/fips140"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
)

// ErrSignature is a missing or invalid SHA-256 delivery signature. It
// deliberately carries neither the secret nor the received signature.
var ErrSignature = errors.New("github: webhook signature refused")

// Verify checks the exact body under every supplied active secret. The
// caller owns the previous-secret window. It retains the verified header
// verbatim, including its algorithm prefix, for delivery replay.
func (*Provider) Verify(h http.Header, body []byte, secrets [][]byte) ([]byte, error) {
	values := h.Values("X-Hub-Signature-256")
	if len(values) != 1 || len(values[0]) != len("sha256=")+2*sha256.Size || !strings.HasPrefix(values[0], "sha256=") {
		return nil, ErrSignature
	}
	got, err := hex.DecodeString(strings.TrimPrefix(values[0], "sha256="))
	if err != nil {
		return nil, ErrSignature
	}
	matched := 0
	for _, secret := range secrets {
		// Strict FIPS requires at least 112 key bits. Check before hmac.New
		// so a configured short key produces the ordinary verification refusal.
		if len(secret) == 0 || (fips140.Enforced() && len(secret) < 14) {
			continue
		}
		m := hmac.New(sha256.New, secret)
		_, _ = m.Write(body)
		matched |= subtle.ConstantTimeCompare(got, m.Sum(nil))
	}
	if matched != 1 {
		return nil, ErrSignature
	}
	return []byte(values[0]), nil
}
