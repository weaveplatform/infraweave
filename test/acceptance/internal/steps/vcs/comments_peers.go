package vcs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/weaveplatform/infraweave/internal/vcs/azuredevopsfake"
	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketcloudfake"
	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketdcfake"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func (c *commentSteps) seedPeer(provider string) (string, map[string]any, error) {
	if err := c.peers.seed(provider); err != nil {
		return "", nil, err
	}
	c.fixture.namespace = c.peers.namespace
	repo := c.peers.repo
	c.w.IDs["commentBase"] = repo.Commit("main", "comment base", deploymentFiles("base"))
	c.w.IDs["seed"] = repo.Commit("main", "comment seed", deploymentFiles("one"))
	for _, branch := range []string{"feature", "other", "target-12", "target-13", "target-14", "target-15"} {
		head := c.w.IDs["commentBase"]
		if branch == "feature" || branch == "other" {
			head = c.w.IDs["seed"]
		}
		repo.Branch(branch, head)
	}
	c.w.IDs["commentMoved"] = repo.Commit("other", "different request head", deploymentFiles("two"))
	c.peers.body["customInput"] = map[string]any{"name": "Plan comments", "spaceID": "acme", "isDefault": true}
	return c.peers.path, c.peers.body, nil
}

func (c *commentSteps) sendPeerRequest(ctx context.Context, delivery string, id int, action, branch, commit string, created time.Time, replica string) (int, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	endpoint := c.w.Base(replica) + "/webhooks/vcs/"
	target := "target-" + strconv.Itoa(id)
	if c.fixture.provider == "Azure DevOps" {
		event, state := "git.pullrequest.created", "active"
		if action != "open" {
			event = "git.pullrequest.updated"
		}
		if action == "close" {
			state = "abandoned"
		}
		iteration := azuredevopsfake.CheckIteration{ID: 1}
		if action == "move" {
			iteration.ID = 2
		}
		iteration.Source.SHA = c.w.IDs[commit]
		c.peers.azure.fake.SetCheckIterations(c.fixture.namespace, "infra", id, []azuredevopsfake.CheckIteration{iteration})
		payload, err := c.peers.azure.fake.PullRequestWebhook(
			delivery,
			event,
			c.fixture.namespace,
			"infra",
			azuredevopsfake.WebhookPullRequest{
				ID:               id,
				SourceBranch:     branch,
				TargetBranch:     target,
				Head:             c.w.IDs[commit],
				Status:           state,
				Fork:             c.sourceNamespace != "",
				SourceProject:    c.sourceNamespace,
				SourceRepository: c.sourceRepository,
			},
		)
		if err != nil {
			return 0, fmt.Errorf("comment Azure request: %w", err)
		}
		sender := azuredevopsfake.NewWebhookSender(azuredevopsfake.WebhookSenderOptions{Client: client})
		return sender.Send(ctx, endpoint+"azure_devops/"+c.fixture.integration, "", c.secret.Reveal(), payload)
	}
	var raw []byte
	var err error
	var sender bitbucketWebhookSender
	var event, provider, field string
	switch c.fixture.provider {
	case "Bitbucket Cloud":
		event, provider, field = "pullrequest:created", "bitbucket_cloud", "pullrequest"
		if action == "move" {
			event = "pullrequest:updated"
		}
		if action == "close" {
			event = "pullrequest:rejected"
		}
		raw, err = bitbucketcloudfake.Payload(event)
		sender = bitbucketcloudfake.NewSender(c.secret.Reveal(), client)
	case "Bitbucket DC":
		event, provider, field = "pr:opened", "bitbucket_datacenter", "pullRequest"
		if action == "move" {
			event = "pr:from_ref_updated"
		}
		if action == "close" {
			event = "pr:declined"
		}
		raw, err = bitbucketdcfake.Payload(event)
		sender = bitbucketdcfake.NewSender(c.secret.Reveal(), client)
	default:
		return 0, fmt.Errorf("%w: unknown comment provider", harness.ErrStep)
	}
	if err != nil {
		return 0, fmt.Errorf("comment Bitbucket payload: %w", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return 0, fmt.Errorf("comment payload decode: %w", err)
	}
	request, ok := body[field].(map[string]any)
	if !ok {
		return 0, fmt.Errorf("%w: missing native request", harness.ErrStep)
	}
	request["id"] = id
	if provider == "bitbucket_cloud" {
		body["repository"] = c.peers.cloud.fake.RepositoryPayload(c.fixture.namespace, "infra")
		source := body["repository"]
		if c.sourceNamespace != "" {
			source = c.peers.cloud.fake.RepositoryPayload(c.sourceNamespace, c.sourceRepository)
		}
		request["created_on"], request["updated_on"] = created.Format(time.RFC3339), c.clock.Format(time.RFC3339)
		request["source"] = map[string]any{"branch": map[string]string{"name": branch}, "commit": map[string]string{"hash": c.w.IDs[commit]}, "repository": source}
		request["destination"] = map[string]any{
			"branch":     map[string]string{"name": target},
			"commit":     map[string]string{"hash": c.w.IDs["commentBase"]},
			"repository": body["repository"],
		}
	} else {
		if _, ok := request["toRef"].(map[string]any); !ok {
			return 0, fmt.Errorf("%w: missing native target", harness.ErrStep)
		}
		repository := c.peers.dc.fake.RepositoryPayload(c.fixture.namespace, "infra")
		source := repository
		if c.sourceNamespace != "" {
			source = c.peers.dc.fake.RepositoryPayload(c.sourceNamespace, c.sourceRepository)
		}
		request["createdDate"], request["updatedDate"] = created.UnixMilli(), c.clock.UnixMilli()
		request["fromRef"] = map[string]any{"id": "refs/heads/" + branch, "displayId": branch, "latestCommit": c.w.IDs[commit], "repository": source}
		request["toRef"] = map[string]any{"id": "refs/heads/" + target, "displayId": target, "latestCommit": c.w.IDs["commentBase"], "repository": repository}
	}
	native, nativeErr := json.Marshal(request)
	if nativeErr != nil {
		return 0, fmt.Errorf("native Bitbucket request encode: %w", nativeErr)
	}
	if provider == "bitbucket_cloud" {
		err = c.peers.cloud.fake.SetPullRequest(c.fixture.namespace, "infra", id, native)
	} else {
		err = c.peers.dc.fake.SetPullRequest(c.fixture.namespace, "infra", id, native)
	}
	if err != nil {
		return 0, fmt.Errorf("native Bitbucket request fixture: %w", err)
	}
	raw, err = json.Marshal(body)
	if err != nil {
		return 0, fmt.Errorf("comment payload encode: %w", err)
	}
	return sender.Send(ctx, endpoint+provider+"/"+c.fixture.integration, delivery, event, raw)
}

func (c *commentSteps) losePeerComment() {
	switch c.fixture.provider {
	case "Bitbucket Cloud":
		c.peers.cloud.fake.FailNextCommentCreate()
	case "Bitbucket DC":
		c.peers.dc.fake.FailNextCommentCreate()
	case "Azure DevOps":
		c.peers.azure.fake.FailNextCommentCreate()
	}
}

func (c *commentSteps) peerComments(pr string) []nativePlanComment {
	var out []nativePlanComment
	switch c.fixture.provider {
	case "Bitbucket Cloud":
		for _, r := range c.peers.cloud.fake.Comments(c.fixture.namespace, "infra", pr) {
			out = append(out, nativePlanComment{ID: r.ID, ThreadID: r.ThreadID, Body: r.Body, Minimized: r.Minimized})
		}
	case "Bitbucket DC":
		for _, r := range c.peers.dc.fake.Comments(c.fixture.namespace, "infra", pr) {
			out = append(out, nativePlanComment{ID: r.ID, ThreadID: r.ThreadID, Body: r.Body, Minimized: r.Minimized})
		}
	case "Azure DevOps":
		for _, r := range c.peers.azure.fake.Comments(c.fixture.namespace, "infra", pr) {
			out = append(out, nativePlanComment{ID: r.ID, ThreadID: r.ThreadID, Body: r.Body, Minimized: r.Minimized})
		}
	}
	return out
}
