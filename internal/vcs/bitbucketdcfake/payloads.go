package bitbucketdcfake

import (
	"encoding/json"
	"errors"
)

// The fixture's stable repository identity and full commit hashes.
const (
	RepositoryID = 7
	BeforeSHA    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	AfterSHA     = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	HeadSHA      = "cccccccccccccccccccccccccccccccccccccccc"
)

// ErrEvent is a fixture family the builder does not describe.
var ErrEvent = errors.New("bitbucketdcfake: unknown payload event")

// Payload builds documented Data Center shapes with example.com addresses.
// https://confluence.atlassian.com/bitbucketserver/event-payload-938025882.html
func Payload(event string) ([]byte, error) {
	repo := payloadRepository(RepositoryID, "ACME")
	author, reviewer := payloadUser(11, "author"), payloadUser(22, "reviewer")
	d := map[string]any{"eventKey": event, "date": "2026-01-01T00:00:00+0000", "actor": author}
	switch event {
	case "repo:refs_changed":
		d["repository"] = repo
		d["changes"] = []any{map[string]any{
			"ref":   map[string]string{"id": "refs/heads/main", "displayId": "main", "type": "BRANCH"},
			"refId": "refs/heads/main", "fromHash": BeforeSHA, "toHash": AfterSHA, "type": "UPDATE",
		}}
	case "pr:opened", "pr:from_ref_updated", "pr:modified", "pr:reviewer:approved", "pr:reviewer:unapproved", "pr:merged", "pr:declined", "pr:comment:added":
		approved := event == "pr:reviewer:approved" || event == "pr:modified" || event == "pr:merged"
		status, state := "UNAPPROVED", "OPEN"
		if approved {
			status = "APPROVED"
		}
		if event == "pr:merged" {
			state = "MERGED"
		}
		if event == "pr:declined" {
			state = "DECLINED"
		}
		participant := map[string]any{"user": reviewer, "role": "REVIEWER", "approved": approved, "status": status}
		d["pullRequest"] = map[string]any{
			"id": 12, "version": 1, "title": "Infrastructure change", "state": state,
			"createdDate": 1767225600000, "updatedDate": 1767225660000,
			"links": map[string]any{"self": []any{map[string]string{"href": "https://bitbucket.example.com/projects/ACME/repos/infra/pull-requests/12"}}},
			"open":  state == "OPEN", "closed": state != "OPEN", "draft": false,
			"author":    map[string]any{"user": author, "role": "AUTHOR", "approved": false, "status": "UNAPPROVED"},
			"fromRef":   map[string]any{"id": "refs/heads/feature", "displayId": "feature", "latestCommit": HeadSHA, "repository": payloadRepository(9, "CONTRIBUTOR")},
			"toRef":     map[string]any{"id": "refs/heads/main", "displayId": "main", "latestCommit": BeforeSHA, "repository": repo},
			"reviewers": []any{participant}, "participants": []any{},
		}
		if event == "pr:from_ref_updated" {
			d["previousFromHash"] = BeforeSHA
		}
		if event == "pr:reviewer:approved" || event == "pr:reviewer:unapproved" {
			d["actor"], d["participant"] = reviewer, participant
		}
		if event == "pr:comment:added" {
			d["actor"] = reviewer
			d["comment"] = map[string]any{"id": 31, "author": reviewer, "text": "/spacelift preview"}
		}
	case "repo:modified":
		d["repository"] = repo
	case "diagnostics:ping":
		d = map[string]any{"test": true}
	default:
		return nil, ErrEvent
	}
	b, err := json.Marshal(d)
	if err != nil {
		panic(err) // fixture maps contain only JSON values
	}
	return b, nil
}

func payloadRepository(id int, project string) map[string]any {
	return map[string]any{
		"id":      id,
		"slug":    "infra",
		"name":    "infra",
		"project": map[string]any{"id": 70, "key": project},
		"links":   map[string]any{"self": []any{map[string]string{"href": "https://bitbucket.example.com/projects/" + project + "/repos/infra/browse"}}},
	}
}

func payloadUser(id int, name string) map[string]any {
	return map[string]any{"id": id, "name": name, "slug": name, "displayName": name, "emailAddress": name + "@example.com", "type": "NORMAL"}
}
