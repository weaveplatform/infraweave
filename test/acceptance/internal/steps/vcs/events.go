package vcs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/azuredevopsfake"
	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketcloudfake"
	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketdcfake"
	"github.com/weaveplatform/infraweave/internal/vcs/githubfake"
	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

type eventSteps struct {
	comments   *commentSteps
	deliveries map[string]string
}

func (s *eventSteps) register(sc *godog.ScenarioContext) {
	s.deliveries = map[string]string{}
	s.registerRecovery(sc)
	sc.Step(`^the event repository commits "(\w+)" on "([^"]+)"$`, s.commit)
	sc.Step(`^the event fixture pushes "(\w+)" from "(\w+)" to "(\w+)" on "([^"]+)" with "(valid|wrong)" verification through ([AB]) and receives (\d+)$`, s.push)
	sc.Step(`^event delivery "(\w+)" settles with (\d+) new runs$`, s.settled)
	sc.Step(`^event delivery "(\w+)" has exactly (\d+) stored rows$`, s.rows)
}

func (s *eventSteps) repository() checkRepository {
	c := s.comments
	switch c.fixture.provider {
	case "GitHub":
		return c.github.fake.Repo(c.fixture.namespace, "infra")
	case "GitLab":
		return c.gitlab.fake.Repo(c.fixture.namespace, "infra")
	default:
		return c.peers.repo
	}
}

func (s *eventSteps) commit(key, branch string) {
	s.comments.w.IDs[key] = s.repository().Commit(branch, key, deploymentFiles(key))
}

func (s *eventSteps) push(ctx context.Context, name, before, head, branch, verification, replica string, want int) error {
	c := s.comments
	id := s.deliveries[name]
	if id == "" {
		id = kernel.NewULID().UUID()
		s.deliveries[name] = id
	}
	base, target := c.w.IDs[before], c.w.IDs[head]
	if base == "" || target == "" {
		return fmt.Errorf("%w: missing event commit", harness.ErrStep)
	}
	status, err := s.send(ctx, id, base, target, branch, verification, replica)
	if err != nil {
		return err
	}
	if status != want {
		return fmt.Errorf("%w: event delivery answered %d, want %d", harness.ErrStep, status, want)
	}
	return nil
}

func (s *eventSteps) send(ctx context.Context, id, before, head, branch, verification, replica string) (int, error) {
	c := s.comments
	client := &http.Client{Timeout: 15 * time.Second}
	endpoint := c.w.Base(replica) + "/webhooks/vcs/"
	secret := c.secret.Reveal()
	if verification == "wrong" {
		secret = []byte("invalid-event-verification")
	}
	switch c.fixture.provider {
	case "GitHub":
		raw, err := githubfake.Payload("push", "")
		if err != nil {
			return 0, fmt.Errorf("event push fixture: %w", err)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			return 0, fmt.Errorf("event push JSON: %w", err)
		}
		repository, err := c.github.fake.RepositoryPayload(c.fixture.namespace, "infra")
		if err != nil {
			return 0, fmt.Errorf("event repository: %w", err)
		}
		body["repository"], body["installation"] = repository, map[string]any{"id": c.installation, "app_id": c.appID}
		body["ref"], body["before"], body["after"] = "refs/heads/"+branch, before, head
		raw, err = json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("event push encode: %w", err)
		}
		sender := c.sender
		if verification == "wrong" {
			app := c.github.fake.AddApp("wrong-signature", "acme")
			sender, err = c.github.fake.Sender(app.ID)
			if err != nil {
				return 0, fmt.Errorf("event invalid sender: %w", err)
			}
		}
		return sender.Send(ctx, endpoint+"github_enterprise/"+c.fixture.integration, id, "push", raw)
	case "GitLab":
		delivery, err := c.gitlab.fake.PushWebhook(c.fixture.namespace, "infra", branch, before, head)
		if err != nil {
			return 0, fmt.Errorf("event GitLab push: %w", err)
		}
		delivery.ID = id
		return gitlabfake.NewWebhookSender(client).Send(ctx, endpoint+"gitlab/"+c.fixture.integration, secret, delivery)
	case "Azure DevOps":
		delivery, err := c.peers.azure.fake.PushWebhook(
			id,
			c.fixture.namespace,
			"infra",
			[]azuredevopsfake.WebhookRefUpdate{{Name: "refs/heads/" + branch, Before: before, Head: head}},
		)
		if err != nil {
			return 0, fmt.Errorf("event Azure push: %w", err)
		}
		return azuredevopsfake.NewWebhookSender(azuredevopsfake.WebhookSenderOptions{Client: client}).
			Send(ctx, endpoint+"azure_devops/"+c.fixture.integration, "", secret, delivery)
	default:
		return s.sendBitbucket(ctx, client, endpoint, id, before, head, branch, secret)
	}
}

