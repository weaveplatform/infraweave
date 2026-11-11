package bitbucketcloud

import (
	"context"
	"net/http"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// NewOver is New sending through rt to apiHost, for a test that serves the
// fake in memory or records each request.
func NewOver(rt http.RoundTripper, apiHost string) *Provider { return newProvider(rt, apiHost) }

// Hosts is the API base and user-facing host of apiHost.
func Hosts(apiHost string) (api, web string, err error) { return hostOf(apiHost) }

// Send GETs rel, an escaped path under the API's base, as in's account.
func (p *Provider) Send(ctx context.Context, in port.Integration, rel string) error {
	resp, err := p.get(ctx, in, p.api+rel, rel)
	if err == nil {
		_ = resp.Body.Close()
	}
	return err
}
