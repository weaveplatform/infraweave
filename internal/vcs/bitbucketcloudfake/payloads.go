package bitbucketcloudfake

import (
	"encoding/json"
	"errors"
)

// Stable identities and full commit hashes used by the documented fixtures.
const (
	RepositoryUUID = "{00000000-0000-4000-8000-000000000007}"
	BeforeSHA      = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	AfterSHA       = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	HeadSHA        = "cccccccccccccccccccccccccccccccccccccccc"
)

// ErrEvent is a fixture family the builder does not describe.
var ErrEvent = errors.New("bitbucketcloudfake: unknown payload event")

// Payload builds the documented webhook shapes with synthetic identities and
// example.com e-mail addresses. It does not mutate the fake's repositories.
// https://support.atlassian.com/bitbucket-cloud/docs/event-payloads/
func Payload(event string) ([]byte, error) {
	repo := payloadRepository(RepositoryUUID, "acme")
	author := payloadUser("{00000000-0000-4000-8000-000000000011}", "author")
	reviewer := payloadUser("{00000000-0000-4000-8000-000000000022}", "reviewer")
	d := map[string]any{"actor": author, "repository": repo}
	switch event {
	case "repo:push":
		d["push"] = map[string]any{"changes": []any{map[string]any{
			"old": payloadRef("branch", "main", BeforeSHA), "new": payloadRef("branch", "main", AfterSHA),
			"created": false, "closed": false, "forced": true,
		}}}
	case "pullrequest:created",
		"pullrequest:updated",
		"pullrequest:approved",
		"pullrequest:unapproved",
		"pullrequest:fulfilled",
		"pullrequest:rejected",
		"pullrequest:comment_created":
		approved := event == "pullrequest:approved" || event == "pullrequest:updated" || event == "pullrequest:fulfilled"
		state := "OPEN"
		if event == "pullrequest:fulfilled" {
			state = "MERGED"
		}
		if event == "pullrequest:rejected" {
			state = "DECLINED"
		}
		d["pullrequest"] = map[string]any{
			"id":         12,
			"title":      "Infrastructure change",
			"created_on": "2026-01-01T00:00:00Z", "updated_on": "2026-01-01T00:01:00Z",
			"links":  map[string]any{"html": map[string]string{"href": "https://bitbucket.org/acme/infra/pull-requests/12"}},
			"state":  state,
			"draft":  false,
			"author": author,
			"source": map[string]any{
				"branch":     map[string]string{"name": "feature"},
				"commit":     map[string]string{"hash": HeadSHA},
				"repository": payloadRepository("{00000000-0000-4000-8000-000000000009}", "contributor"),
			},
			"destination":  map[string]any{"branch": map[string]string{"name": "main"}, "commit": map[string]string{"hash": BeforeSHA}, "repository": repo},
			"participants": []any{map[string]any{"user": reviewer, "role": "PARTICIPANT", "approved": approved}},
		}
		if event == "pullrequest:approved" || event == "pullrequest:unapproved" {
			d["actor"], d["approval"] = reviewer, map[string]any{"date": "2026-01-01T00:00:00Z", "user": reviewer}
		}
		if event == "pullrequest:comment_created" {
			d["actor"] = reviewer
			d["comment"] = map[string]any{"id": 31, "user": reviewer, "content": map[string]string{"raw": "/spacelift preview", "markup": "markdown"}}
		}
	case "repo:fork":
		d["fork"] = payloadRepository("{00000000-0000-4000-8000-000000000009}", "contributor")
	default:
		return nil, ErrEvent
	}
	b, err := json.Marshal(d)
	if err != nil {
		panic(err) // fixture maps contain only JSON values
	}
	return b, nil
}

func payloadRepository(id, workspace string) map[string]any {
	return map[string]any{
		"uuid": id, "name": "infra", "slug": "infra", "full_name": workspace + "/infra",
		"links":     map[string]any{"html": map[string]string{"href": "https://bitbucket.org/" + workspace + "/infra"}},
		"workspace": map[string]string{"slug": workspace, "name": workspace},
	}
}

func payloadUser(id, nickname string) map[string]any {
	return map[string]any{"type": "user", "uuid": id, "nickname": nickname, "display_name": nickname, "email": nickname + "@example.com"}
}

func payloadRef(kind, name, hash string) map[string]any {
	return map[string]any{"type": kind, "name": name, "target": map[string]any{"type": "commit", "hash": hash}}
}
