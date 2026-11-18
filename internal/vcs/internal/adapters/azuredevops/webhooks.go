package azuredevops

import (
	"context"
	"net/url"
	"slices"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/webhookcheck"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var webhookEvents = []string{"git.push", "git.pullrequest.created", "git.pullrequest.updated", "git.pullrequest.merged", "ms.vss-code.git-pullrequest-comment-event"}

type subscription struct {
	EventType        string            `json:"eventType"`
	Status           string            `json:"status"`
	PublisherID      string            `json:"publisherId"`
	PublisherInputs  map[string]string `json:"publisherInputs"`
	ConsumerID       string            `json:"consumerId"`
	ConsumerActionID string            `json:"consumerActionId"`
	ConsumerInputs   map[string]string `json:"consumerInputs"`
}

type hookRepository struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Project struct {
		ID string `json:"id"`
	} `json:"project"`
}

var _ port.WebhookTester = (*Provider)(nil)

// TestWebhooks combines active per-event subscriptions for the same URL
// and repository. Azure identifies repository filters by immutable GUID.
func (p *Provider) TestWebhooks(ctx context.Context, in port.Integration, expected port.WebhookInput) (port.Check, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	var subscriptions []subscription
	var listErr error
	if len(expected.Repositories) > 0 {
		subscriptions, listErr = all[subscription](ctx, p, in, "/_apis/hooks/subscriptions", url.Values{}, "service hooks")
	}
	projects := map[string][]hookRepository{}
	return webhookcheck.Check(expected, webhookEvents, func(repo port.Listed) ([]webhookcheck.Hook, error) {
		if listErr != nil {
			return nil, listErr
		}
		repositories, ok := projects[repo.Namespace]
		if !ok {
			var page list[hookRepository]
			if _, err := p.decode(ctx, in, "/"+url.PathEscape(repo.Namespace)+"/_apis/git/repositories", url.Values{}, "repositories", &page); err != nil {
				return nil, err
			}
			repositories = page.Value
			projects[repo.Namespace] = repositories
		}
		i := slices.IndexFunc(repositories, func(r hookRepository) bool { return r.Name == repo.Name })
		if i < 0 {
			return nil, port.ErrNotFound
		}
		r := repositories[i]
		var out []webhookcheck.Hook
		for _, hook := range subscriptions {
			if hook.PublisherID != "tfs" || hook.ConsumerID != "webHooks" || hook.ConsumerActionID != "httpRequest" {
				continue
			}
			if id := hook.PublisherInputs["projectId"]; id != "" && id != r.Project.ID {
				continue
			}
			if id := hook.PublisherInputs["repository"]; id != "" && id != r.ID {
				continue
			}
			out = append(out, webhookcheck.Hook{URL: hook.ConsumerInputs["url"], Events: []string{hook.EventType}, Active: hook.Status == "enabled"})
		}
		return out, nil
	}), nil
}
