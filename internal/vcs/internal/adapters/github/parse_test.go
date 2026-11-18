package github_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/githubfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/github"
)

func payload(t *testing.T, kind, action string, edit func(map[string]any)) []byte {
	t.Helper()
	b, err := githubfake.Payload(kind, action)
	if err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		var d map[string]any
		if err := json.Unmarshal(b, &d); err != nil {
			t.Fatal(err)
		}
		edit(d)
		b, err = json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
	}
	return b
}

func TestUnitGitHubParse(t *testing.T) {
	repo := vcsapi.Repository{ID: "7", Namespace: "acme", Name: "infra"}
	pr := &vcsapi.PullRequest{
		Number: 12, HeadBranch: "feature", BaseBranch: "main", HeadOwner: "contributor",
		Fork: true, Mergeable: true, Labels: []string{"infrastructure"},
	}
	type example struct {
		kind, action string
		edit         func(map[string]any)
		want         vcsapi.Event
	}
	cases := make([]example, 0, 26)
	cases = append(cases, []example{
		{
			kind: "push",
			want: vcsapi.Event{
				Type:       "push",
				Repository: repo,
				Branch:     "main",
				Before:     githubfake.BeforeSHA,
				Head:       githubfake.AfterSHA,
				Forced:     true,
				Author:     githubfake.AuthorLogin,
			},
		},
		{
			kind: "push",
			edit: func(d map[string]any) { d["ref"] = "refs/tags/release/v1" },
			want: vcsapi.Event{
				Type:       "tag",
				Repository: repo,
				Tag:        "release/v1",
				Before:     githubfake.BeforeSHA,
				Head:       githubfake.AfterSHA,
				Forced:     true,
				Author:     githubfake.AuthorLogin,
			},
		},
		{
			kind:   "issue_comment",
			action: "created",
			want: vcsapi.Event{
				Type:       "comment",
				Action:     "created",
				Repository: repo,
				Author:     githubfake.AuthorLogin,
				Comment:    &vcsapi.Comment{Body: "/spacelift preview", Author: githubfake.AuthorLogin, PullRequest: 12},
			},
		},
		{
			kind:   "check_run",
			action: "requested_action",
			want: vcsapi.Event{
				Type:        "check_action",
				Action:      "requested_action",
				Repository:  repo,
				Author:      githubfake.AuthorLogin,
				Head:        githubfake.AfterSHA,
				CheckAction: &vcsapi.CheckAction{Action: "preview", ExternalID: "stack-7"},
			},
		},
		{kind: "repository", action: "renamed", want: vcsapi.Event{Type: "repository_renamed", Action: "renamed", Repository: repo, Author: githubfake.AuthorLogin}},
	}...)
	for _, action := range []string{"opened", "reopened", "synchronize", "edited", "closed", "labeled", "unlabeled", "ready_for_review", "converted_to_draft"} {
		request := *pr
		request.Draft = action == "converted_to_draft"
		cases = append(
			cases,
			example{
				kind:   "pull_request",
				action: action,
				want: vcsapi.Event{
					Type:        "pull_request",
					Action:      action,
					Repository:  repo,
					Author:      githubfake.AuthorLogin,
					Branch:      "feature",
					Head:        githubfake.HeadSHA,
					PullRequest: &request,
				},
			},
		)
	}
	cases = append(
		cases,
		example{
			kind:   "pull_request",
			action: "closed",
			edit:   func(d map[string]any) { d["pull_request"].(map[string]any)["merged"] = true },
			want: vcsapi.Event{
				Type:        "pull_request",
				Action:      "merged",
				Repository:  repo,
				Author:      githubfake.AuthorLogin,
				Branch:      "feature",
				Head:        githubfake.HeadSHA,
				PullRequest: pr,
			},
		},
	)
	for _, action := range []string{"submitted", "dismissed"} {
		cases = append(
			cases,
			example{
				kind:   "pull_request_review",
				action: action,
				want: vcsapi.Event{
					Type:        "pull_request",
					Action:      "edited",
					Repository:  repo,
					Author:      githubfake.AuthorLogin,
					Branch:      "feature",
					Head:        githubfake.HeadSHA,
					PullRequest: pr,
				},
			},
		)
	}
	for _, action := range []string{"created", "deleted", "suspend", "unsuspend", "new_permissions_accepted"} {
		cases = append(
			cases,
			example{
				kind:   "installation",
				action: action,
				want: vcsapi.Event{
					Type:   "installation",
					Action: action,
					Author: githubfake.AuthorLogin,
					Installation: &vcsapi.Installation{
						ID:           "71",
						Action:       action,
						Owner:        vcsapi.Owner{ID: "41", Login: "acme"},
						Repositories: []vcsapi.Repository{repo},
						Added:        []vcsapi.Repository{},
						Removed:      []vcsapi.Repository{},
					},
				},
			},
		)
	}
	for _, action := range []string{"added", "removed"} {
		cases = append(
			cases,
			example{
				kind:   "installation_repositories",
				action: action,
				want: vcsapi.Event{
					Type:   "installation",
					Action: action,
					Author: githubfake.AuthorLogin,
					Installation: &vcsapi.Installation{
						ID:           "71",
						Action:       action,
						Owner:        vcsapi.Owner{ID: "41", Login: "acme"},
						Repositories: []vcsapi.Repository{},
						Added:        []vcsapi.Repository{repo},
						Removed:      []vcsapi.Repository{{ID: "8", Namespace: "acme", Name: "retired"}},
					},
				},
			},
		)
	}
	for _, kind := range []string{"installation_target", "organization"} {
		cases = append(
			cases,
			example{
				kind:   kind,
				action: "renamed",
				want: vcsapi.Event{
					Type:        "owner_renamed",
					Action:      "renamed",
					Author:      githubfake.AuthorLogin,
					OwnerRename: &vcsapi.OwnerRename{ID: "41", PreviousLogin: "old-acme", Login: "acme"},
				},
			},
		)
	}
	p := github.New(github.Options{})
	for _, tc := range cases {
		t.Run(tc.kind+"/"+tc.action, func(t *testing.T) {
			body := payload(t, tc.kind, tc.action, tc.edit)
			h := http.Header{"X-Github-Event": {tc.kind}, "X-Github-Delivery": {"72d3162e-7c72-11e3-81e9-123456789abc"}}
			got, err := p.Parse(h, body)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff([]vcsapi.Event{tc.want}, got); diff != "" {
				t.Fatalf("normalized event (-want +got):\n%s", diff)
			}
			if p.Event(h, body) != tc.kind || !p.Consumed(tc.kind) || p.DeliveryID(h, body) != h.Get("X-GitHub-Delivery") {
				t.Fatal("consumed delivery metadata changed")
			}
			wantRepository := "7"
			if tc.kind == "installation" || tc.kind == "installation_repositories" || tc.kind == "installation_target" || tc.kind == "organization" {
				wantRepository = "71"
			}
			if got := p.Repository(body); got != wantRepository {
				t.Fatalf("ordering repository = %s, want %s", got, wantRepository)
			}
		})
	}
}

