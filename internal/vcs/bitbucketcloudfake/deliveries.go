package bitbucketcloudfake

import (
	"net/http"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/deliveryfake"
)

// Sender posts Cloud webhook fixtures and retains their attempt statuses.
type Sender = deliveryfake.Sender

// Attempt is one Cloud HTTP delivery attempt.
type Attempt = deliveryfake.Attempt

// NewSender signs with the hook secret, independently of the API token.
// Cloud retries a 5xx at most twice, preserving its ID and body each time.
// https://support.atlassian.com/bitbucket-cloud/kb/troubleshoot-webhooks/
func NewSender(secret []byte, client *http.Client) *Sender {
	return deliveryfake.New(secret, client, deliveryfake.Config{
		DeliveryHeader: "X-Request-UUID", AttemptHeader: "X-Attempt-Number", HookHeader: "X-Hook-UUID", Attempts: 3,
	})
}
