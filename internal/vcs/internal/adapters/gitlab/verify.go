package gitlab

import (
	"crypto/fips140"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
)

var errWebhookToken = errors.New("gitlab: webhook token refused")

// Verify compares every allowed secret before selecting the body digest.
// Hashing the tokens gives the comparison a fixed length; neither the token
// itself nor its request header is retained as replay metadata.
func (*Provider) Verify(headers http.Header, body []byte, secrets [][]byte) ([]byte, error) {
	tokens := headers.Values("X-Gitlab-Token")
	token := headers.Get("X-Gitlab-Token")
	got := sha256.Sum256([]byte(token))
	var matched []byte
	for _, secret := range secrets {
		want := sha256.Sum256(secret)
		if subtle.ConstantTimeCompare(got[:], want[:]) == 1 && len(secret) != 0 {
			matched = secret
		}
	}
	if len(tokens) != 1 || token == "" || matched == nil {
		return nil, errWebhookToken
	}
	// FIPS-only HMAC requires at least 112 key bits. A configured shorter
	// token is a verification refusal, not an invalid cryptographic operation.
	if fips140.Enforced() && len(matched) < 112/8 {
		return nil, errWebhookToken
	}
	digest := hmac.New(sha256.New, matched)
	_, _ = digest.Write(body)
	return []byte(hex.EncodeToString(digest.Sum(nil))), nil
}

// DeliveryID is GitLab's request UUID; intake supplies the body hash when absent.
func (*Provider) DeliveryID(headers http.Header, _ []byte) string {
	return headers.Get("X-Gitlab-Event-UUID")
}
