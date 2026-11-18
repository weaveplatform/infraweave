package bitbucketcloud

import (
	"context"
	"net/url"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/webhookcheck"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var webhookEvents = []string{
	"repo:push",
	"pullrequest:created",
	"pullrequest:updated",
	"pullrequest:approved",
	"pullrequest:unapproved",
	"pullrequest:fulfilled",
	"pullrequest:rejected",
	"pullrequest:comment_created",
}

var _ port.WebhookTester = (*Provider)(nil)

// TestWebhooks checks each used repository's complete hook listing.
func (p *Provider) TestWebhooks(ctx context.Context, in port.Integration, expected port.WebhookInput) (port.Check, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	return webhookcheck.Check(expected, webhookEvents, func(repo port.Listed) ([]webhookcheck.Hook, error) {
		path := "/repositories/" + url.PathEscape(repo.Namespace) + "/" + url.PathEscape(repo.Name) + "/hooks"
		return all[webhookcheck.Hook](ctx, p, in, path, url.Values{}, "repository hooks")
	}), nil
}
