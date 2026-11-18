package azuredevops_test

import (
	"net/url"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/azuredevops"
)

func TestUnitAzureRequestMetadata(t *testing.T) {
	t.Parallel()
	p := azuredevops.New(azuredevops.Options{})
	for _, state := range []string{"active", "abandoned", "completed"} {
		t.Run(state, func(t *testing.T) {
			body := azureFixture(t, "pull_request", func(d map[string]any) {
				d["eventType"] = "git.pullrequest.updated"
				d["createdDate"] = "2026-01-01T00:01:00Z"
				r := d["resource"].(map[string]any)
				r["title"] = "Infrastructure change"
				r["creationDate"] = "2026-01-01T00:00:00Z"
				r["status"] = state
				r["repository"].(map[string]any)["remoteUrl"] = "https://dev.azure.com/acme/Platform/_git/infra"
			})
			events, err := p.Parse(nil, body)
			if err != nil || len(events) != 1 {
				t.Fatal(err)
			}
			r := events[0].PullRequest
			want := map[string]string{"active": "open", "abandoned": "closed", "completed": "merged"}[state]
			branch, err := url.Parse(r.HeadBranchURL)
			if err != nil || branch.Query().Get("version") != "GBfeature" {
				t.Fatal("wrong head branch URL", r.HeadBranchURL, err)
			}
			if r.State != want || r.AuthorLogin != "author@example.com" || r.Title != "Infrastructure change" ||
				r.URL != "https://dev.azure.com/acme/Platform/_git/infra/pullrequest/42" ||
				r.CreatedAt.Unix() != 1767225600 ||
				!r.UpdatedAt.Equal(r.CreatedAt.Add(time.Minute)) {
				t.Fatalf("lost native request metadata: %+v", r)
			}
		})
	}
}
