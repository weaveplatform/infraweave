package azuredevopsfake

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// WebhookRefUpdate is one branch/tag ref change in a single push delivery.
type WebhookRefUpdate struct{ Name, Before, Head string }

// WebhookPullRequest supplies documented payload facts. A fork has its own
// source repository, which can belong to the same project as the target.
type WebhookPullRequest struct {
	ID                                                    int
	SourceBranch, TargetBranch, Head, Status, MergeStatus string
	Draft, Fork                                           bool
	SourceProject, SourceRepository                       string
	Votes                                                 []int
	Labels                                                []string
}

// PushWebhook builds the documented git.push envelope, with one delivery event
// ID and as many ref updates as the sender received in the push.
func (f *Fake) PushWebhook(id, project, repository string, updates []WebhookRefUpdate) (WebhookDelivery, error) {
	refs := make([]map[string]string, 0, len(updates))
	for _, update := range updates {
		refs = append(refs, map[string]string{"name": update.Name, "oldObjectId": update.Before, "newObjectId": update.Head})
	}
	return encodeWebhookEnvelope(id, "git.push", map[string]any{
		"repository": f.webhookRepository(project, repository),
		"refUpdates": refs, "pushedBy": webhookIdentity("author"), "pushId": 14, "commits": []any{},
	})
}

// PullRequestWebhook builds created/updated/merge-attempted event shapes. Its
// merge status is a preview result, not proof that all branch policies passed.
func (f *Fake) PullRequestWebhook(id, event, project, repository string, in WebhookPullRequest) (WebhookDelivery, error) {
	return encodeWebhookEnvelope(id, event, f.webhookRequest(project, repository, in))
}

// CommentWebhook places the target repository inside resource.pullRequest,
// as Azure's documented comment envelope does, preserving the text verbatim.
func (f *Fake) CommentWebhook(id, project, repository string, in WebhookPullRequest, comment string) (WebhookDelivery, error) {
	return encodeWebhookEnvelope(id, "ms.vss-code.git-pullrequest-comment-event", map[string]any{
		"pullRequest": f.webhookRequest(project, repository, in),
		"comment":     map[string]any{"id": 2, "parentCommentId": 1, "content": comment, "author": webhookIdentity("reviewer"), "commentType": "text"},
	})
}

func (f *Fake) webhookRepository(projectName, name string) map[string]any {
	f.Repo(projectName, name)
	f.mu.Lock()
	defer f.mu.Unlock()
	project := f.projects[projectName]
	repository := project.repos[name]
	return map[string]any{
		"id":        repository.id,
		"name":      repository.name,
		"project":   map[string]string{"id": project.id, "name": project.name},
		"remoteUrl": "https://dev.azure.com/acme/" + projectName + "/_git/" + name,
	}
}

func (f *Fake) webhookRequest(project, repository string, in WebhookPullRequest) map[string]any {
	status := in.Status
	if status == "" {
		status = "active"
	}
	mergeStatus := in.MergeStatus
	if mergeStatus == "" {
		mergeStatus = "succeeded"
	}
	reviewers := make([]map[string]any, 0, len(in.Votes))
	for index, vote := range in.Votes {
		identity := webhookIdentity("reviewer" + strconv.Itoa(index+1))
		identity["vote"] = vote
		reviewers = append(reviewers, identity)
	}
	labels := make([]map[string]string, 0, len(in.Labels))
	for _, label := range in.Labels {
		labels = append(labels, map[string]string{"name": label})
	}
	request := map[string]any{
		"repository": f.webhookRepository(project, repository), "pullRequestId": in.ID,
		"sourceRefName": "refs/heads/" + in.SourceBranch, "targetRefName": "refs/heads/" + in.TargetBranch,
		"lastMergeSourceCommit": map[string]string{"commitId": in.Head}, "status": status, "mergeStatus": mergeStatus,
		"title": "Infrastructure change", "creationDate": "2026-01-01T00:00:00Z",
		"isDraft": in.Draft, "createdBy": webhookIdentity("author"), "reviewers": reviewers, "labels": labels,
	}
	if in.Fork {
		sourceProject, sourceRepository := in.SourceProject, in.SourceRepository
		if sourceProject == "" {
			sourceProject = project
		}
		if sourceRepository == "" {
			sourceRepository = repository + "-fork"
		}
		request["forkSource"] = map[string]any{
			"repository": f.webhookRepository(sourceProject, sourceRepository),
			"name":       "refs/heads/" + in.SourceBranch, "objectId": in.Head,
		}
	}
	return request
}

func webhookIdentity(username string) map[string]any {
	return map[string]any{"id": "11111111-2222-3333-4444-555555555555", "displayName": "Example " + username, "uniqueName": username + "@example.com"}
}

func encodeWebhookEnvelope(id, event string, resource any) (WebhookDelivery, error) {
	body, err := json.Marshal(map[string]any{"id": id, "eventType": event, "publisherId": "tfs", "resource": resource, "resourceVersion": "2.0"})
	if err != nil {
		return WebhookDelivery{}, fmt.Errorf("azuredevopsfake: encode webhook fixture: %w", err)
	}
	return WebhookDelivery{Body: body}, nil
}
