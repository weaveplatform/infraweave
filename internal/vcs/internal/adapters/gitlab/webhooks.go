package gitlab

import (
	"context"
	"time"

	gitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/webhookcheck"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var webhookEvents = []string{"push_events", "tag_push_events", "merge_requests_events", "note_events"}

var _ port.WebhookTester = (*Provider)(nil)

// TestWebhooks reads every project's registered hooks without changing them.
func (p *Provider) TestWebhooks(ctx context.Context, in port.Integration, expected port.WebhookInput) (port.Check, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	client, _, clientErr := p.client(in)
	return webhookcheck.Check(expected, webhookEvents, func(repo port.Listed) ([]webhookcheck.Hook, error) {
		if clientErr != nil {
			return nil, clientErr
		}
		var out []webhookcheck.Hook
		for page := int64(1); ; {
			hooks, response, err := client.Projects.ListProjectHooks(repo.Namespace+"/"+repo.Name,
				&gitlab.ListProjectHooksOptions{ListOptions: gitlab.ListOptions{Page: page, PerPage: 100}}, gitlab.WithContext(ctx))
			if err != nil {
				return nil, classify(response, err, "project hooks")
			}
			for _, hook := range hooks {
				var events []string
				for i, enabled := range []bool{hook.PushEvents, hook.TagPushEvents, hook.MergeRequestsEvents, hook.NoteEvents} {
					if enabled {
						events = append(events, webhookEvents[i])
					}
				}
				out = append(out, webhookcheck.Hook{URL: hook.URL, Events: events, Active: hook.DisabledUntil == nil || !hook.DisabledUntil.After(time.Now())})
			}
			if response.NextPage == 0 {
				return out, nil
			}
			page = response.NextPage
		}
	}), nil
}
