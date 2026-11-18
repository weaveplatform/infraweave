package gitlabfake

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const noteTypeField = "noteable_type" //nolint:misspell // GitLab's documented field name.

// WebhookMergeRequest supplies the documented merge-request fields. The source
// defaults to the target project; naming another source creates a fork fixture.
type WebhookMergeRequest struct {
	Author, Actor, Title, URL, HeadBranchURL, State string
	CreatedAt, UpdatedAt                            time.Time
	IID                                             int
	Action, SourceBranch, TargetBranch, Head        string
	SourceNamespace, SourceRepository, OldRev       string
	Draft                                           bool
	Labels, PreviousLabels                          []string
	MergeStatus, DetailedMergeStatus                string
	BlockingDiscussionsResolved                     *bool
}

// PushWebhook follows GitLab's documented push payload, including a stable
// target project ID and a canonical namespace path. Addresses use example.com.
func (f *Fake) PushWebhook(namespace, repository, branch, before, head string) (WebhookDelivery, error) {
	return f.refWebhook(namespace, repository, "push", "Push Hook", "refs/heads/"+branch, before, head)
}

// TagWebhook follows the documented tag-push shape, including tag deletion
// when head is the all-zero commit ID.
func (f *Fake) TagWebhook(namespace, repository, tag, before, head string) (WebhookDelivery, error) {
	return f.refWebhook(namespace, repository, "tag_push", "Tag Push Hook", "refs/tags/"+tag, before, head)
}

func (f *Fake) refWebhook(namespace, repository, kind, event, ref, before, head string) (WebhookDelivery, error) {
	payload := map[string]any{
		"object_kind": kind, "event_name": kind, "before": before, "after": head,
		"ref": ref, "checkout_sha": head, "user_name": "Example Author", "user_username": "author",
		"user_email": "author@example.com", "project": f.webhookProject(namespace, repository),
		"commits": []any{}, "total_commits_count": 0,
	}
	return encodeWebhook(event, payload)
}

// MergeRequestWebhook follows the documented action and label-change shape.
func (f *Fake) MergeRequestWebhook(namespace, repository string, in WebhookMergeRequest) (WebhookDelivery, error) {
	attributes := f.webhookRequest(namespace, repository, in)
	attributes["action"], attributes["oldrev"] = in.Action, in.OldRev
	changes := map[string]any{}
	if in.PreviousLabels != nil {
		changes["labels"] = map[string]any{"previous": webhookLabels(in.PreviousLabels), "current": webhookLabels(in.Labels)}
	}
	actor := in.Actor
	if actor == "" {
		actor = in.Author
	}
	if actor == "" {
		actor = "author"
	}
	return encodeWebhook("Merge Request Hook", map[string]any{
		"object_kind": "merge_request", "event_type": "merge_request", "user": f.requestUser(actor),
		"project": f.webhookProject(namespace, repository), "object_attributes": attributes,
		"labels": webhookLabels(in.Labels), "changes": changes,
	})
}

// NoteWebhook follows a merge request comment, preserving the note verbatim;
// no interpretation or command processing occurs in the fake or parser.
func (f *Fake) NoteWebhook(namespace, repository string, in WebhookMergeRequest, note string) (WebhookDelivery, error) {
	return encodeWebhook("Note Hook", map[string]any{
		"object_kind": "note", "user": webhookUser("reviewer"),
		"project":           f.webhookProject(namespace, repository),
		"object_attributes": map[string]any{"id": 1234, "action": "create", noteTypeField: "MergeRequest", "note": note},
		"merge_request":     f.webhookRequest(namespace, repository, in),
	})
}

func (f *Fake) webhookProject(namespace, repository string) map[string]any {
	p := f.projectOf(namespace, repository)
	return map[string]any{
		"id": p.id, "namespace": namespace, "path_with_namespace": namespace + "/" + repository,
		"web_url": "https://gitlab.example.com/" + namespace + "/" + repository,
	}
}

func (f *Fake) webhookRequest(namespace, repository string, in WebhookMergeRequest) map[string]any {
	sourceNamespace, sourceRepository := in.SourceNamespace, in.SourceRepository
	if sourceNamespace == "" {
		sourceNamespace = namespace
	}
	if sourceRepository == "" {
		sourceRepository = repository
	}
	source, target := f.projectOf(sourceNamespace, sourceRepository), f.projectOf(namespace, repository)
	author := in.Author
	if author == "" {
		author = "author"
	}
	creator := f.requestUser(author)
	created, updated := in.CreatedAt, in.UpdatedAt
	if created.IsZero() {
		created = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	if updated.IsZero() {
		updated = created
	}
	title := in.Title
	if title == "" {
		title = "Infrastructure change"
	}
	link := in.URL
	if link == "" {
		link = fmt.Sprintf("https://gitlab.example.com/%s/%s/-/merge_requests/%d", namespace, repository, in.IID)
	}
	state := in.State
	if state == "" {
		state = "opened"
		if in.Action == "close" {
			state = "closed"
		}
		if in.Action == "merge" {
			state = "merged"
		}
	}
	sourceProject := f.webhookProject(sourceNamespace, sourceRepository)
	if in.HeadBranchURL != "" {
		sourceProject["web_url"] = strings.Split(in.HeadBranchURL, "/-/tree/")[0]
	}
	return map[string]any{
		"author_id": creator["id"], "title": title, "url": link, "state": state, "created_at": created.Format(time.RFC3339Nano), "updated_at": updated.Format(time.RFC3339Nano),
		"id": 9001, "iid": in.IID, "source_branch": in.SourceBranch, "target_branch": in.TargetBranch,
		"source_project_id": source.id, "target_project_id": target.id,
		"source":      sourceProject,
		"last_commit": map[string]any{"id": in.Head, "author": webhookUser("author")},
		"draft":       in.Draft, "labels": webhookLabels(in.Labels), "merge_status": in.MergeStatus,
		"detailed_merge_status": in.DetailedMergeStatus, "blocking_discussions_resolved": in.BlockingDiscussionsResolved,
	}
}

func webhookLabels(labels []string) []map[string]string {
	result := make([]map[string]string, 0, len(labels))
	for _, label := range labels {
		result = append(result, map[string]string{"title": label})
	}
	return result
}

func webhookUser(username string) map[string]string {
	return map[string]string{"username": username, "name": "Example Author", "email": username + "@example.com"}
}

func encodeWebhook(event string, payload any) (WebhookDelivery, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return WebhookDelivery{}, fmt.Errorf("gitlabfake: encode webhook fixture: %w", err)
	}
	return WebhookDelivery{Event: event, Body: body}, nil
}
