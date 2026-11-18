package gitlab_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/gitlab"
)

func TestUnitGitLabMalformedConsumedPayload(t *testing.T) {
	provider := gitlab.New(gitlab.Options{})
	for _, event := range []string{"Push Hook", "Tag Push Hook", "Merge Request Hook", "Note Hook"} {
		for _, body := range []string{`{token-text-537`, `null`, `[]`, `{}`} {
			headers := gitlabHeaders(event)
			got, err := provider.Parse(headers, []byte(body))
			if provider.Event(headers, []byte(body)) != event || err == nil || len(got) != 0 || strings.Contains(err.Error(), "token-text-537") {
				t.Fatal("malformed consumed JSON was filtered, accepted or disclosed", event)
			}
		}
	}
	for _, tc := range []struct {
		name, fixture, event string
		edit                 func(map[string]any)
	}{
		{"missing project", "push", "Push Hook", func(p map[string]any) { delete(p, "project") }},
		{"wrong project id", "push", "Push Hook", func(p map[string]any) { p["project"].(map[string]any)["id"] = "15" }},
		{"zero project id", "push", "Push Hook", func(p map[string]any) { p["project"].(map[string]any)["id"] = 0 }},
		{"negative project id", "push", "Push Hook", func(p map[string]any) { p["project"].(map[string]any)["id"] = -1 }},
		{"missing project path", "push", "Push Hook", func(p map[string]any) { delete(p["project"].(map[string]any), "path_with_namespace") }},
		{"path lacks namespace", "push", "Push Hook", func(p map[string]any) { p["project"].(map[string]any)["path_with_namespace"] = "infra" }},
		{"path lacks name", "push", "Push Hook", func(p map[string]any) { p["project"].(map[string]any)["path_with_namespace"] = "acme/" }},
		{"path lacks owner", "push", "Push Hook", func(p map[string]any) { p["project"].(map[string]any)["path_with_namespace"] = "/infra" }},
		{"wrong object kind", "push", "Push Hook", func(p map[string]any) { p["object_kind"] = "note" }},
		{"missing branch", "push", "Push Hook", func(p map[string]any) { p["ref"] = "refs/heads/" }},
		{"wrong ref", "push", "Push Hook", func(p map[string]any) { p["ref"] = "refs/tags/v1" }},
		{"missing before", "push", "Push Hook", func(p map[string]any) { delete(p, "before") }},
		{"missing head", "push", "Push Hook", func(p map[string]any) { delete(p, "after") }},
		{"nonhex head", "push", "Push Hook", func(p map[string]any) { p["after"] = strings.Repeat("z", 40) }},
		{"wrong tag object", "tag", "Tag Push Hook", func(p map[string]any) { p["object_kind"] = "push" }},
		{"missing MR attributes", "merge_request", "Merge Request Hook", func(p map[string]any) { delete(p, "object_attributes") }},
		{"null MR attributes", "merge_request", "Merge Request Hook", func(p map[string]any) { p["object_attributes"] = nil }},
		{"empty MR action", "merge_request", "Merge Request Hook", func(p map[string]any) { p["object_attributes"].(map[string]any)["action"] = "" }},
		{"wrong MR action type", "merge_request", "Merge Request Hook", func(p map[string]any) { p["object_attributes"].(map[string]any)["action"] = 17 }},
		{"wrong MR object", "merge_request", "Merge Request Hook", func(p map[string]any) { p["object_kind"] = "push" }},
		{"missing MR IID", "merge_request", "Merge Request Hook", func(p map[string]any) { delete(p["object_attributes"].(map[string]any), "iid") }},
		{"negative MR IID", "merge_request", "Merge Request Hook", func(p map[string]any) { p["object_attributes"].(map[string]any)["iid"] = -1 }},
		{"missing MR head", "merge_request", "Merge Request Hook", func(p map[string]any) { delete(p["object_attributes"].(map[string]any), "last_commit") }},
		{"zero MR head", "merge_request", "Merge Request Hook", func(p map[string]any) {
			p["object_attributes"].(map[string]any)["last_commit"] = map[string]any{"id": strings.Repeat("0", 40)}
		}},
		{"missing source id", "merge_request", "Merge Request Hook", func(p map[string]any) { delete(p["object_attributes"].(map[string]any), "source_project_id") }},
		{"mismatched target id", "merge_request", "Merge Request Hook", func(p map[string]any) { p["object_attributes"].(map[string]any)["target_project_id"] = 18 }},
		{"missing source branch", "merge_request", "Merge Request Hook", func(p map[string]any) { delete(p["object_attributes"].(map[string]any), "source_branch") }},
		{"missing target branch", "merge_request", "Merge Request Hook", func(p map[string]any) { delete(p["object_attributes"].(map[string]any), "target_branch") }},
		{"invalid old revision", "merge_request", "Merge Request Hook", func(p map[string]any) { p["object_attributes"].(map[string]any)["oldrev"] = "token-text-537" }},
		{"missing note kind", "note", "Note Hook", func(p map[string]any) { delete(p["object_attributes"].(map[string]any), noteTypeField) }},
		{"wrong note kind type", "note", "Note Hook", func(p map[string]any) { p["object_attributes"].(map[string]any)[noteTypeField] = 42 }},
		{"missing note body", "note", "Note Hook", func(p map[string]any) { delete(p["object_attributes"].(map[string]any), "note") }},
		{"wrong note body type", "note", "Note Hook", func(p map[string]any) { p["object_attributes"].(map[string]any)["note"] = true }},
		{"missing note MR", "note", "Note Hook", func(p map[string]any) { delete(p, "merge_request") }},
		{"missing note MR IID", "note", "Note Hook", func(p map[string]any) { delete(p["merge_request"].(map[string]any), "iid") }},
		{"wrong note object", "note", "Note Hook", func(p map[string]any) { p["object_kind"] = "merge_request" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := webhookFixture(t, tc.fixture, tc.edit)
			headers := gitlabHeaders(tc.event)
			got, err := provider.Parse(headers, body)
			if provider.Event(headers, body) != tc.event || err == nil || len(got) != 0 || strings.Contains(err.Error(), "token-text-537") {
				t.Fatal("malformed consumed payload was filtered, accepted or disclosed")
			}
		})
	}
}

