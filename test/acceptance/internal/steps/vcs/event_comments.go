package vcs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/azuredevopsfake"
	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketcloudfake"
	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketdcfake"
	"github.com/weaveplatform/infraweave/internal/vcs/githubfake"
	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func (s *eventSteps) ordinaryComment(ctx context.Context) error {
	c := s.comments
	var before, after int
	if err := c.w.Row(ctx, `SELECT count(*) FROM runs`, nil, &before); err != nil {
		return err
	}
	id := kernel.NewULID().UUID()
	s.deliveries["ordinaryComment"] = id
	marker := "ordinary review note " + id
	status, err := s.sendComment(ctx, id, marker)
	if err != nil {
		return err
	}
	if status != http.StatusAccepted {
		return fmt.Errorf("%w: ordinary comment answered %d", harness.ErrStep, status)
	}
	if err := s.settled(ctx, "ordinaryComment", 0); err != nil {
		return err
	}
	var stored bool
	if err := c.w.Row(
		ctx,
		`SELECT position(convert_to($3,'UTF8') IN body)>0 FROM vcs_deliveries WHERE integration_id=$1 AND delivery_id=$2`,
		[]any{harness.UUIDOf(c.fixture.integration), id, marker},
		&stored,
	); err != nil {
		return err
	}
	if err := c.w.Row(ctx, `SELECT count(*) FROM runs`, nil, &after); err != nil {
		return err
	}
	if !stored || before != after {
		return fmt.Errorf("%w: ordinary comment was not retained without creating a run", harness.ErrStep)
	}
	return nil
}

func (s *eventSteps) sendComment(ctx context.Context, id, marker string) (int, error) {
	c := s.comments
	client := &http.Client{Timeout: 15 * time.Second}
	endpoint := c.w.Base("A") + "/webhooks/vcs/"
	switch c.fixture.provider {
	case "GitLab":
		delivery, err := c.gitlab.fake.NoteWebhook(
			c.fixture.namespace,
			"infra",
			gitlabfake.WebhookMergeRequest{IID: 12, SourceBranch: "feature", TargetBranch: "target-12", Head: c.w.IDs["proposed"], Author: "creator", Actor: "reviewer"},
			marker,
		)
		if err != nil {
			return 0, fmt.Errorf("event GitLab comment: %w", err)
		}
		delivery.ID = id
		return gitlabfake.NewWebhookSender(client).Send(ctx, endpoint+"gitlab/"+c.fixture.integration, c.secret.Reveal(), delivery)
	case "Azure DevOps":
		delivery, err := c.peers.azure.fake.CommentWebhook(
			id,
			c.fixture.namespace,
			"infra",
			azuredevopsfake.WebhookPullRequest{ID: 12, SourceBranch: "feature", TargetBranch: "target-12", Head: c.w.IDs["proposed"]},
			marker,
		)
		if err != nil {
			return 0, fmt.Errorf("event Azure comment: %w", err)
		}
		return azuredevopsfake.NewWebhookSender(azuredevopsfake.WebhookSenderOptions{Client: client}).
			Send(ctx, endpoint+"azure_devops/"+c.fixture.integration, "", c.secret.Reveal(), delivery)
	default:
		return s.sendSignedComment(ctx, client, endpoint, id, marker)
	}
}

func (s *eventSteps) sendSignedComment(ctx context.Context, client *http.Client, endpoint, id, marker string) (int, error) {
	c := s.comments
	var raw []byte
	var err error
	var sender bitbucketWebhookSender
	event, provider := "issue_comment", "github_enterprise"
	switch c.fixture.provider {
	case "GitHub":
		raw, err = githubfake.Payload(event, "created")
		sender = c.sender
	case "Bitbucket Cloud":
		event, provider = "pullrequest:comment_created", "bitbucket_cloud"
		raw, err = bitbucketcloudfake.Payload(event)
		sender = bitbucketcloudfake.NewSender(c.secret.Reveal(), client)
	case "Bitbucket DC":
		event, provider = "pr:comment:added", "bitbucket_datacenter"
		raw, err = bitbucketdcfake.Payload(event)
		sender = bitbucketdcfake.NewSender(c.secret.Reveal(), client)
	}
	if err != nil {
		return 0, fmt.Errorf("event signed comment fixture: %w", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return 0, fmt.Errorf("event signed comment JSON: %w", err)
	}
	switch c.fixture.provider {
	case "GitHub":
		repository, err := c.github.fake.RepositoryPayload(c.fixture.namespace, "infra")
		if err != nil {
			return 0, fmt.Errorf("event comment repository: %w", err)
		}
		body["repository"], body["installation"] = repository, map[string]any{"id": c.installation, "app_id": c.appID}
		body["comment"] = map[string]any{"id": 31, "body": marker, "user": map[string]string{"login": "reviewer"}}
	case "Bitbucket Cloud":
		repository := c.peers.cloud.fake.RepositoryPayload(c.fixture.namespace, "infra")
		body["repository"] = repository
		request, ok := body["pullrequest"].(map[string]any)
		if !ok {
			return 0, fmt.Errorf("%w: missing native comment request", harness.ErrStep)
		}
		request["source"] = map[string]any{
			"branch":     map[string]string{"name": "feature"},
			"commit":     map[string]string{"hash": c.w.IDs["proposed"]},
			"repository": repository,
		}
		request["destination"] = map[string]any{
			"branch":     map[string]string{"name": "target-12"},
			"commit":     map[string]string{"hash": c.w.IDs["commentBase"]},
			"repository": repository,
		}
		body["comment"] = map[string]any{"id": 31, "user": body["actor"], "content": map[string]string{"raw": marker, "markup": "markdown"}}
	case "Bitbucket DC":
		request, ok := body["pullRequest"].(map[string]any)
		if !ok {
			return 0, fmt.Errorf("%w: missing native comment request", harness.ErrStep)
		}
		request["toRef"] = map[string]any{
			"id":           "refs/heads/target-12",
			"displayId":    "target-12",
			"latestCommit": c.w.IDs["commentBase"],
			"repository":   c.peers.dc.fake.RepositoryPayload(c.fixture.namespace, "infra"),
		}
		body["comment"] = map[string]any{"id": 31, "text": marker, "author": body["actor"]}
	}
	raw, err = json.Marshal(body)
	if err != nil {
		return 0, fmt.Errorf("event signed comment encode: %w", err)
	}
	return sender.Send(ctx, endpoint+provider+"/"+c.fixture.integration, id, event, raw)
}
