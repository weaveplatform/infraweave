package bitbucketcloud_test

import (
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketcloudfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/bitbucketcloud"
)

func TestUnitCloudRequestMetadataAndClosure(t *testing.T) {
	t.Parallel()
	p := bitbucketcloud.New(bitbucketcloud.Options{})
	for _, tc := range []struct{ event, state, action string }{{"pullrequest:created", "open", "opened"}, {"pullrequest:updated", "open", "edited"}, {"pullrequest:fulfilled", "merged", "merged"}, {"pullrequest:rejected", "closed", "closed"}} {
		t.Run(tc.event, func(t *testing.T) {
			body := cloudBody(t, tc.event, func(d map[string]any) {
				d["actor"] = map[string]any{"nickname": "reviewer"}
				d["pullrequest"].(map[string]any)["draft"] = true
			})
			events, err := p.Parse(cloudHeaders(tc.event), body)
			if err != nil || len(events) != 1 {
				t.Fatal(err)
			}
			e := events[0]
			r := e.PullRequest
			if !p.Consumed(tc.event) || e.Action != tc.action || e.Author != "reviewer" || e.Head != bitbucketcloudfake.HeadSHA || r.AuthorLogin != "author" ||
				r.Title != "Infrastructure change" ||
				r.URL != "https://bitbucket.org/acme/infra/pull-requests/12" ||
				r.HeadBranchURL != "https://bitbucket.org/contributor/infra/src/feature" ||
				r.State != tc.state ||
				!r.Draft ||
				r.CreatedAt.Unix() != 1767225600 ||
				!r.UpdatedAt.Equal(r.CreatedAt.Add(time.Minute)) {
				t.Fatalf("lost native request metadata: %+v %+v", e, r)
			}
		})
	}
}

func TestUnitCloudRequestHeadRequiredExceptTerminal(t *testing.T) {
	p := bitbucketcloud.New(bitbucketcloud.Options{})
	for _, head := range []string{"", "refs/heads/main", strings.Repeat("0", 40), strings.Repeat("z", 40)} {
		for _, kind := range []string{"pullrequest:created", "pullrequest:rejected", "pullrequest:fulfilled"} {
			t.Run(kind+"/"+head, func(t *testing.T) {
				body := cloudBody(t, kind, func(d map[string]any) {
					d["pullrequest"].(map[string]any)["source"].(map[string]any)["commit"].(map[string]any)["hash"] = head
				})
				events, err := p.Parse(cloudHeaders(kind), body)
				if kind == "pullrequest:created" {
					if err == nil {
						t.Fatal("noncommit open head accepted")
					}
					return
				}
				if err != nil || len(events) != 1 {
					t.Fatal("terminal close with deleted branch was lost", events, err)
				}
			})
		}
	}
}

func TestUnitCloudSupersededUpdateClosesRequest(t *testing.T) {
	p := bitbucketcloud.New(bitbucketcloud.Options{})
	body := cloudBody(t, "pullrequest:updated", func(d map[string]any) { d["pullrequest"].(map[string]any)["state"] = "SUPERSEDED" })
	events, err := p.Parse(cloudHeaders("pullrequest:updated"), body)
	if err != nil || len(events) != 1 || events[0].Action != "edited" || events[0].PullRequest.State != "closed" {
		t.Fatal("superseded update can reopen obsolete request", events, err)
	}
}
