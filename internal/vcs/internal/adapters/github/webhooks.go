package github

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/go-github/v93/github"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var _ port.Webhooks = (*Provider)(nil)

// DeliveryID is GitHub's stable delivery GUID, reused by explicit redelivery.
func (*Provider) DeliveryID(h http.Header, _ []byte) string { return h.Get("X-GitHub-Delivery") }

// Event retains the provider name for consumed actions. Returning an empty
// name for other actions lets shared intake filter them before storage.
// Malformed consumed payloads remain durable for the processor to refuse.
func (p *Provider) Event(h http.Header, body []byte) string {
	kind := h.Get("X-GitHub-Event")
	if !p.Consumed(kind) {
		return kind
	}
	var d *webhook
	if err := json.Unmarshal(body, &d); err != nil || d == nil {
		return kind
	}
	if !d.routingValid(kind) {
		return kind
	}
	if !d.consumed(kind) {
		return ""
	}
	return kind
}

// Missing routing fields cannot establish that a delivery is unconsumed.
// Retain it for the durable processor's normal malformed-payload path.
func (d *webhook) routingValid(kind string) bool {
	if kind == "push" {
		return d.Ref != "" && d.Ref != "refs/heads/" && d.Ref != "refs/tags/"
	}
	if d.Action == "" {
		return false
	}
	return kind != "issue_comment" || d.Action != "created" || d.Issue != nil
}

// Consumed names the event families whose actions the adapter handles.
func (*Provider) Consumed(kind string) bool {
	switch kind {
	case "push", "pull_request", "pull_request_review", "issue_comment", "check_run", "repository",
		"installation", "installation_repositories", "installation_target", "organization":
		return true
	default:
		return false
	}
}

// Repository supplies the stable repository ordering identity. Installation
// lifecycle deliveries have no repository and use their installation ID.
func (*Provider) Repository(body []byte) string {
	var d struct {
		Repository   *webhookRepository   `json:"repository"`
		Installation *github.Installation `json:"installation"`
	}
	if json.Unmarshal(body, &d) != nil {
		return ""
	}
	if d.Repository != nil && d.Repository.ID > 0 {
		return strconv.FormatInt(d.Repository.ID, 10)
	}
	if id := d.Installation.GetID(); id > 0 {
		return strconv.FormatInt(id, 10)
	}
	return ""
}

func (d *webhook) consumed(kind string) bool {
	switch kind {
	case "push":
		return strings.HasPrefix(d.Ref, "refs/heads/") || strings.HasPrefix(d.Ref, "refs/tags/")
	case "pull_request":
		switch d.Action {
		case "opened", "reopened", "synchronize", "edited", "closed", "labeled", "unlabeled", "ready_for_review", "converted_to_draft":
			return true
		}
	case "pull_request_review":
		return d.Action == "submitted" || d.Action == "dismissed"
	case "issue_comment":
		return d.Action == "created" && d.Issue.GetPullRequestLinks() != nil
	case "check_run":
		return d.Action == "requested_action"
	case "repository", "installation_target", "organization":
		return d.Action == "renamed"
	case "installation":
		switch d.Action {
		case "created", "deleted", "suspend", "unsuspend", "new_permissions_accepted":
			return true
		}
	case "installation_repositories":
		return d.Action == "added" || d.Action == "removed"
	}
	return false
}
