package azuredevops_test

import (
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/azuredevops"
)

func TestUnitAzureDevOpsMalformedConsumedPayloads(t *testing.T) {
	p := azuredevops.New(azuredevops.Options{})
	for _, tc := range []struct {
		name, fixture string
		edit          func(map[string]any)
	}{
		{"missing resource", "push", func(v map[string]any) { delete(v, "resource") }},
		{"missing repo", "push", func(v map[string]any) { delete(v["resource"].(map[string]any), "repository") }},
		{"missing repo ID", "push", func(v map[string]any) { delete(v["resource"].(map[string]any)["repository"].(map[string]any), "id") }},
		{"wrong repo ID type", "push", func(v map[string]any) { v["resource"].(map[string]any)["repository"].(map[string]any)["id"] = 42 }},
		{"missing repo name", "push", func(v map[string]any) { delete(v["resource"].(map[string]any)["repository"].(map[string]any), "name") }},
		{"missing project name", "push", func(v map[string]any) {
			delete(v["resource"].(map[string]any)["repository"].(map[string]any)["project"].(map[string]any), "name")
		}},
		{"missing refs", "push", func(v map[string]any) { delete(v["resource"].(map[string]any), "refUpdates") }},
		{"empty refs", "push", func(v map[string]any) { v["resource"].(map[string]any)["refUpdates"] = []any{} }},
		{"empty ref name", "push", func(v map[string]any) {
			v["resource"].(map[string]any)["refUpdates"].([]any)[0].(map[string]any)["name"] = ""
		}},
		{"empty branch name", "push", func(v map[string]any) {
			v["resource"].(map[string]any)["refUpdates"].([]any)[0].(map[string]any)["name"] = "refs/heads/"
		}},
		{"missing before", "push", func(v map[string]any) {
			delete(v["resource"].(map[string]any)["refUpdates"].([]any)[0].(map[string]any), "oldObjectId")
		}},
		{"invalid after", "push", func(v map[string]any) {
			v["resource"].(map[string]any)["refUpdates"].([]any)[0].(map[string]any)["newObjectId"] = strings.Repeat("z", 40)
		}},
		{"bad later ref", "push", func(v map[string]any) {
			v["resource"].(map[string]any)["refUpdates"].([]any)[1].(map[string]any)["newObjectId"] = "short"
		}},
		{"missing PR ID", "pull_request", func(v map[string]any) { delete(v["resource"].(map[string]any), "pullRequestId") }},
		{"negative PR ID", "pull_request", func(v map[string]any) { v["resource"].(map[string]any)["pullRequestId"] = -1 }},
		{"missing PR repo", "pull_request", func(v map[string]any) { delete(v["resource"].(map[string]any), "repository") }},
		{"missing source ref", "pull_request", func(v map[string]any) { delete(v["resource"].(map[string]any), "sourceRefName") }},
		{"wrong target ref", "pull_request", func(v map[string]any) { v["resource"].(map[string]any)["targetRefName"] = "refs/tags/v1" }},
		{"missing PR head", "pull_request", func(v map[string]any) { delete(v["resource"].(map[string]any), "lastMergeSourceCommit") }},
		{"zero PR head", "pull_request", func(v map[string]any) {
			v["resource"].(map[string]any)["lastMergeSourceCommit"] = map[string]any{"commitId": strings.Repeat("0", 40)}
		}},
		{"wrong reviewer vote", "pull_request", func(v map[string]any) { v["resource"].(map[string]any)["reviewers"] = []map[string]any{{"vote": "10"}} }},
		{"wrong draft type", "pull_request", func(v map[string]any) { v["resource"].(map[string]any)["isDraft"] = "false" }},
		{"wrong fork type", "pull_request", func(v map[string]any) { v["resource"].(map[string]any)["forkSource"] = false }},
		{"missing comment PR", "comment", func(v map[string]any) { delete(v["resource"].(map[string]any), "pullRequest") }},
		{"missing comment", "comment", func(v map[string]any) { delete(v["resource"].(map[string]any), "comment") }},
		{"missing content", "comment", func(v map[string]any) { delete(v["resource"].(map[string]any)["comment"].(map[string]any), "content") }},
		{"wrong content type", "comment", func(v map[string]any) { v["resource"].(map[string]any)["comment"].(map[string]any)["content"] = 42 }},
		{"missing nested repo", "comment", func(v map[string]any) {
			delete(v["resource"].(map[string]any)["pullRequest"].(map[string]any), "repository")
		}},
		{"missing nested head", "comment", func(v map[string]any) {
			delete(v["resource"].(map[string]any)["pullRequest"].(map[string]any), "lastMergeSourceCommit")
		}},
		{"mismatched direct repo", "comment", func(v map[string]any) {
			v["resource"].(map[string]any)["repository"] = map[string]any{"id": "different-target"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := azureFixture(t, tc.fixture, tc.edit)
			if event := p.Event(nil, body); event == "" || !p.Consumed(event) {
				t.Fatal("malformed consumed payload silently filtered")
			}
			if events, err := p.Parse(nil, body); err == nil || len(events) != 0 {
				t.Fatal("malformed consumed payload produced partial or successful events")
			}
		})
	}
}

func TestUnitAzureDevOpsPayloadEdges(t *testing.T) {
	p := azuredevops.New(azuredevops.Options{})
	body := azureFixture(t, "push", func(v map[string]any) {
		r := v["resource"].(map[string]any)
		r["refUpdates"] = []map[string]string{
			{"name": "refs/notes/ignored"},
			{"name": "refs/heads/release/next", "oldObjectId": strings.Repeat("a", 64), "newObjectId": strings.Repeat("0", 40)},
		}
		r["pushedBy"] = map[string]string{"id": "author-id"}
	})
	events, err := p.Parse(nil, body)
	if err != nil || len(events) != 1 || events[0].Branch != "release/next" || events[0].Head != strings.Repeat("0", 40) || events[0].Author != "author-id" {
		t.Fatal("mixed ref deletion or author fallback lost", err)
	}
	body = azureFixture(t, "push", func(v map[string]any) {
		v["resource"].(map[string]any)["refUpdates"] = []map[string]string{{"name": "refs/notes/ignored"}}
	})
	if events, err := p.Parse(nil, body); err != nil || len(events) != 0 {
		t.Fatal("unsupported ref kind became an event", err)
	}
	for _, source := range []any{nil, map[string]any{}, map[string]any{"repository": map[string]any{"project": map[string]any{"id": azureTargetProject}}}} {
		body = azureFixture(t, "pull_request", func(v map[string]any) {
			r := v["resource"].(map[string]any)
			r["forkSource"] = source
			delete(r, "labels")
			r["createdBy"] = map[string]string{"displayName": "Example Author"}
		})
		events, err = p.Parse(nil, body)
		if err != nil {
			t.Fatal(err)
		}
		pr := events[0].PullRequest
		wantOwner := azureTargetProject
		if source != nil && len(source.(map[string]any)) == 0 {
			wantOwner = ""
		}
		if pr.Fork != (source != nil) || pr.HeadOwner != wantOwner || pr.Mergeable || pr.Undiverged || pr.Labels == nil || events[0].Author != "Example Author" {
			t.Fatalf("fork identity or unknown facts fabricated: %+v", pr)
		}
	}
	body = azureFixture(t, "pull_request", func(v map[string]any) {
		v["resource"].(map[string]any)["labels"] = []map[string]string{{"name": ""}, {"name": "label"}}
	})
	events, err = p.Parse(nil, body)
	if err != nil || len(events[0].PullRequest.Labels) != 1 {
		t.Fatal("empty label handling changed", err)
	}
	body = azureFixture(t, "comment", func(v map[string]any) {
		r := v["resource"].(map[string]any)
		r["repository"] = map[string]any{"id": azureRepositoryID}
		r["comment"].(map[string]any)["content"] = ""
		r["comment"].(map[string]any)["author"] = map[string]any{}
	})
	events, err = p.Parse(nil, body)
	if err != nil || events[0].Comment.Body != "" || events[0].Comment.Author != "" {
		t.Fatal("explicit empty comment or missing optional actor changed", err)
	}
	for _, raw := range []string{`{}`, `null`, `{`, `{"eventType":"git.push","resource":{"repository":null}}`, `{"eventType":"ms.vss-code.git-pullrequest-comment-event","resource":{}}`} {
		if p.Repository([]byte(raw)) != "" {
			t.Fatal("missing repository identity fabricated")
		}
	}
}
