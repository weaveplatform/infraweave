package azuredevops

import (
	"context"
	"net/http"
	"net/url"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// NewOver is New sending through rt, for a test that serves the fake in
// memory or records each request.
func NewOver(rt http.RoundTripper) *Provider { return newProvider(rt) }

// Hosts is the organization URL, its escaped path and the user-facing
// host of s.
func Hosts(s port.Settings) (api, base, web string, err error) {
	o, err := orgOf(s)
	return o.api, o.base, o.web, err
}

// Send GETs rel, an escaped path under in's organization, as in's token.
func (p *Provider) Send(ctx context.Context, in port.Integration, rel string) error {
	resp, err := p.get(ctx, in, rel, url.Values{}, rel)
	if err == nil {
		_ = resp.Body.Close()
	}
	return err
}
