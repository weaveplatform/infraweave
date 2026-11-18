package gitlab_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/gitlab"
)

func TestUnitGitLabFakeDocumentedPayloads(t *testing.T) {
	fake := gitlabfake.New()
	provider := gitlab.New(gitlab.Options{})
	request := gitlabfake.WebhookMergeRequest{
		IID:             42,
		Action:          "open",
		SourceBranch:    "feature",
		TargetBranch:    "main",
		Head:            gitlabHead,
		SourceNamespace: "forks/team",
		Draft:           true,
		Labels:          []string{"review"},
	}
	for _, tc := range []struct {
		name string
		make func() (gitlabfake.WebhookDelivery, error)
		kind string
	}{
		{"push", func() (gitlabfake.WebhookDelivery, error) {
			return fake.PushWebhook("acme/platform", "infra", "main", strings.Repeat("1", 40), gitlabHead)
		}, "push"},
		{"tag", func() (gitlabfake.WebhookDelivery, error) {
			return fake.TagWebhook("acme/platform", "infra", "v1", strings.Repeat("0", 40), gitlabHead)
		}, "tag"},
		{"merge request", func() (gitlabfake.WebhookDelivery, error) {
			return fake.MergeRequestWebhook("acme/platform", "infra", request)
		}, "pull_request"},
		{"note", func() (gitlabfake.WebhookDelivery, error) {
			return fake.NoteWebhook("acme/platform", "infra", request, "/spacelift plan")
		}, "comment"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			delivery, err := tc.make()
			if err != nil {
				t.Fatal(err)
			}
			events, err := provider.Parse(gitlabHeaders(delivery.Event), delivery.Body)
			if err != nil || len(events) != 1 || events[0].Type != tc.kind || events[0].Repository.ID == "" || events[0].Repository.Namespace != "acme/platform" ||
				events[0].Repository.Name != "infra" {
				t.Fatal("fake did not emit documented provider shape", err)
			}
			if events[0].PullRequest != nil &&
				(!events[0].PullRequest.Fork || events[0].PullRequest.HeadOwner != "forks/team" || events[0].PullRequest.Number != 42 || !events[0].PullRequest.Draft) {
				t.Fatal("fake MR identity/fork/draft changed")
			}
			var body any
			if err := json.Unmarshal(delivery.Body, &body); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(delivery.Body), "example.com") {
				t.Fatal("fixture omitted documented example.com author")
			}
		})
	}
	request.SourceNamespace = ""
	request.SourceRepository = ""
	request.PreviousLabels = []string{"old"}
	request.Labels = []string{"new"}
	request.Action = "update"
	delivery, err := fake.MergeRequestWebhook("acme/platform", "infra", request)
	if err != nil {
		t.Fatal(err)
	}
	events, err := provider.Parse(gitlabHeaders(delivery.Event), delivery.Body)
	if err != nil || len(events) != 2 || events[0].Action != "labeled" || events[1].Action != "unlabeled" || events[0].PullRequest.Fork {
		t.Fatal("fake lost same-project label delta", err)
	}
}