func (s *eventSteps) sendBitbucket(ctx context.Context, client *http.Client, endpoint, id, before, head, branch string, secret []byte) (int, error) {
	c := s.comments
	var raw []byte
	var err error
	var sender bitbucketWebhookSender
	event, provider := "repo:push", "bitbucket_cloud"
	if c.fixture.provider == "Bitbucket Cloud" {
		raw, err = bitbucketcloudfake.Payload(event)
		sender = bitbucketcloudfake.NewSender(secret, client)
	} else {
		event, provider = "repo:refs_changed", "bitbucket_datacenter"
		raw, err = bitbucketdcfake.Payload(event)
		sender = bitbucketdcfake.NewSender(secret, client)
	}
	if err != nil {
		return 0, fmt.Errorf("event Bitbucket push: %w", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return 0, fmt.Errorf("event Bitbucket JSON: %w", err)
	}
	if provider == "bitbucket_cloud" {
		body["repository"] = c.peers.cloud.fake.RepositoryPayload(c.fixture.namespace, "infra")
		ref := func(sha string) any {
			return map[string]any{"type": "branch", "name": branch, "target": map[string]string{"type": "commit", "hash": sha}}
		}
		body["push"] = map[string]any{"changes": []any{map[string]any{"old": ref(before), "new": ref(head), "created": false, "closed": false, "forced": false}}}
	} else {
		body["repository"] = c.peers.dc.fake.RepositoryPayload(c.fixture.namespace, "infra")
		body["changes"] = []any{
			map[string]any{
				"ref":      map[string]string{"id": "refs/heads/" + branch, "displayId": branch, "type": "BRANCH"},
				"refId":    "refs/heads/" + branch,
				"fromHash": before,
				"toHash":   head,
				"type":     "UPDATE",
			},
		}
	}
	raw, err = json.Marshal(body)
	if err != nil {
		return 0, fmt.Errorf("event Bitbucket encode: %w", err)
	}
	return sender.Send(ctx, endpoint+provider+"/"+c.fixture.integration, id, event, raw)
}

func (s *eventSteps) rows(ctx context.Context, name string, want int) error {
	c := s.comments
	var count int
	err := c.w.Row(ctx, `SELECT count(*) FROM vcs_deliveries WHERE integration_id=$1 AND delivery_id=$2`, []any{harness.UUIDOf(c.fixture.integration), s.deliveries[name]}, &count)
	if err != nil {
		return err
	}
	if count != want {
		return fmt.Errorf("%w: event has %d rows, want %d", harness.ErrStep, count, want)
	}
	return nil
}

func (s *eventSteps) settled(ctx context.Context, name string, want int) error {
	c := s.comments
	return deps.Eventually(ctx, "event dispatched and stack evaluations complete", func(ctx context.Context) (bool, error) {
		var done bool
		var runs int
		var delivery kernel.ULID
		err := c.w.Row(
			ctx,
			`SELECT state='dispatched',runs_created,id FROM vcs_deliveries WHERE integration_id=$1 AND delivery_id=$2`,
			[]any{harness.UUIDOf(c.fixture.integration), s.deliveries[name]},
			&done,
			&runs,
			&delivery,
		)
		if err != nil {
			return false, err
		}
		if !done {
			return false, nil
		}
		var pending int
		if err := c.w.Row(
			ctx,
			`SELECT count(*) FROM jobs WHERE kind='vcs.evaluate_event' AND payload->>'delivery'=$1 AND state<>'succeeded'`,
			[]any{delivery.String()},
			&pending,
		); err != nil {
			return false, err
		}
		if pending != 0 {
			return false, nil
		}
		if runs != want {
			return false, fmt.Errorf("%w: event created %d runs, want %d", harness.ErrStep, runs, want)
		}
		return true, nil
	})
}