func TestUnitGitLabApprovalFacts(t *testing.T) {
	provider := gitlab.New(gitlab.Options{})
	for _, tc := range []struct {
		name, action, status, detailed string
		resolved                       *bool
		draft, approved, mergeable     bool
	}{
		{name: "unknown facts", action: "open"},
		{name: "legacy conflict-free lacks approval proof", action: "open", status: "can_be_merged", resolved: gitlabBool(true)},
		{name: "fully approved but unresolved", action: "approved", status: "can_be_merged", resolved: gitlabBool(false), approved: true},
		{name: "fully approved lacks discussion proof", action: "approved", status: "can_be_merged", approved: true},
		{name: "fully approved legacy", action: "approved", status: "can_be_merged", resolved: gitlabBool(true), approved: true, mergeable: true},
		{name: "detailed complete", action: "open", detailed: "mergeable", resolved: gitlabBool(true), mergeable: true},
		{name: "checking beats legacy", action: "approved", status: "can_be_merged", detailed: "checking", resolved: gitlabBool(true), approved: true},
		{name: "conflicts", action: "approved", status: "cannot_be_merged", detailed: "conflict", resolved: gitlabBool(true), approved: true},
		{name: "missing detailed and legacy status", action: "approved", resolved: gitlabBool(true), approved: true},
		{name: "explicit unapproval beats stale status", action: "unapproved", detailed: "mergeable", resolved: gitlabBool(true)},
		{name: "draft", action: "approved", detailed: "mergeable", resolved: gitlabBool(true), draft: true, approved: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := webhookFixture(t, "merge_request", func(p map[string]any) {
				a := p["object_attributes"].(map[string]any)
				a["action"], a["merge_status"], a["detailed_merge_status"], a["draft"] = tc.action, tc.status, tc.detailed, tc.draft
				a["blocking_discussions_resolved"] = tc.resolved
			})
			events, err := provider.Parse(gitlabHeaders("Merge Request Hook"), body)
			if err != nil || len(events) != 1 {
				t.Fatal(err)
			}
			pr := events[0].PullRequest
			if pr.Approved != tc.approved || pr.Mergeable != tc.mergeable || pr.Undiverged {
				t.Fatalf("approval facts %+v", pr)
			}
		})
	}
}

