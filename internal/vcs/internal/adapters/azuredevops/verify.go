package azuredevops

import (
	"crypto/fips140"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
)

var errWebhookBasic = errors.New("azure devops: webhook credential refused")

// Verify requires Basic credentials with an empty username. Every allowed
// password is compared before deciding; the retained signature binds the body
// without retaining the Authorization header that carried the password.
func (*Provider) Verify(headers http.Header, body []byte, secrets [][]byte) ([]byte, error) {
	request := http.Request{Header: headers}
	username, password, ok := request.BasicAuth()
	got := sha256.Sum256([]byte(password))
	var matched []byte
	for _, secret := range secrets {
		want := sha256.Sum256(secret)
		if subtle.ConstantTimeCompare(got[:], want[:]) == 1 && len(secret) != 0 {
			matched = secret
		}
	}
	if !ok || len(headers.Values("Authorization")) != 1 || username != "" || password == "" || matched == nil {
		return nil, errWebhookBasic
	}
	// FIPS-only HMAC requires at least 112 key bits. Invalid configured keys
	// are verification refusals, not panics or silently transformed passwords.
	if fips140.Enforced() && len(matched) < 112/8 {
		return nil, errWebhookBasic
	}
	digest := hmac.New(sha256.New, matched)
	_, _ = digest.Write(body)
	return []byte(hex.EncodeToString(digest.Sum(nil))), nil
}
