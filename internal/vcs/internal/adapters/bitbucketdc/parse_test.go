package bitbucketdc_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketdcfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/bitbucketdc"
)

func dcBody(t *testing.T, event string, change func(map[string]any)) []byte {
	t.Helper()
	b, err := bitbucketdcfake.Payload(event)
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

func dcHeaders(event string) http.Header {
	return http.Header{"X-Event-Key": {event}, "X-Request-Id": {"delivery"}}
}

func TestUnitBitbucketDCWebhookActions(t *testing.T) {
	t.Parallel()
	p := bitbucketdc.New(bitbucketdc.Options{})
	for _, tc := range []struct {
		event, kind, action string
		approved            bool
	}{
		{"repo:refs_changed", "push", "", false},
		{"pr:opened", "pull_request", "opened", false},
		{"pr:from_ref_updated", "pull_request", "synchronize", false},
		{"pr:modified", "pull_request", "edited", true},
		{"pr:reviewer:approved", "pull_request", "edited", true},
		{"pr:reviewer:unapproved", "pull_request", "edited", false},
		{"pr:merged", "pull_request", "merged", true},
		{"pr:comment:added", "comment", "created", false},
	} {
		t.Run(tc.event, func(t *testing.T) {
			t.Parallel()
			h, b := dcHeaders(tc.event), dcBody(t, tc.event, nil)
			if !p.Consumed(tc.event) || p.Event(h, b) != tc.event || p.DeliveryID(h, b) != "delivery" || p.Repository(b) != "7" {
				t.Fatal("routing metadata differs")
			}
			events, err := p.Parse(h, b)
			if err != nil || len(events) != 1 {
				t.Fatalf("events=%v err=%v", events, err)
			}
			e := events[0]
			if e.Type != tc.kind || e.Action != tc.action || e.Repository.ID != "7" || e.Repository.Namespace != "ACME" || e.Repository.Name != "infra" {
				t.Fatalf("event=%+v", e)
			}
			switch e.Type {
			case "push":
				if e.Before != bitbucketdcfake.BeforeSHA || e.Head != bitbucketdcfake.AfterSHA || e.Branch != "main" || e.Forced {
					t.Fatalf("push=%+v", e)
				}
			case "pull_request":
				r := e.PullRequest
				if r == nil || r.Number != 12 || r.HeadBranch != "feature" || r.BaseBranch != "main" || r.HeadOwner != "CONTRIBUTOR" || !r.Fork || r.Approved != tc.approved ||
					r.Mergeable ||
					r.Undiverged ||
					r.Labels == nil ||
					e.Head != bitbucketdcfake.HeadSHA {
					t.Fatalf("request=%+v event=%+v", r, e)
				}
				if tc.event == "pr:from_ref_updated" && e.Before != bitbucketdcfake.BeforeSHA {
					t.Fatal("previous head lost")
				}
			case "comment":
				c := e.Comment
				if c == nil || c.Body != "/spacelift preview" || c.Author != "reviewer" || c.PullRequest != 12 || c.Head != bitbucketdcfake.HeadSHA || c.BaseBranch != "main" ||
					!c.Fork {
					t.Fatalf("comment=%+v", c)
				}
			}
		})
	}
	for _, event := range []string{"diagnostics:ping", "repo:modified"} {
		if p.Consumed(event) {
			t.Fatal("unsupported event consumed")
		}
		if events, err := p.Parse(dcHeaders(event), []byte("malformed")); err != nil || len(events) != 0 {
			t.Fatal("unsupported event parsed")
		}
	}
}

func TestUnitBitbucketDCApproval(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, event string
		change      func(map[string]any)
		want        bool
	}{
		{"author cannot approve", "pr:modified", func(d map[string]any) {
			r := d["pullRequest"].(map[string]any)
			r["reviewers"].([]any)[0].(map[string]any)["user"] = r["author"].(map[string]any)["user"]
		}, false},
		{"unknown author", "pr:modified", func(d map[string]any) { d["pullRequest"].(map[string]any)["author"] = map[string]any{} }, false},
		{"unknown reviewer", "pr:modified", func(d map[string]any) {
			d["pullRequest"].(map[string]any)["reviewers"].([]any)[0].(map[string]any)["user"] = map[string]any{}
		}, false},
		{"status proof", "pr:modified", func(d map[string]any) {
			delete(d["pullRequest"].(map[string]any)["reviewers"].([]any)[0].(map[string]any), "approved")
		}, true},
		{"false boolean beats stale status", "pr:modified", func(d map[string]any) {
			d["pullRequest"].(map[string]any)["reviewers"].([]any)[0].(map[string]any)["approved"] = false
		}, false},
		{"root participant without snapshot", "pr:reviewer:approved", func(d map[string]any) { d["pullRequest"].(map[string]any)["reviewers"] = []any{} }, true},
		{"root author cannot approve", "pr:reviewer:approved", func(d map[string]any) {
			r := d["pullRequest"].(map[string]any)
			r["reviewers"] = []any{}
			d["participant"].(map[string]any)["user"] = r["author"].(map[string]any)["user"]
		}, false},
		{"revocation overrides stale snapshot", "pr:reviewer:unapproved", func(d map[string]any) {
			r := d["pullRequest"].(map[string]any)["reviewers"].([]any)[0].(map[string]any)
			r["approved"], r["status"] = true, "APPROVED"
		}, false},
		{"other approver survives revocation", "pr:reviewer:unapproved", func(d map[string]any) {
			d["pullRequest"].(map[string]any)["participants"] = []any{map[string]any{"approved": true, "user": map[string]any{"id": 33}}}
		}, true},
		{"missing proof", "pr:reviewer:approved", func(d map[string]any) {
			d["pullRequest"].(map[string]any)["reviewers"] = []any{}
			delete(d, "participant")
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := bitbucketdc.New(bitbucketdc.Options{})
			events, err := p.Parse(dcHeaders(tc.event), dcBody(t, tc.event, tc.change))
			if err != nil || len(events) != 1 || events[0].PullRequest.Approved != tc.want {
				t.Fatalf("events=%+v err=%v", events, err)
			}
		})
	}
}

