package gitlab

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var (
	_                 port.Webhooks = (*Provider)(nil)
	errWebhookPayload               = errors.New("gitlab: invalid webhook payload")
)

// Event retains GitLab's event name for consumed or malformed routing data.
// Only a well-typed, explicit unsupported action/note kind is filtered here;
// malformed consumed payloads must be retained for the durable parser to fail.
func (*Provider) Event(headers http.Header, body []byte) string {
	event := headers.Get("X-Gitlab-Event")
	if event != "Merge Request Hook" && event != "Note Hook" {
		return event
	}
	var hint webhookRouting
	if json.Unmarshal(body, &hint) != nil || hint.Attributes == nil {
		return event
	}
	if event == "Merge Request Hook" {
		action := hint.Attributes.Action
		if action != "" && !mergeAction(action) {
			return ""
		}
	} else if kind := hint.Attributes.NoteableType; kind != "" && kind != "MergeRequest" {
		return ""
	}
	return event
}

// Consumed is the GitLab filter agreed for push-policy inputs.
func (*Provider) Consumed(event string) bool {
	switch event {
	case "Push Hook", "Tag Push Hook", "Merge Request Hook", "Note Hook":
		return true
	default:
		return false
	}
}

// Repository is GitLab's stable target project ID. Invalid payloads can have no
// ordering identity; the durable parser still records their failure.
func (*Provider) Repository(body []byte) string {
	var payload struct {
		Project struct {
			ID int64 `json:"id"`
		} `json:"project"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.Project.ID <= 0 {
		return ""
	}
	return strconv.FormatInt(payload.Project.ID, 10)
}

// Parse normalizes consumed payloads using retained metadata only, without
// querying GitLab or interpreting note bodies as commands.
func (p *Provider) Parse(headers http.Header, body []byte) ([]vcsapi.Event, error) {
	event := p.Event(headers, body)
	if !p.Consumed(event) {
		return nil, nil
	}
	var payload webhookPayload
	if json.Unmarshal(body, &payload) != nil {
		return nil, errWebhookPayload
	}
	repository, ok := payload.Project.repository()
	if !ok {
		return nil, errWebhookPayload
	}
	base := vcsapi.Event{Repository: repository, Author: payload.User.Username}
	switch event {
	case "Push Hook", "Tag Push Hook":
		return pushPayload(payload, base, event == "Tag Push Hook")
	case "Merge Request Hook":
		return mergePayload(payload, base)
	case "Note Hook":
		return notePayload(payload, base)
	default:
		return nil, errWebhookPayload
	}
}

func pushPayload(payload webhookPayload, event vcsapi.Event, tag bool) ([]vcsapi.Event, error) {
	prefix, kind := "refs/heads/", "push"
	if tag {
		prefix, kind = "refs/tags/", "tag_push"
	}
	ref, ok := strings.CutPrefix(payload.Ref, prefix)
	if payload.ObjectKind != kind || !ok || ref == "" || !commitID(payload.Before) || !commitID(payload.After) {
		return nil, errWebhookPayload
	}
	event.Type, event.Before, event.Head = "push", payload.Before, payload.After
	event.Author = payload.UserUsername
	if event.Author == "" {
		event.Author = payload.UserName
	}
	if tag {
		event.Type, event.Tag = "tag", ref
	} else {
		event.Branch = ref
	}
	return []vcsapi.Event{event}, nil
}

func mergePayload(payload webhookPayload, event vcsapi.Event) ([]vcsapi.Event, error) {
	attributes := payload.Attributes
	if payload.ObjectKind != "merge_request" || attributes == nil || !mergeAction(attributes.Action) {
		return nil, errWebhookPayload
	}
	if attributes.OldRev != "" && !commitID(attributes.OldRev) {
		return nil, errWebhookPayload
	}
	request, ok := attributes.request(payload.Project, attributes.Action)
	if !ok {
		return nil, errWebhookPayload
	}
	if attributes.Labels == nil {
		request.Labels = labelNames(payload.Labels)
	}
	event.Type, event.PullRequest = "pull_request", &request
	if attributes.AuthorID > 0 && payload.User.ID == attributes.AuthorID {
		request.AuthorLogin = payload.User.Username
	}
	event.Head, event.Branch = attributes.LastCommit.ID, attributes.SourceBranch
	event.Before = attributes.OldRev
	actions := requestActions(attributes.Action, attributes.OldRev, payload.Changes.Labels)
	result := make([]vcsapi.Event, 0, len(actions))
	for _, action := range actions {
		copy := event
		requestCopy := request
		requestCopy.Labels = slices.Clone(request.Labels)
		copy.PullRequest = &requestCopy
		copy.Action = action
		result = append(result, copy)
	}
	return result, nil
}

func notePayload(payload webhookPayload, event vcsapi.Event) ([]vcsapi.Event, error) {
	a := payload.Attributes
	if payload.ObjectKind != "note" || a == nil || a.NoteableType != "MergeRequest" || a.Note == nil || payload.MergeRequest == nil {
		return nil, errWebhookPayload
	}
	request, ok := payload.MergeRequest.request(payload.Project, "")
	if !ok {
		return nil, errWebhookPayload
	}
	event.Type, event.Action, event.PullRequest = "comment", "commented", &request
	event.Head, event.Branch = payload.MergeRequest.LastCommit.ID, request.HeadBranch
	event.Comment = &vcsapi.Comment{
		Body: *a.Note, Author: payload.User.Username, PullRequest: request.Number,
		Head: event.Head, BaseBranch: request.BaseBranch, Fork: request.Fork,
	}
	return []vcsapi.Event{event}, nil
}

func mergeAction(action string) bool {
	switch action {
	case "open", "reopen", "update", "close", "merge", "approved", "unapproved":
		return true
	default:
		return false
	}
}

func requestActions(action, oldrev string, labels *webhookLabelChange) []string {
	switch action {
	case "open":
		return []string{"opened"}
	case "reopen":
		return []string{"reopened"}
	case "close":
		return []string{"closed"}
	case "merge":
		return []string{"merged"}
	case "update":
		if oldrev != "" {
			return []string{"synchronize"}
		}
		if actions := labelActions(labels); len(actions) != 0 {
			return actions
		}
	}
	// Approval notifications change policy facts, while the policy action
	// vocabulary records their re-evaluation as an edit.
	return []string{"edited"}
}

func labelActions(change *webhookLabelChange) []string {
	if change == nil {
		return nil
	}
	previous, current := labelNames(change.Previous), labelNames(change.Current)
	var actions []string
	if slices.ContainsFunc(current, func(label string) bool { return !slices.Contains(previous, label) }) {
		actions = append(actions, "labeled")
	}
	if slices.ContainsFunc(previous, func(label string) bool { return !slices.Contains(current, label) }) {
		actions = append(actions, "unlabeled")
	}
	return actions
}

func labelNames(labels []webhookLabel) []string {
	result := make([]string, 0, len(labels))
	for _, label := range labels {
		if label.Title != "" && !slices.Contains(result, label.Title) {
			result = append(result, label.Title)
		}
	}
	return result
}

func commitID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