func gitlabBool(value bool) *bool { return &value }

func TestUnitGitLabPayloadEdges(t *testing.T) {
	p := gitlab.New(gitlab.Options{})
	body := webhookFixture(t, "push", func(payload map[string]any) {
		delete(payload, "user_username")
		payload["ref"] = "refs/heads/release/next"
		payload["after"] = strings.Repeat("0", 40)
	})
	events, err := p.Parse(gitlabHeaders("Push Hook"), body)
	if err != nil || events[0].Author != "Example Author" || events[0].Branch != "release/next" || events[0].Head != strings.Repeat("0", 40) {
		t.Fatal("branch deletion or fallback author lost", err)
	}
	body = webhookFixture(t, "push", func(payload map[string]any) { payload["after"] = strings.Repeat("a", 64) })
	if _, err := p.Parse(gitlabHeaders("Push Hook"), body); err != nil {
		t.Fatal("SHA-256 Git commit rejected", err)
	}
	body = webhookFixture(t, "merge_request", func(payload map[string]any) {
		a := payload["object_attributes"].(map[string]any)
		a["source_project_id"] = 15
		delete(a, "source")
		a["labels"] = []map[string]string{{"title": "explicit"}, {"title": "explicit"}, {"title": ""}}
	})
	events, err = p.Parse(gitlabHeaders("Merge Request Hook"), body)
	if err != nil || events[0].PullRequest.Fork || events[0].PullRequest.HeadOwner != "Acme Platform" || !slices.Equal(events[0].PullRequest.Labels, []string{"explicit"}) {
		t.Fatal("same-project owner or explicit labels lost", err)
	}
	body = webhookFixture(t, "merge_request", func(payload map[string]any) {
		delete(payload["object_attributes"].(map[string]any), "source")
		delete(payload, "labels")
	})
	events, err = p.Parse(gitlabHeaders("Merge Request Hook"), body)
	if err != nil || !events[0].PullRequest.Fork || events[0].PullRequest.HeadOwner != "" || events[0].PullRequest.Labels == nil {
		t.Fatal("unknown fork owner fabricated", err)
	}
	for _, source := range []map[string]any{{"path_with_namespace": "forks/subgroup/infra"}, {}} {
		body = webhookFixture(t, "merge_request", func(payload map[string]any) {
			payload["object_attributes"].(map[string]any)["source"] = source
		})
		events, err = p.Parse(gitlabHeaders("Merge Request Hook"), body)
		want := ""
		if len(source) != 0 {
			want = "forks/subgroup"
		}
		if err != nil || events[0].PullRequest.HeadOwner != want {
			t.Fatal("source namespace fallback changed", err)
		}
	}
	body = webhookFixture(t, "merge_request", func(payload map[string]any) {
		a := payload["object_attributes"].(map[string]any)
		a["source_project_id"] = 15
		delete(a, "source")
		delete(payload["project"].(map[string]any), "namespace")
	})
	events, err = p.Parse(gitlabHeaders("Merge Request Hook"), body)
	if err != nil || events[0].PullRequest.HeadOwner != "acme/platform" {
		t.Fatal("same-project namespace fallback changed", err)
	}
	for raw, want := range map[string]string{`{"project":{"id":9223372036854775807}}`: "9223372036854775807", `{"project":{"id":"15"}}`: "", `{"project":{"id":0}}`: "", `null`: "", `{`: ""} {
		if p.Repository([]byte(raw)) != want {
			t.Fatal("project identity was rounded or fabricated")
		}
	}
}
