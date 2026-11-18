package webhookauth

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

// ErrSignature reports refusal without exposing a header or secret value.
var ErrSignature = errors.New("webhook signature refused")

// Verify checks one exact sha256= header under each supplied secret. It
// retains the original header rather than a re-encoded equivalent for replay.
func Verify(header string, h http.Header, body []byte, secrets [][]byte) ([]byte, error) {
	values := h.Values(header)
	if len(values) != 1 || len(values[0]) != len("sha256=")+2*sha256.Size || !strings.HasPrefix(values[0], "sha256=") {
		return nil, ErrSignature
	}
	got, err := hex.DecodeString(strings.TrimPrefix(values[0], "sha256="))
	if err != nil {
		return nil, ErrSignature
	}
	matched := 0
	for _, secret := range secrets {
		// Strict FIPS requires at least 112 key bits. Refuse an ineligible
		// candidate before hmac.New can panic; never change or pad the key.
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
