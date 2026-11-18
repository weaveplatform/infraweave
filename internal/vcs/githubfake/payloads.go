package githubfake

import (
	"encoding/json"
	"errors"
)

// ErrEvent is an event family the fixture builder does not describe.
var ErrEvent = errors.New("githubfake: unknown payload event")

// The fixture commits have GitHub's forty-hex-digit SHA shape.
const (
	BeforeSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	AfterSHA  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	HeadSHA   = "cccccccccccccccccccccccccccccccccccccccc"
)

// Payload builds documented GitHub webhook shapes with synthetic identities.
// It retains the fields normalization consumes; every fixture e-mail uses
// example.com. Callers can edit the JSON to exercise absent/unknown fields.
// https://docs.github.com/en/webhooks/webhook-events-and-payloads
func Payload(event, action string) ([]byte, error) {
	repo := repositoryPayload(7, "acme", "infra")
	owner := map[string]any{"id": 41, "login": "acme", "type": "Organization"}
	sender := map[string]any{"id": 17, "login": AuthorLogin, "email": "author@example.com"}
	in := map[string]any{"id": 71, "account": owner, "app_id": 101}
	d := map[string]any{"action": action, "repository": repo, "sender": sender, "installation": in}
	switch event {
	case "push":
		delete(d, "action")
		d["ref"], d["before"], d["after"], d["forced"] = "refs/heads/main", BeforeSHA, AfterSHA, true
		d["pusher"] = map[string]string{"name": AuthorLogin, "email": "author@example.com"}
		d["head_commit"] = map[string]any{
			"id":        AfterSHA,
			"message":   "Update infrastructure",
			"timestamp": "2026-01-01T00:00:00Z",
			"author":    map[string]string{"name": AuthorName, "email": AuthorEmail, "username": AuthorLogin},
		}
	case "pull_request", "pull_request_review":
		d["number"], d["pull_request"] = 12, pullRequestPayload(repo, sender)
		if event == "pull_request_review" {
			d["review"] = map[string]any{"id": 93, "state": "approved", "user": sender, "body": "Looks good"}
		}
	case "issue_comment":
		d["issue"] = map[string]any{"number": 12, "pull_request": map[string]string{"url": "https://api.github.com/repos/acme/infra/pulls/12"}}
		d["comment"] = map[string]any{"id": 94, "body": "/spacelift preview", "user": sender}
	case "check_run":
		d["check_run"] = map[string]any{"id": 95, "head_sha": AfterSHA, "external_id": "stack-7"}
		d["requested_action"] = map[string]string{"identifier": "preview"}
	case "repository":
		d["changes"] = map[string]any{"repository": map[string]any{"name": map[string]string{"from": "old-infra"}}}
	case "installation":
		delete(d, "repository")
		d["repositories"] = []any{repo}
	case "installation_repositories":
		delete(d, "repository")
		d["repositories_added"] = []any{repo}
		d["repositories_removed"] = []any{repositoryPayload(8, "acme", "retired")}
	case "installation_target", "organization":
		delete(d, "repository")
		d["changes"] = map[string]any{"login": map[string]string{"from": "old-acme"}}
		if event == "installation_target" {
			d["account"], d["target_type"] = owner, "Organization"
		} else {
			d["organization"] = owner
		}
	case "ping":
		d = map[string]any{"zen": "Keep it logically awesome.", "hook_id": 9}
	default:
		return nil, ErrEvent
	}
	b, err := json.Marshal(d)
	if err != nil {
		panic(err) // fixture maps contain only JSON values
	}
	return b, nil
}

func repositoryPayload(id int64, owner, name string) map[string]any {
	return map[string]any{
		"id": id, "name": name, "full_name": owner + "/" + name,
		"owner": map[string]any{"id": 41, "login": owner, "email": "owner@example.com"},
	}
}

func pullRequestPayload(repo, sender map[string]any) map[string]any {
	return map[string]any{
		"number": 12, "user": sender, "draft": false, "merged": false,
		"mergeable": true, "mergeable_state": "clean",
		"labels": []any{map[string]string{"name": "infrastructure"}},
		"head":   map[string]any{"ref": "feature", "sha": HeadSHA, "repo": repositoryPayload(9, "contributor", "infra")},
		"base":   map[string]any{"ref": "main", "sha": BeforeSHA, "repo": repo},
	}
}
