package bitbucketcloud_test

import (
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
