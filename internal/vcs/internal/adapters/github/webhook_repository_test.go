package github_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/github"
)

// Push webhooks carry repository.organization as a string. REST repositories
// use an object there; neither representation contributes to event identity.
func TestUnitGitHubPushRepositoryMetadata(t *testing.T) {
	const body = `{"ref":"refs/heads/main","before":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","after":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","repository":{"id":7,"name":"infra","owner":{"id":41,"login":"acme"},"organization":"acme"},"sender":{"login":"dev"}}`
	header := http.Header{"X-Github-Event": {"push"}}
	p := github.New(github.Options{})
	if got := p.Event(header, []byte(body)); got != "push" {
		t.Fatalf("event = %q, want push", got)
	}
	if got := p.Repository([]byte(body)); got != "7" {
		t.Fatalf("ordering identity = %q, want 7", got)
	}
	got, err := p.Parse(header, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	want := []vcsapi.Event{{
		Type: "push", Repository: vcsapi.Repository{ID: "7", Namespace: "acme", Name: "infra", OwnerID: "41"},
		Branch: "main", Before: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Head: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Author: "dev",
	}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatal(diff)
	}
}

func TestUnitGitHubWebhookRepositoryMetadataControls(t *testing.T) {
	p := github.New(github.Options{})
	for _, tc := range []struct{ kind, action string }{{"repository", "renamed"}, {"pull_request", "opened"}, {"installation", "created"}} {
		t.Run(tc.kind, func(t *testing.T) {
			body := payload(t, tc.kind, tc.action, func(d map[string]any) {
				var repo map[string]any
				if tc.kind == "installation" {
					repo = d["repositories"].([]any)[0].(map[string]any)
				} else {
					repo = d["repository"].(map[string]any)
				}
				repo["organization"] = map[string]any{"id": 41, "login": "acme"}
			})
			got, err := p.Parse(http.Header{"X-Github-Event": {tc.kind}}, body)
			if err != nil || len(got) != 1 {
				t.Fatalf("non-push repository metadata = %v", err)
			}
		})
	}
	t.Run("unconsumed ref", func(t *testing.T) {
		body := payload(t, "push", "", func(d map[string]any) { d["ref"] = "refs/notes/review" })
		header := http.Header{"X-Github-Event": {"push"}}
		if p.Event(header, body) != "" {
			t.Fatal("unused push metadata prevented filtering an unconsumed ref")
		}
		got, err := p.Parse(header, body)
		if err != nil || len(got) != 0 {
			t.Fatal("unconsumed ref was normalized")
		}
	})
	t.Run("fixture matches native push shape", func(t *testing.T) {
		body := payload(t, "push", "", nil)
		var decoded struct {
			Repository struct {
				Organization string `json:"organization"`
			} `json:"repository"`
		}
		if err := json.Unmarshal(body, &decoded); err != nil || decoded.Repository.Organization != "acme" {
			t.Fatal("fake omitted the native push organization field")
		}
	})
}
