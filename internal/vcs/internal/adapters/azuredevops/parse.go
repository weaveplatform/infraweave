package azuredevops

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

const (
	webhookPush            = "git.push"
	webhookCreated         = "git.pullrequest.created"
	webhookUpdated         = "git.pullrequest.updated"
	webhookMergeAttempted  = "git.pullrequest.merged"
	webhookCommented       = "ms.vss-code.git-pullrequest-comment-event"
	invalidWebhookEnvelope = "azure-invalid-envelope"
)

var (
	_                 port.Webhooks = (*Provider)(nil)
	errWebhookPayload               = errors.New("azure devops: invalid webhook payload")
)

// Event uses the body's routing field, never a request header. An authenticated
// envelope that cannot name its event must be retained and fail durably, rather
// than being silently filtered. The private sentinel cannot be selected by a
// valid unsupported eventType, which always returns the unconsumed empty name.
func (*Provider) Event(_ http.Header, body []byte) string {
	var envelope struct {
		EventType json.RawMessage `json:"eventType"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return invalidWebhookEnvelope
	}
	var event string
	if json.Unmarshal(envelope.EventType, &event) != nil || event == "" {
		return invalidWebhookEnvelope
	}
	if knownWebhookEvent(event) {
		return event
	}
	return ""
}

func knownWebhookEvent(event string) bool {
	switch event {
	case webhookPush, webhookCreated, webhookUpdated, webhookMergeAttempted, webhookCommented:
		return true
	default:
		return false
	}
}

// Consumed includes only the documented filter and an unrouteable envelope's
// fixed marker. Verification always happens before this classification.
func (*Provider) Consumed(event string) bool {
	return knownWebhookEvent(event) || event == invalidWebhookEnvelope
}

// DeliveryID is the payload event ID; intake supplies a body hash when absent.
func (*Provider) DeliveryID(_ http.Header, body []byte) string {
	var envelope struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return ""
	}
	return envelope.ID
}

// Repository is the stable target repository ID. Comment events carry it inside
// resource.pullRequest, unlike pushes and other pull-request notifications.
func (*Provider) Repository(body []byte) string {
	var envelope struct {
		EventType string `json:"eventType"`
		Resource  *struct {
			Repository *struct {
				ID string `json:"id"`
			} `json:"repository"`
			PullRequest *struct {
				Repository *struct {
					ID string `json:"id"`
				} `json:"repository"`
			} `json:"pullRequest"`
		} `json:"resource"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Resource == nil {
		return ""
	}
	resource := envelope.Resource
	if envelope.EventType == webhookCommented {
		if resource.PullRequest != nil && resource.PullRequest.Repository != nil {
			return resource.PullRequest.Repository.ID
		}
		return ""
	}
	if resource.Repository != nil {
		return resource.Repository.ID
	}
	return ""
}

// Parse normalizes only payload facts; it never queries the provider or treats
// comment text as a command. Invalid consumed data produces one safe error.
func (p *Provider) Parse(headers http.Header, body []byte) ([]vcsapi.Event, error) {
	event := p.Event(headers, body)
	if event == invalidWebhookEnvelope {
		return nil, errWebhookPayload
	}
	if !p.Consumed(event) {
		return nil, nil
	}
	var envelope webhookEnvelope
	if json.Unmarshal(body, &envelope) != nil || envelope.Resource == nil {
		return nil, errWebhookPayload
	}
	if event == webhookPush {
		return parseWebhookPush(envelope.Resource)
	}
	if event == webhookCommented {
		return parseWebhookComment(envelope.Resource)
	}
	return parseWebhookRequest(event, envelope.Resource.webhookRequest)
}

func parseWebhookPush(resource *webhookResource) ([]vcsapi.Event, error) {
	repository, ok := resource.Repository.repository()
	if !ok || len(resource.RefUpdates) == 0 {
		return nil, errWebhookPayload
	}
	result := make([]vcsapi.Event, 0, len(resource.RefUpdates))
	for _, ref := range resource.RefUpdates {
		if ref.Name == "" {
			return nil, errWebhookPayload
		}
		kind, name := "push", ""
		if value, ok := strings.CutPrefix(ref.Name, "refs/heads/"); ok {
			name = value
		} else if value, ok := strings.CutPrefix(ref.Name, "refs/tags/"); ok {
			kind, name = "tag", value
		} else {
			continue
		}
		if name == "" || !webhookCommitID(ref.OldObjectID) || !webhookCommitID(ref.NewObjectID) {
			return nil, errWebhookPayload
		}
		event := vcsapi.Event{Type: kind, Repository: repository, Before: ref.OldObjectID, Head: ref.NewObjectID, Author: resource.PushedBy.login()}
		if kind == "tag" {
			event.Tag = name
		} else {
			event.Branch = name
		}
		result = append(result, event)
	}
	return result, nil
}

func parseWebhookRequest(event string, resource webhookRequest) ([]vcsapi.Event, error) {
	repository, ok := resource.Repository.repository()
	request, valid := resource.request()
	if !ok || !valid {
		return nil, errWebhookPayload
	}
	action := "edited"
	switch resource.Status {
	case "completed":
		action = "merged"
	case "abandoned":
		action = "closed"
	default:
		if event == webhookCreated {
			action = "opened"
		}
	}
	return []vcsapi.Event{{
		Type: "pull_request", Action: action, Repository: repository, Branch: request.HeadBranch,
		Head: resource.LastMergeSourceCommit.CommitID, Author: resource.CreatedBy.login(), PullRequest: &request,
	}}, nil
}

func parseWebhookComment(resource *webhookResource) ([]vcsapi.Event, error) {
	if resource.PullRequest == nil || resource.Comment == nil || resource.Comment.Content == nil {
		return nil, errWebhookPayload
	}
	pr := resource.PullRequest
	repository, ok := pr.Repository.repository()
	request, valid := pr.request()
	if !ok || !valid {
		return nil, errWebhookPayload
	}
	if resource.Repository != nil && resource.Repository.ID != repository.ID {
		return nil, errWebhookPayload
	}
	head, author := pr.LastMergeSourceCommit.CommitID, resource.Comment.Author.login()
	return []vcsapi.Event{{
		Type: "comment", Action: "commented", Repository: repository, Branch: request.HeadBranch,
		Head: head, Author: author, PullRequest: &request, Comment: &vcsapi.Comment{
			Body:   *resource.Comment.Content,
			Author: author, PullRequest: request.Number, Head: head, BaseBranch: request.BaseBranch, Fork: request.Fork,
		},
	}}, nil
}

func webhookCommitID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
