package bitbucketdc

import (
	"context"
	"net/http"
	"net/url"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// NewOver is New sending through rt, for a test that serves the fake in
// memory or records each request.
func NewOver(rt http.RoundTripper) *Provider { return newProvider(rt) }

// Hosts is the REST API base and user-facing host of s.
func Hosts(s port.Settings) (api, web string, err error) {
	h, err := hostOf(s)
	return h.api, h.web, err
}

// Send GETs rel, an escaped path under in's REST API, as in's user.
func (p *Provider) Send(ctx context.Context, in port.Integration, rel string) error {
	resp, err := p.get(ctx, in, rel, url.Values{}, rel)
	if err == nil {
		_ = resp.Body.Close()
	}
	return err
}

// Transport is p's transport, the allowlist in front of the network.
func Transport(p *Provider) http.RoundTripper { return p.client.Transport }
