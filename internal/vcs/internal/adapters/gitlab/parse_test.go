package gitlab_test

import (
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/gitlab"
)

const noteTypeField = "noteable_type" //nolint:misspell // GitLab's documented field name.

const gitlabHead = "2222222222222222222222222222222222222222"

func webhookFixture(t *testing.T, name string, edit func(map[string]any)) []byte {
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

func gitlabHeaders(event string) http.Header {
	headers := http.Header{}
	headers.Set("X-Gitlab-Event", event)
	return headers
}

func TestUnitGitLabWebhookParse(t *testing.T) {
	provider := gitlab.New(gitlab.Options{})
	repository := vcsapi.Repository{ID: "15", Namespace: "acme/platform", Name: "infra"}
	request := vcsapi.PullRequest{Number: 42, HeadBranch: "feature", BaseBranch: "main", HeadOwner: "forks/team", Fork: true, Labels: []string{"review"}}
	noteRequest := request
	noteRequest.Draft = true
	for _, tc := range []struct {
		fixture, event string
		want           vcsapi.Event
	}{
		{"push", "Push Hook", vcsapi.Event{Type: "push", Repository: repository, Branch: "main", Before: strings.Repeat("1", 40), Head: gitlabHead, Author: "author"}},
		{"tag", "Tag Push Hook", vcsapi.Event{Type: "tag", Repository: repository, Tag: "v1.0.0", Before: strings.Repeat("0", 40), Head: gitlabHead, Author: "author"}},
		{"merge_request", "Merge Request Hook", vcsapi.Event{Type: "pull_request", Action: "opened", Repository: repository, Branch: "feature", Head: gitlabHead, Author: "author", PullRequest: &request}},
		{"note", "Note Hook", vcsapi.Event{
			Type: "comment", Action: "commented", Repository: repository, Branch: "feature", Head: gitlabHead, Author: "reviewer", PullRequest: &noteRequest,
			Comment: &vcsapi.Comment{Body: "/spacelift plan", Author: "reviewer", PullRequest: 42, Head: gitlabHead, BaseBranch: "main", Fork: true},
		}},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			body, headers := webhookFixture(t, tc.fixture, nil), gitlabHeaders(tc.event)
			got, err := provider.Parse(headers, body)
			if err != nil || !reflect.DeepEqual(got, []vcsapi.Event{tc.want}) {
				t.Fatalf("normalized event: %+v %v", got, err)
			}
			if provider.Event(headers, body) != tc.event || !provider.Consumed(tc.event) || provider.Repository(body) != "15" {
				t.Fatal("provider routing lost raw event or stable project identity")
			}
		})
	}
}

func TestUnitGitLabMergeActions(t *testing.T) {
	provider := gitlab.New(gitlab.Options{})
	for _, tc := range []struct {
		action, oldrev    string
		previous, current []string
		want              []string
	}{
		{action: "open", want: []string{"opened"}},
		{action: "reopen", want: []string{"reopened"}},
		{action: "close", want: []string{"closed"}},
		{action: "merge", want: []string{"merged"}},
		{action: "approved", want: []string{"edited"}},
		{action: "unapproved", want: []string{"edited"}},
		{action: "update", want: []string{"edited"}},
		{action: "update", oldrev: strings.Repeat("1", 40), want: []string{"synchronize"}},
		{action: "update", previous: []string{}, current: []string{"new"}, want: []string{"labeled"}},
		{action: "update", previous: []string{"old"}, current: []string{}, want: []string{"unlabeled"}},
		{action: "update", previous: []string{"old"}, current: []string{"new"}, want: []string{"labeled", "unlabeled"}},
		{action: "update", previous: []string{"same"}, current: []string{"same"}, want: []string{"edited"}},
		{action: "update", oldrev: strings.Repeat("1", 40), previous: []string{"old"}, current: []string{"new"}, want: []string{"synchronize"}},
	} {
		t.Run(tc.action+strings.Join(tc.want, "-"), func(t *testing.T) {
			body := webhookFixture(t, "merge_request", func(payload map[string]any) {
				attributes := payload["object_attributes"].(map[string]any)
				attributes["action"], attributes["oldrev"] = tc.action, tc.oldrev
				if tc.previous != nil {
					labels := func(names []string) []map[string]string {
						out := make([]map[string]string, 0, len(names))
						for _, name := range names {
							out = append(out, map[string]string{"title": name})
						}
						return out
					}
					payload["changes"] = map[string]any{"labels": map[string]any{"previous": labels(tc.previous), "current": labels(tc.current)}}
					payload["labels"] = labels(tc.current)
				}
			})
			got, err := provider.Parse(gitlabHeaders("Merge Request Hook"), body)
			if err != nil {
				t.Fatal(err)
			}
			actions := make([]string, 0, len(got))
			for _, event := range got {
				actions = append(actions, event.Action)
				if event.PullRequest.Approved != (tc.action == "approved") || event.PullRequest.Number != 42 {
					t.Fatal("approval fact or request IID lost")
				}
				if event.Before != tc.oldrev {
					t.Fatal("synchronization lost its previous commit")
				}
			}
			if !slices.Equal(actions, tc.want) {
				t.Fatalf("actions %v want %v", actions, tc.want)
			}
			if len(got) == 2 && len(got[0].PullRequest.Labels) > 0 {
				got[0].PullRequest.Labels[0] = "changed-by-first-consumer"
				if got[1].PullRequest.Labels[0] != tc.current[0] {
					t.Fatal("independent label events share mutable request state")
				}
			}
		})
	}
}

func TestUnitGitLabWebhookFilter(t *testing.T) {
	provider := gitlab.New(gitlab.Options{})
	for _, event := range []string{"Issue Hook", "System Hook", "Pipeline Hook", "", "Push hook"} {
		got, err := provider.Parse(gitlabHeaders(event), []byte(`not JSON`))
		if err != nil || len(got) != 0 || provider.Consumed(event) {
			t.Fatal("unsupported family was not filtered", event, err)
		}
	}
	for _, kind := range []string{"Issue", "Commit", "Snippet", "UnknownFutureNote"} {
		body := webhookFixture(t, "note", func(payload map[string]any) { payload["object_attributes"].(map[string]any)[noteTypeField] = kind })
		got, err := provider.Parse(gitlabHeaders("Note Hook"), body)
		if err != nil || len(got) != 0 || provider.Event(gitlabHeaders("Note Hook"), body) != "" {
			t.Fatal("non-MR note was not filtered", kind, err)
		}
	}
	for _, action := range []string{"approval", "unapproval", "unknown"} {
		body := webhookFixture(t, "merge_request", func(payload map[string]any) { payload["object_attributes"].(map[string]any)["action"] = action })
		got, err := provider.Parse(gitlabHeaders("Merge Request Hook"), body)
		if err != nil || len(got) != 0 || provider.Event(gitlabHeaders("Merge Request Hook"), body) != "" {
			t.Fatal("unsupported MR action was not filtered", action, err)
		}
	}
}
