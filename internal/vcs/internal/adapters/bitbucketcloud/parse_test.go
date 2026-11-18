package bitbucketcloud_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketcloudfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/bitbucketcloud"
)

func cloudBody(t *testing.T, event string, change func(map[string]any)) []byte {
	t.Helper()
	b, err := bitbucketcloudfake.Payload(event)
	if err != nil {
		t.Fatal(err)
	}
	if change == nil {
		return b
	}
	var d map[string]any
	if err = json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	change(d)
	b, err = json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func cloudHeaders(event string) http.Header {
	return http.Header{"X-Event-Key": {event}, "X-Request-Uuid": {"delivery"}}
}

func TestUnitBitbucketCloudWebhookActions(t *testing.T) {
	t.Parallel()
	p := bitbucketcloud.New(bitbucketcloud.Options{})
	for _, tc := range []struct {
		event, kind, action string
		approved            bool
	}{
		{"repo:push", "push", "", false},
		{"pullrequest:created", "pull_request", "opened", false},
		{"pullrequest:updated", "pull_request", "edited", true},
		{"pullrequest:approved", "pull_request", "edited", true},
		{"pullrequest:unapproved", "pull_request", "edited", false},
		{"pullrequest:fulfilled", "pull_request", "merged", true},
		{"pullrequest:comment_created", "comment", "created", false},
	} {
		t.Run(tc.event, func(t *testing.T) {
			t.Parallel()
			h, b := cloudHeaders(tc.event), cloudBody(t, tc.event, nil)
			if !p.Consumed(tc.event) || p.Event(h, b) != tc.event || p.DeliveryID(h, b) != "delivery" || p.Repository(b) != bitbucketcloudfake.RepositoryUUID {
				t.Fatal("routing metadata differs")
			}
			events, err := p.Parse(h, b)
			if err != nil || len(events) != 1 {
				t.Fatalf("events=%v err=%v", events, err)
			}
			e := events[0]
			if e.Type != tc.kind || e.Action != tc.action || e.Repository.ID != bitbucketcloudfake.RepositoryUUID || e.Repository.Namespace != "acme" ||
				e.Repository.Name != "infra" {
				t.Fatalf("event=%+v", e)
			}
			switch e.Type {
			case "push":
				if e.Before != bitbucketcloudfake.BeforeSHA || e.Head != bitbucketcloudfake.AfterSHA || e.Branch != "main" || !e.Forced {
					t.Fatalf("push=%+v", e)
				}
			case "pull_request":
				r := e.PullRequest
				if r == nil || r.Number != 12 || r.HeadBranch != "feature" || r.BaseBranch != "main" || r.HeadOwner != "contributor" || !r.Fork || r.Approved != tc.approved ||
					r.Mergeable ||
					r.Undiverged ||
					r.Labels == nil ||
					e.Head != bitbucketcloudfake.HeadSHA {
					t.Fatalf("request=%+v event=%+v", r, e)
				}
			case "comment":
				c := e.Comment
				if c == nil || c.Body != "/spacelift preview" || c.Author != "reviewer" || c.PullRequest != 12 || c.Head != bitbucketcloudfake.HeadSHA || c.BaseBranch != "main" ||
					!c.Fork {
					t.Fatalf("comment=%+v", c)
				}
			}
		})
	}
	if p.Consumed("repo:fork") {
		t.Fatal("unconsumed family accepted")
	}
	if events, err := p.Parse(cloudHeaders("repo:fork"), []byte("malformed")); err != nil || len(events) != 0 {
		t.Fatal("filtered event parsed")
	}
}

func TestUnitBitbucketCloudApproval(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, event string
		change      func(map[string]any)
		want        bool
	}{
		{"author cannot approve", "pullrequest:updated", func(d map[string]any) {
			r := d["pullrequest"].(map[string]any)
			r["participants"].([]any)[0].(map[string]any)["user"] = r["author"]
		}, false},
		{"unknown author", "pullrequest:updated", func(d map[string]any) {
			d["pullrequest"].(map[string]any)["author"] = map[string]any{"nickname": "author"}
		}, false},
		{"unknown reviewer", "pullrequest:updated", func(d map[string]any) {
			d["pullrequest"].(map[string]any)["participants"].([]any)[0].(map[string]any)["user"] = map[string]any{"nickname": "reviewer"}
		}, false},
		{"account identity fallback", "pullrequest:updated", func(d map[string]any) {
			r := d["pullrequest"].(map[string]any)
			r["author"] = map[string]any{"account_id": "author"}
			r["participants"].([]any)[0].(map[string]any)["user"] = map[string]any{"account_id": "reviewer"}
		}, true},
		{"root approval without snapshot", "pullrequest:approved", func(d map[string]any) { d["pullrequest"].(map[string]any)["participants"] = []any{} }, true},
		{"root author approval", "pullrequest:approved", func(d map[string]any) {
			r := d["pullrequest"].(map[string]any)
			r["participants"] = []any{}
			d["approval"].(map[string]any)["user"] = r["author"]
		}, false},
		{"revocation overrides stale snapshot", "pullrequest:unapproved", func(d map[string]any) {
			d["pullrequest"].(map[string]any)["participants"].([]any)[0].(map[string]any)["approved"] = true
		}, false},
		{"other approver survives revocation", "pullrequest:unapproved", func(d map[string]any) {
			r := d["pullrequest"].(map[string]any)
			r["participants"] = []any{map[string]any{"approved": true, "user": map[string]any{"uuid": "other-reviewer"}}}
		}, true},
		{"missing approval proof", "pullrequest:approved", func(d map[string]any) {
			r := d["pullrequest"].(map[string]any)
			r["participants"] = []any{}
			delete(d, "approval")
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := bitbucketcloud.New(bitbucketcloud.Options{})
			events, err := p.Parse(cloudHeaders(tc.event), cloudBody(t, tc.event, tc.change))
			if err != nil || len(events) != 1 || events[0].PullRequest.Approved != tc.want {
				t.Fatalf("events=%+v err=%v", events, err)
			}
		})
	}
}