func TestUnitGitHubFiltered(t *testing.T) {
	for _, tc := range []struct{ kind, action string }{
		{"ping", ""},
		{"pull_request", "assigned"},
		{"pull_request_review", "edited"},
		{"issue_comment", "edited"},
		{"issue_comment", "deleted"},
		{"check_run", "completed"},
		{"repository", "created"},
		{"installation", "unrecognized"},
		{"installation_repositories", "unrecognized"},
		{"installation_target", "unrecognized"},
		{"organization", "member_added"},
	} {
		t.Run(tc.kind+"/"+tc.action, func(t *testing.T) {
			assertFiltered(t, tc.kind, payload(t, tc.kind, tc.action, nil))
		})
	}
	t.Run("plain issue comment", func(t *testing.T) {
		assertFiltered(t, "issue_comment", payload(t, "issue_comment", "created", func(d map[string]any) { delete(d["issue"].(map[string]any), "pull_request") }))
	})
	t.Run("non branch or tag push", func(t *testing.T) {
		assertFiltered(t, "push", payload(t, "push", "", func(d map[string]any) { d["ref"] = "refs/pull/12/head" }))
	})
	t.Run("unknown family", func(t *testing.T) { assertFiltered(t, "future_event", []byte("not JSON")) })
}

func assertFiltered(t *testing.T, kind string, body []byte) {
	t.Helper()
	p := github.New(github.Options{})
	h := http.Header{"X-Github-Event": {kind}}
	if got, err := p.Parse(h, body); err != nil || len(got) != 0 {
		t.Fatalf("filtered payload = %+v, %v", got, err)
	}
	if p.Consumed(p.Event(h, body)) {
		t.Fatal("filtered event would be stored")
	}
}

