package bitbucketdcfake

import (
	"net/http"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/deliveryfake"
)

// Sender posts Data Center webhook fixtures and retains attempt statuses.
type Sender = deliveryfake.Sender

// Attempt is one Data Center HTTP delivery attempt.
type Attempt = deliveryfake.Attempt

// NewSender posts once; documented circuit breaking skips unhealthy hooks
// rather than establishing an automatic redelivery policy for a failed event.
// https://confluence.atlassian.com/bitbucketserver/manage-webhooks-938025878.html
func NewSender(secret []byte, client *http.Client) *Sender {
	return deliveryfake.New(secret, client, deliveryfake.Config{DeliveryHeader: "X-Request-Id", Attempts: 1})
}
