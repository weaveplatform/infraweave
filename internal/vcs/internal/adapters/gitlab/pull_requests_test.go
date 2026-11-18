package gitlab_test

import (
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/gitlab"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func TestUnitGitLabOriginalAuthorAndMetadata(t *testing.T) {
	l := newLab(t)
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, action := range []string{"open", "reopen", "update", "close", "merge"} {
		t.Run(action, func(t *testing.T) {
			delivery, err := l.fake.MergeRequestWebhook(
				"acme/platform",
				"infra",
				gitlabfake.WebhookMergeRequest{
					IID:          17,
					Action:       action,
					SourceBranch: "feature",
					TargetBranch: "main",
					Head:         gitlabHead,
					Author:       "creator",
					Actor:        "reviewer",
					Title:        "changes",
					CreatedAt:    created,
					UpdatedAt:    created.Add(time.Minute),
					Draft:        true,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			events, err := l.gl.Parse(gitlabHeaders(delivery.Event), delivery.Body)
			if err != nil || len(events) != 1 {
				t.Fatal(err)
			}
			e := events[0]
			r := e.PullRequest
			if e.Author != "reviewer" || r.AuthorLogin != "" || r.AuthorID == "" || r.Title != "changes" ||
				r.URL != "https://gitlab.example.com/acme/platform/infra/-/merge_requests/17" ||
				r.HeadBranchURL != "https://gitlab.example.com/acme/platform/infra/-/tree/feature" ||
				!r.CreatedAt.Equal(created) ||
				!r.UpdatedAt.Equal(created.Add(time.Minute)) {
				t.Fatalf("actor replaced creator or lost snapshot: %+v %+v", e, r)
			}
			before := l.fake.Requests()
			login, err := l.gl.AuthorLogin(t.Context(), l.in, r.AuthorID)
			if err != nil || login != "creator" || l.fake.Requests() != before+1 {
				t.Fatal("narrow creator lookup failed", login, err)
			}
		})
	}
	for _, identity := range []string{"bad", "0", "999999"} {
		if _, err := l.gl.AuthorLogin(t.Context(), l.in, identity); err == nil {
			t.Fatal("invalid/missing author accepted", identity)
		}
	}
	if _, err := l.gl.AuthorLogin(t.Context(), port.Integration{}, "1"); !errors.Is(err, gitlab.ErrCredential) {
		t.Fatal("missing credentials hidden", err)
	}
}

func TestUnitGitLabRequestTimestampFormats(t *testing.T) {
	for _, stamp := range []string{"2026-01-01 00:00:00 UTC", "2026-01-01 00:00:00 +0000", "2026-01-01T00:00:00.000Z", "invalid"} {
		t.Run(stamp, func(t *testing.T) {
			body := webhookFixture(t, "merge_request", func(d map[string]any) {
				r := d["object_attributes"].(map[string]any)
				r["created_at"], r["updated_at"] = stamp, stamp
			})
			p := gitlab.New(gitlab.Options{})
			events, err := p.Parse(gitlabHeaders("Merge Request Hook"), body)
			if stamp == "invalid" {
				if err == nil || err.Error() != "gitlab: invalid webhook payload" {
					t.Fatal("bad timestamp accepted", err)
				}
				return
			}
			if err != nil || events[0].PullRequest.CreatedAt.Unix() != 1767225600 {
				t.Fatal("native timestamp lost", err)
			}
		})
	}
}
