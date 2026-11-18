package bitbucketdc

import (
	"context"
	"net/url"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/webhookcheck"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var webhookEvents = []string{
	"repo:refs_changed",
	"pr:opened",
	"pr:from_ref_updated",
	"pr:modified",
	"pr:reviewer:approved",
	"pr:reviewer:unapproved",
	"pr:merged",
	"pr:comment:added",
}

var _ port.WebhookTester = (*Provider)(nil)

// TestWebhooks checks the active registrations on each used repository.
func (p *Provider) TestWebhooks(ctx context.Context, in port.Integration, expected port.WebhookInput) (port.Check, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	return webhookcheck.Check(expected, webhookEvents, func(repo port.Listed) ([]webhookcheck.Hook, error) {
		path := "/projects/" + url.PathEscape(repo.Namespace) + "/repos/" + url.PathEscape(repo.Name) + "/webhooks"
		return all[webhookcheck.Hook](ctx, p, in, path, url.Values{}, "repository webhooks")
	}), nil
}
