package port

import (
	"net/http"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

// Webhooks verifies and normalizes deliveries without making provider calls.
// Verify compares credentials in constant time. Header-signature providers
// return the verified header verbatim; token/Basic providers return the
// body's hex HMAC-SHA256 under the matching secret instead of credentials.
// Parse receives only the retained safe headers, never credential headers.
//
// ref: decision 0012 § "Webhook intake: stored before acknowledgement"
type Webhooks interface {
	Verify(headers http.Header, body []byte, secrets [][]byte) (signature []byte, err error)
	DeliveryID(headers http.Header, body []byte) string
	Event(headers http.Header, body []byte) string
	Consumed(event string) bool
	Repository(body []byte) string
	Parse(headers http.Header, body []byte) ([]vcsapi.Event, error)
}