func TestUnitBitbucketCloudMultipleRefs(t *testing.T) {
	t.Parallel()
	b := cloudBody(t, "repo:push", func(d map[string]any) {
		changes := d["push"].(map[string]any)["changes"].([]any)
		ref := func(kind, name, hash string) map[string]any {
			return map[string]any{"type": kind, "name": name, "target": map[string]string{"hash": hash}}
		}
		d["push"].(map[string]any)["changes"] = append(changes,
			map[string]any{"old": nil, "new": ref("tag", "v1", bitbucketcloudfake.AfterSHA)},
			map[string]any{"old": ref("branch", "deleted", bitbucketcloudfake.BeforeSHA), "new": nil},
			map[string]any{"old": nil, "new": ref("branch", "new", bitbucketcloudfake.HeadSHA)},
			map[string]any{"old": nil, "new": ref("bookmark", "ignored", bitbucketcloudfake.HeadSHA)},
		)
	})
	events, err := bitbucketcloud.New(bitbucketcloud.Options{}).Parse(cloudHeaders("repo:push"), b)
	if err != nil || len(events) != 4 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	zero := strings.Repeat("0", 40)
	if events[1].Type != "tag" || events[1].Tag != "v1" || events[1].Before != zero || events[2].Head != zero || events[2].Branch != "deleted" || events[3].Before != zero ||
		events[3].Branch != "new" {
		t.Fatalf("refs=%+v", events)
	}
}

func TestUnitBitbucketCloudPayloadRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, event string
		change      func(map[string]any)
	}{
		{"no repository", "repo:push", func(d map[string]any) { delete(d, "repository") }},
		{"no identity", "repo:push", func(d map[string]any) { delete(d["repository"].(map[string]any), "uuid") }},
		{"no path", "repo:push", func(d map[string]any) {
			r := d["repository"].(map[string]any)
			delete(r, "full_name")
			delete(r, "workspace")
		}},
		{"no push", "repo:push", func(d map[string]any) { delete(d, "push") }},
		{"empty changes", "repo:push", func(d map[string]any) { d["push"].(map[string]any)["changes"] = []any{} }},
		{"old omitted", "repo:push", func(d map[string]any) {
			delete(d["push"].(map[string]any)["changes"].([]any)[0].(map[string]any), "old")
		}},
		{"null refs", "repo:push", func(d map[string]any) {
			c := d["push"].(map[string]any)["changes"].([]any)[0].(map[string]any)
			c["old"], c["new"] = nil, nil
		}},
		{"ref wrong type", "repo:push", func(d map[string]any) { d["push"].(map[string]any)["changes"].([]any)[0].(map[string]any)["new"] = 42 }},
		{"hash missing", "repo:push", func(d map[string]any) {
			d["push"].(map[string]any)["changes"].([]any)[0].(map[string]any)["new"].(map[string]any)["target"] = map[string]any{}
		}},
		{"no request", "pullrequest:updated", func(d map[string]any) { delete(d, "pullrequest") }},
		{"no source", "pullrequest:updated", func(d map[string]any) { delete(d["pullrequest"].(map[string]any), "source") }},
		{"wrong destination", "pullrequest:updated", func(d map[string]any) {
			d["pullrequest"].(map[string]any)["destination"].(map[string]any)["repository"].(map[string]any)["uuid"] = "another"
		}},
		{"no comment", "pullrequest:comment_created", func(d map[string]any) { delete(d, "comment") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := bitbucketcloud.New(bitbucketcloud.Options{})
			b := cloudBody(t, tc.event, tc.change)
			if _, err := p.Parse(cloudHeaders(tc.event), b); !errors.Is(err, bitbucketcloud.ErrWebhookPayload) {
				t.Fatalf("refusal=%v", err)
			}
			if p.Event(cloudHeaders(tc.event), b) != tc.event {
				t.Fatal("malformed consumed event silently filtered")
			}
		})
	}
	p := bitbucketcloud.New(bitbucketcloud.Options{})
	for _, b := range [][]byte{[]byte("private-malformed"), []byte(`{"repository":42}`)} {
		if _, err := p.Parse(cloudHeaders("repo:push"), b); !errors.Is(err, bitbucketcloud.ErrWebhookPayload) || strings.Contains(err.Error(), "private-malformed") {
			t.Fatalf("unsafe refusal=%v", err)
		}
		if p.Repository(b) != "" {
			t.Fatal("bad metadata retained")
		}
	}
	b := cloudBody(t, "repo:push", func(d map[string]any) {
		c := d["push"].(map[string]any)["changes"].([]any)[0].(map[string]any)
		c["old"] = nil
		c["new"].(map[string]any)["type"] = "bookmark"
	})
	if p.Event(cloudHeaders("repo:push"), b) != "" {
		t.Fatal("valid unsupported ref not filtered")
	}
	b = cloudBody(t, "pullrequest:updated", func(d map[string]any) {
		r := d["repository"].(map[string]any)
		delete(r, "slug")
		delete(r, "workspace")
		r["name"] = "Human display name"
	})
	events, err := p.Parse(cloudHeaders("pullrequest:updated"), b)
	if err != nil || len(events) != 1 || events[0].Repository.Name != "infra" || events[0].Repository.Namespace != "acme" {
		t.Fatalf("full-name fallback=%+v err=%v", events, err)
	}
}
