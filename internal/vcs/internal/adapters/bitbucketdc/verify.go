package bitbucketdc

import (
	"net/http"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/webhookauth"
)

// ErrSignature refuses unsigned deliveries as well as invalid signatures.
var ErrSignature = webhookauth.ErrSignature

// Verify checks each active secret and retains the exact verified header.
func (*Provider) Verify(h http.Header, body []byte, secrets [][]byte) ([]byte, error) {
	signature, err := webhookauth.Verify("X-Hub-Signature", h, body, secrets)
	if err != nil {
		return nil, ErrSignature
	}
	return signature, nil
}