func TestUnitBitbucketDCMultipleRefs(t *testing.T) {
	t.Parallel()
	b := dcBody(t, "repo:refs_changed", func(d map[string]any) {
		changes := d["changes"].([]any)
		zero := strings.Repeat("0", 40)
		d["changes"] = append(changes,
			map[string]any{"refId": "refs/tags/v1", "fromHash": zero, "toHash": bitbucketdcfake.AfterSHA},
			map[string]any{"refId": "refs/heads/deleted", "fromHash": bitbucketdcfake.BeforeSHA, "toHash": zero},
			map[string]any{"ref": map[string]any{"type": "BRANCH", "displayId": "new"}, "fromHash": zero, "toHash": bitbucketdcfake.HeadSHA},
			map[string]any{"ref": map[string]any{"type": "OTHER", "displayId": "ignored"}},
			map[string]any{"refId": "refs/other/ignored"},
		)
	})
	events, err := bitbucketdc.New(bitbucketdc.Options{}).Parse(dcHeaders("repo:refs_changed"), b)
	if err != nil || len(events) != 4 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	zero := strings.Repeat("0", 40)
	if events[1].Type != "tag" || events[1].Tag != "v1" || events[1].Before != zero || events[2].Head != zero || events[2].Branch != "deleted" || events[3].Before != zero ||
		events[3].Branch != "new" {
		t.Fatalf("refs=%+v", events)
	}
}

func TestUnitBitbucketDCPayloadRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, event string
		change      func(map[string]any)
	}{
		{"no event key", "repo:refs_changed", func(d map[string]any) { delete(d, "eventKey") }},
		{"mismatched event key", "repo:refs_changed", func(d map[string]any) { d["eventKey"] = "repo:modified" }},
		{"no repository", "repo:refs_changed", func(d map[string]any) { delete(d, "repository") }},
		{"no identity", "repo:refs_changed", func(d map[string]any) { delete(d["repository"].(map[string]any), "id") }},
		{"no changes", "repo:refs_changed", func(d map[string]any) { delete(d, "changes") }},
		{"empty ref", "repo:refs_changed", func(d map[string]any) {
			c := d["changes"].([]any)[0].(map[string]any)
			delete(c, "ref")
			delete(c, "refId")
		}},
		{"no name", "repo:refs_changed", func(d map[string]any) {
			c := d["changes"].([]any)[0].(map[string]any)
			c["ref"] = map[string]any{"type": "BRANCH"}
			delete(c, "refId")
		}},
		{"no hash", "repo:refs_changed", func(d map[string]any) { delete(d["changes"].([]any)[0].(map[string]any), "fromHash") }},
		{"no request", "pr:modified", func(d map[string]any) { delete(d, "pullRequest") }},
		{"no source", "pr:modified", func(d map[string]any) { delete(d["pullRequest"].(map[string]any), "fromRef") }},
		{"no target", "pr:modified", func(d map[string]any) { delete(d["pullRequest"].(map[string]any), "toRef") }},
		{"wrong top repository", "pr:modified", func(d map[string]any) { d["repository"] = map[string]any{"id": 99} }},
		{"no comment", "pr:comment:added", func(d map[string]any) { delete(d, "comment") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := bitbucketdc.New(bitbucketdc.Options{})
			b := dcBody(t, tc.event, tc.change)
			if _, err := p.Parse(dcHeaders(tc.event), b); !errors.Is(err, bitbucketdc.ErrWebhookPayload) {
				t.Fatalf("refusal=%v", err)
			}
			if p.Event(dcHeaders(tc.event), b) != tc.event {
				t.Fatal("malformed consumed event silently filtered")
			}
		})
	}
	p := bitbucketdc.New(bitbucketdc.Options{})
	for _, b := range [][]byte{[]byte("private-malformed"), []byte(`{"repository":{"id":"bad"}}`)} {
		if _, err := p.Parse(dcHeaders("repo:refs_changed"), b); !errors.Is(err, bitbucketdc.ErrWebhookPayload) || strings.Contains(err.Error(), "private-malformed") {
			t.Fatalf("unsafe refusal=%v", err)
		}
		if p.Repository(b) != "" {
			t.Fatal("bad metadata retained")
		}
	}
	b := dcBody(t, "repo:refs_changed", func(d map[string]any) {
		d["changes"].([]any)[0].(map[string]any)["ref"].(map[string]any)["type"] = "OTHER"
	})
	if p.Event(dcHeaders("repo:refs_changed"), b) != "" {
		t.Fatal("valid unsupported ref not filtered")
	}
	b = dcBody(t, "pr:modified", func(d map[string]any) {
		r := d["pullRequest"].(map[string]any)
		delete(r["fromRef"].(map[string]any), "displayId")
		delete(r["toRef"].(map[string]any), "displayId")
	})
	events, err := p.Parse(dcHeaders("pr:modified"), b)
	if err != nil || len(events) != 1 || events[0].PullRequest.BaseBranch != "main" || events[0].PullRequest.HeadBranch != "feature" {
		t.Fatalf("full-ref fallback=%+v err=%v", events, err)
	}
	b = []byte(strings.ReplaceAll(string(dcBody(t, "repo:refs_changed", nil)), `"id":7,`, `"id":9007199254740993,`))
	if p.Repository(b) != "9007199254740993" {
		t.Fatal("repository ID lost integer precision")
	}
	if p.Repository([]byte(`{"repository":{"id":0}}`)) != "" {
		t.Fatal("invalid ID accepted")
	}
}
