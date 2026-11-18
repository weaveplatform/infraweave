package bitbucketdc_test

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketdcfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/bitbucketdc"
)

func TestUnitDCRequestMetadataAndClosure(t *testing.T) {
	t.Parallel()
	p := bitbucketdc.New(bitbucketdc.Options{})
	for _, tc := range []struct{ event, state, action string }{{"pr:opened", "open", "opened"}, {"pr:from_ref_updated", "open", "synchronize"}, {"pr:modified", "open", "edited"}, {"pr:merged", "merged", "merged"}, {"pr:declined", "closed", "closed"}} {
		t.Run(tc.event, func(t *testing.T) {
			body := dcBody(t, tc.event, func(d map[string]any) {
				d["actor"] = map[string]any{"name": "reviewer"}
				d["pullRequest"].(map[string]any)["draft"] = true
			})
			events, err := p.Parse(dcHeaders(tc.event), body)
			if err != nil || len(events) != 1 {
				t.Fatal(err)
			}
			e := events[0]
			r := e.PullRequest
			branch, err := url.Parse(r.HeadBranchURL)
			if err != nil || branch.Query().Get("at") != "refs/heads/feature" {
				t.Fatal("wrong source branch URL", r.HeadBranchURL, err)
			}
			if !p.Consumed(tc.event) || e.Action != tc.action || e.Author != "reviewer" || e.Head != bitbucketdcfake.HeadSHA || r.AuthorLogin != "author" ||
				r.Title != "Infrastructure change" ||
				r.URL != "https://bitbucket.example.com/projects/ACME/repos/infra/pull-requests/12" ||
				r.State != tc.state ||
				!r.Draft ||
				r.CreatedAt.Unix() != 1767225600 ||
				!r.UpdatedAt.Equal(r.CreatedAt.Add(time.Minute)) {
				t.Fatalf("lost native request metadata: %+v %+v", e, r)
			}
		})
	}
}

func TestUnitDCRequestHeadRequiredExceptTerminal(t *testing.T) {
	p := bitbucketdc.New(bitbucketdc.Options{})
	for _, head := range []string{"", "refs/heads/main", strings.Repeat("0", 40), strings.Repeat("z", 40)} {
		for _, kind := range []string{"pr:opened", "pr:declined", "pr:merged"} {
			t.Run(kind+"/"+head, func(t *testing.T) {
				body := dcBody(t, kind, func(d map[string]any) {
					d["pullRequest"].(map[string]any)["fromRef"].(map[string]any)["latestCommit"] = head
				})
				events, err := p.Parse(dcHeaders(kind), body)
				if kind == "pr:opened" {
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