func TestUnitGitHubPayloadRefusals(t *testing.T) {
	p := github.New(github.Options{})
	for _, body := range []string{"{", "null", `{}`, `{"action":true}`, `{} {}`} {
		t.Run(body, func(t *testing.T) {
			h := http.Header{"X-Github-Event": {"pull_request"}}
			if _, err := p.Parse(h, []byte(body)); !errors.Is(err, github.ErrWebhookPayload) {
				t.Fatalf("malformed payload: %v", err)
			}
			if p.Event(h, []byte(body)) != "pull_request" {
				t.Fatal("malformed consumed payload was silently filtered")
			}
		})
	}
	for _, tc := range []struct{ kind, body string }{
		{"push", `{}`},
		{"push", `{"ref":"refs/heads/"}`},
		{"issue_comment", `{"action":"created"}`},
	} {
		t.Run("missing routing/"+tc.kind+tc.body, func(t *testing.T) {
			h := http.Header{"X-Github-Event": {tc.kind}}
			if _, err := p.Parse(h, []byte(tc.body)); !errors.Is(err, github.ErrWebhookPayload) || p.Event(h, []byte(tc.body)) != tc.kind {
				t.Fatalf("malformed routing was filtered: %v", err)
			}
		})
	}
	for _, tc := range []struct {
		kind, action, field string
	}{
		{"push", "", "repository"},
		{"push", "", "after"},
		{"pull_request", "opened", "pull_request"},
		{"issue_comment", "created", "comment"},
		{"check_run", "requested_action", "requested_action"},
		{"installation", "created", "installation"},
		{"installation_target", "renamed", "account"},
		{"organization", "renamed", "changes"},
	} {
		t.Run(tc.kind+"/"+tc.field, func(t *testing.T) {
			b := payload(t, tc.kind, tc.action, func(d map[string]any) { delete(d, tc.field) })
			if _, err := p.Parse(http.Header{"X-Github-Event": {tc.kind}}, b); !errors.Is(err, github.ErrWebhookPayload) {
				t.Fatalf("missing required %s: %v", tc.field, err)
			}
		})
	}
	if p.Repository([]byte("{")) != "" || p.Repository([]byte("{}")) != "" {
		t.Fatal("invalid payload produced an ordering identity")
	}
}

func TestUnitGitHubPullRequestEvidence(t *testing.T) {
	p := github.New(github.Options{})
	for _, tc := range []struct {
		name      string
		mergeable any
		state     any
		want      bool
	}{
		{"clean without divergence evidence", true, "clean", true},
		{"blocked", true, "blocked", false},
		{"conflicts", false, "dirty", false},
		{"unknown", nil, "unknown", false},
		{"missing state", true, nil, false},
		{"clean but no mergeable", nil, "clean", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := payload(t, "pull_request_review", "submitted", func(d map[string]any) {
				pr := d["pull_request"].(map[string]any)
				pr["mergeable"], pr["mergeable_state"] = tc.mergeable, tc.state
			})
			got, err := p.Parse(http.Header{"X-Github-Event": {"pull_request_review"}}, b)
			if err != nil || len(got) != 1 {
				t.Fatalf("review = %+v, %v", got, err)
			}
			if got[0].PullRequest.Approved || got[0].PullRequest.Undiverged || got[0].PullRequest.Mergeable != tc.want {
				t.Fatalf("payload invented aggregate approval/divergence or changed mergeability: %+v", got[0].PullRequest)
			}
		})
	}
	t.Run("same repository", func(t *testing.T) {
		b := payload(t, "pull_request", "opened", func(d map[string]any) {
			pr := d["pull_request"].(map[string]any)
			pr["head"].(map[string]any)["repo"] = pr["base"].(map[string]any)["repo"]
		})
		got, err := p.Parse(http.Header{"X-Github-Event": {"pull_request"}}, b)
		if err != nil || got[0].PullRequest.Fork || got[0].PullRequest.HeadOwner != "acme" {
			t.Fatalf("same-repository request = %+v, %v", got, err)
		}
	})
	t.Run("repository list full name", func(t *testing.T) {
		b := payload(t, "installation", "created", func(d map[string]any) {
			delete(d["repositories"].([]any)[0].(map[string]any), "owner")
		})
		got, err := p.Parse(http.Header{"X-Github-Event": {"installation"}}, b)
		if err != nil || got[0].Installation.Repositories[0].Namespace != "acme" {
			t.Fatalf("installation repository = %+v, %v", got, err)
		}
	})
	t.Run("incomplete installation repository", func(t *testing.T) {
		b := payload(t, "installation", "created", func(d map[string]any) {
			r := d["repositories"].([]any)[0].(map[string]any)
			delete(r, "owner")
			r["full_name"] = "infra"
		})
		if _, err := p.Parse(http.Header{"X-Github-Event": {"installation"}}, b); !errors.Is(err, github.ErrWebhookPayload) {
			t.Fatalf("incomplete repository = %v", err)
		}
	})
	t.Run("exact integer identity", func(t *testing.T) {
		b := payload(t, "push", "", nil)
		b = []byte(strings.Replace(string(b), `"id":7,`, `"id":9007199254740993,`, 1))
		got, err := p.Parse(http.Header{"X-Github-Event": {"push"}}, b)
		if err != nil || got[0].Repository.ID != "9007199254740993" || p.Repository(b) != "9007199254740993" {
			t.Fatalf("integer identity lost precision = %+v, %v", got, err)
		}
	})
}
