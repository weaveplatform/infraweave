package azuredevops_test

import (
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/azuredevopsfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/azuredevops"
)

func TestUnitAzureDevOpsFakeDocumentedPayloads(t *testing.T) {
	fake := azuredevopsfake.New()
	provider := azuredevops.New(azuredevops.Options{})
	request := azuredevopsfake.WebhookPullRequest{
		ID:            42,
		SourceBranch:  "feature",
		TargetBranch:  "main",
		Head:          strings.Repeat("2", 40),
		Fork:          true,
		SourceProject: "Fork",
		Votes:         []int{5},
		Draft:         true,
		Labels:        []string{"review"},
	}
	for _, tc := range []struct {
		name, kind string
		make       func() (azuredevopsfake.WebhookDelivery, error)
		events     int
	}{
		{"push", "push", func() (azuredevopsfake.WebhookDelivery, error) {
			return fake.PushWebhook(azureDeliveryID, "My Project", "infra", []azuredevopsfake.WebhookRefUpdate{{Name: "refs/heads/main", Before: strings.Repeat("1", 40), Head: strings.Repeat("2", 40)}, {Name: "refs/tags/v1", Before: strings.Repeat("0", 40), Head: strings.Repeat("2", 40)}})
		}, 2},
		{"request", "pull_request", func() (azuredevopsfake.WebhookDelivery, error) {
			return fake.PullRequestWebhook(azureDeliveryID, "git.pullrequest.created", "My Project", "infra", request)
		}, 1},
		{"comment", "comment", func() (azuredevopsfake.WebhookDelivery, error) {
			return fake.CommentWebhook(azureDeliveryID, "My Project", "infra", request, "/spacelift plan")
		}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			delivery, err := tc.make()
			if err != nil {
				t.Fatal(err)
			}
			events, err := provider.Parse(nil, delivery.Body)
			if err != nil || len(events) != tc.events || events[0].Type != tc.kind || events[0].Repository.Namespace != "My Project" || events[0].Repository.Name != "infra" ||
				provider.DeliveryID(nil, delivery.Body) != azureDeliveryID ||
				provider.Repository(delivery.Body) != events[0].Repository.ID {
				t.Fatal("fake lost documented envelope or target identity", err)
			}
			if events[0].PullRequest != nil {
				pr := events[0].PullRequest
				if !pr.Fork || pr.HeadOwner == "" || pr.Number != 42 || !pr.Draft || !pr.Approved || pr.Mergeable || pr.Undiverged {
					t.Fatalf("fake supplied incorrect request facts %+v", pr)
				}
			}
			if !strings.Contains(string(delivery.Body), "example.com") {
				t.Fatal("fixture omitted safe example author address")
			}
		})
	}
	request.SourceProject = ""
	request.SourceRepository = ""
	request.Status = "completed"
	request.MergeStatus = "conflicts"
	delivery, err := fake.PullRequestWebhook(azureDeliveryID, "git.pullrequest.updated", "My Project", "infra", request)
	if err != nil {
		t.Fatal(err)
	}
	events, err := provider.Parse(nil, delivery.Body)
	if err != nil || !events[0].PullRequest.Fork || events[0].Action != "merged" {
		t.Fatal("same-project fork or explicit completion status changed", err)
	}
	request.Fork = false
	delivery, err = fake.CommentWebhook(azureDeliveryID, "My Project", "infra", request, "")
	if err != nil {
		t.Fatal(err)
	}
	events, err = provider.Parse(nil, delivery.Body)
	if err != nil || events[0].Comment.Fork || events[0].Comment.Body != "" {
		t.Fatal("nonfork empty comment changed", err)
	}
}
