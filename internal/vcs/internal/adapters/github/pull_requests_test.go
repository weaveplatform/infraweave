package github_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/vcs/githubfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/github"
)

func TestUnitGitHubRequestMetadata(t *testing.T) {
	t.Parallel()
	p := github.New(github.Options{})
	for _, action := range []string{"opened", "reopened", "synchronize", "edited", "closed"} {
		t.Run(action, func(t *testing.T) {
			body := payload(t, "pull_request", action, func(d map[string]any) {
				d["sender"] = map[string]any{"login": "reviewer"}
				pr := d["pull_request"].(map[string]any)
				pr["user"] = map[string]any{"login": "creator"}
				pr["draft"] = true
				pr["merge_commit_sha"] = githubfake.AfterSHA
			})
			events, err := p.Parse(http.Header{"X-Github-Event": {"pull_request"}}, body)
			if err != nil || len(events) != 1 {
				t.Fatal(err)
			}
			e := events[0]
			r := e.PullRequest
			state := "open"
			if action == "closed" {
				state = "closed"
			}
			if e.Author != "reviewer" || e.Head != githubfake.HeadSHA || r.AuthorLogin != "creator" || r.Title != "Infrastructure change" ||
				r.URL != "https://github.example.com/acme/infra/pull/12" ||
				r.HeadBranchURL != "https://github.example.com/contributor/infra/tree/feature" ||
				!r.Draft ||
				r.State != state ||
				r.CreatedAt.Unix() != 1767225600 ||
				!r.UpdatedAt.Equal(r.CreatedAt.Add(time.Minute)) {
				t.Fatalf("request lost original metadata/head: %+v %+v", e, r)
			}
		})
	}
}
