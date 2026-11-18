package azuredevops_test

import (
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/azuredevops"
)

const (
	azureRepositoryID  = "11111111-2222-3333-4444-555555555555"
	azureTargetProject = "22222222-3333-4444-5555-666666666666"
	azureSourceProject = "44444444-5555-6666-7777-888888888888"
	azureDeliveryID    = "aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb"
	azureCommentEvent  = "ms.vss-code.git-pullrequest-comment-event"
)

func azureFixture(t *testing.T, name string, edit func(map[string]any)) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/webhooks/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	if edit == nil {
		return body
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	edit(payload)
	body, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestUnitAzureDevOpsWebhookParse(t *testing.T) {
	provider := azuredevops.New(azuredevops.Options{})
	repository := vcsapi.Repository{ID: azureRepositoryID, Namespace: "Platform", Name: "infra"}
	request := vcsapi.PullRequest{
		AuthorLogin: "author@example.com", State: "open",
		Number:     42,
		HeadBranch: "feature",
		BaseBranch: "main",
		HeadOwner:  azureSourceProject,
		Draft:      true,
		Fork:       true,
		Approved:   true,
		Labels:     []string{"review"},
	}
	for _, tc := range []struct {
		fixture, event string
		want           []vcsapi.Event
	}{
		{"push", "git.push", []vcsapi.Event{
			{Type: "push", Repository: repository, Branch: "main", Before: strings.Repeat("1", 40), Head: strings.Repeat("2", 40), Author: "author@example.com"},
			{Type: "tag", Repository: repository, Tag: "v1", Before: strings.Repeat("0", 40), Head: strings.Repeat("3", 40), Author: "author@example.com"},
		}},
		{"pull_request", "git.pullrequest.created", []vcsapi.Event{{Type: "pull_request", Action: "opened", Repository: repository, Branch: "feature", Head: strings.Repeat("2", 40), Author: "author@example.com", PullRequest: &request}}},
		{"comment", azureCommentEvent, []vcsapi.Event{{
			Type: "comment", Action: "commented", Repository: repository, Branch: "feature", Head: strings.Repeat("2", 40), Author: "reviewer@example.com", PullRequest: &request,
			Comment: &vcsapi.Comment{Body: "/spacelift plan", Author: "reviewer@example.com", PullRequest: 42, Head: strings.Repeat("2", 40), BaseBranch: "main", Fork: true},
		}}},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			body := azureFixture(t, tc.fixture, nil)
			headers := http.Header{"X-Event-Type": {"spoofed"}, "X-Github-Delivery": {"spoofed-id"}}
			events, err := provider.Parse(headers, body)
			if err != nil || !reflect.DeepEqual(events, tc.want) {
				t.Fatalf("normalized Azure events %+v err=%v", events, err)
			}
			if provider.Event(headers, body) != tc.event || provider.DeliveryID(headers, body) != azureDeliveryID || provider.Repository(body) != azureRepositoryID {
				t.Fatal("Azure routing used headers instead of payload identity")
			}
		})
	}
}

func TestUnitAzureDevOpsRequestActionsAndFacts(t *testing.T) {
	provider := azuredevops.New(azuredevops.Options{})
	for _, tc := range []struct {
		event, status, merge, action string
		votes                        []int
		approved                     bool
	}{
		{"git.pullrequest.created", "active", "succeeded", "opened", []int{0}, false},
		{"git.pullrequest.updated", "active", "succeeded", "edited", []int{5}, true},
		{"git.pullrequest.updated", "abandoned", "conflicts", "closed", []int{-10, 0}, false},
		{"git.pullrequest.updated", "completed", "succeeded", "merged", []int{10}, true},
		{"git.pullrequest.merged", "active", "conflicts", "edited", []int{10}, true},
		{"git.pullrequest.merged", "active", "rejectedByPolicy", "edited", []int{5}, true},
		{"git.pullrequest.merged", "completed", "succeeded", "merged", []int{10}, true},
		{"git.pullrequest.updated", "active", "queued", "edited", []int{-5}, false},
		{"git.pullrequest.updated", "active", "notSet", "edited", []int{7, 15}, false},
		{"git.pullrequest.updated", "", "", "edited", nil, false},
	} {
		t.Run(tc.event+"/"+tc.status+"/"+tc.merge, func(t *testing.T) {
			body := azureFixture(t, "pull_request", func(payload map[string]any) {
				payload["eventType"] = tc.event
				resource := payload["resource"].(map[string]any)
				resource["status"], resource["mergeStatus"], resource["isDraft"] = tc.status, tc.merge, false
				delete(resource, "forkSource")
				reviewers := make([]map[string]any, 0, len(tc.votes))
				for _, vote := range tc.votes {
					reviewers = append(reviewers, map[string]any{"vote": vote, "isRequired": true})
				}
				resource["reviewers"] = reviewers
			})
			events, err := provider.Parse(nil, body)
			if err != nil || len(events) != 1 {
				t.Fatal(err)
			}
			pr := events[0].PullRequest
			if events[0].Action != tc.action || pr.Approved != tc.approved || pr.Mergeable || pr.Undiverged || pr.Fork || pr.HeadOwner != azureTargetProject {
				t.Fatalf("Azure payload facts %+v action=%s", pr, events[0].Action)
			}
		})
	}
}

func TestUnitAzureDevOpsEnvelopeRouting(t *testing.T) {
	provider := azuredevops.New(azuredevops.Options{})
	sentinel := provider.Event(nil, []byte(`not JSON`))
	if sentinel == "" || !provider.Consumed(sentinel) {
		t.Fatal("invalid authenticated envelope would be silently filtered")
	}
	for _, body := range []string{`not JSON`, `{`, `null`, `[]`, `{}`, `{"eventType":null}`, `{"eventType":17}`, `{"eventType":""}`} {
		if provider.Event(nil, []byte(body)) != sentinel {
			t.Fatal("invalid envelope lost safe routing marker")
		}
		if events, err := provider.Parse(nil, []byte(body)); err == nil || len(events) != 0 || strings.Contains(err.Error(), body) {
			t.Fatal("invalid envelope parsed or leaked raw input")
		}
	}
	for _, event := range []string{"build.complete", "git.repo.renamed", " ", sentinel} {
		body, err := json.Marshal(map[string]any{"eventType": event, "resource": 42})
		if err != nil {
			t.Fatal(err)
		}
		if provider.Event(nil, body) != "" {
			t.Fatal("unsupported typed event was retained")
		}
		if events, err := provider.Parse(nil, body); err != nil || len(events) != 0 {
			t.Fatal("unsupported event was not filtered", err)
		}
	}
	for _, body := range []string{`{"eventType":"git.push","id":17}`, `{"eventType":"git.push","resource":17}`, `{"eventType":"git.push","resource":[]}`} {
		if provider.Event(nil, []byte(body)) != "git.push" {
			t.Fatal("recognized type was erased by malformed unrelated fields")
		}
		if _, err := provider.Parse(nil, []byte(body)); err == nil {
			t.Fatal("malformed recognized payload accepted")
		}
	}
	for _, body := range []string{`{}`, `null`, `{"id":null}`, `{"id":17}`, `{`} {
		if provider.DeliveryID(nil, []byte(body)) != "" {
			t.Fatal("missing/malformed delivery identity fabricated")
		}
	}
}
