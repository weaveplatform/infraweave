package vcs

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/githubfake"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

type githubWebhookDelivery struct {
	event, signature string
	body             []byte
}

type githubWebhookSteps struct {
	w          *harness.World
	github     *githubSteps
	senders    map[string]*githubfake.Sender
	deliveries map[string]githubWebhookDelivery
}

func (g *githubWebhookSteps) register(sc *godog.ScenarioContext) {
	g.senders = make(map[string]*githubfake.Sender)
	g.deliveries = make(map[string]githubWebhookDelivery)
	sc.Step(`^the GitHub integration "(\w+)" uses the stored API host "([^"]+)"$`, g.apiHost)
	sc.Step(`^the GitHub App "(\w+)" sends "([a-z_]+)" action "([a-z_]*)" as delivery "([^"]+)" to integration "(\w+)" through ([AB])$`, g.send)
	sc.Step(`^the GitHub App "(\w+)" redelivers its first delivery$`, g.redeliver)
	sc.Step(`^the GitHub App "(\w+)" recorded only the attempt statuses "([0-9, ]+)"$`, g.statuses)
	sc.Step(`^GitHub delivery "([^"]+)" belongs to integration "(\w+)" with its exact signed bytes and one processing job$`, g.stored)
	sc.Step(`^GitHub delivery "([^"]+)" is eventually dispatched$`, g.dispatched)
	sc.Step(`^no stored webhook row or server log contains the GitHub App secrets "([^"]+)"$`, g.noSecrets)
}

// apiHost isolates registration's provider metadata lookup on the fake.
// Delivery then exercises the actual stored public or Enterprise host
// without any production transport override or a request to live GitHub.
func (g *githubWebhookSteps) apiHost(ctx context.Context, integration, host string) error {
	host = g.w.Resolve(host)
	var stored string
	if err := g.w.Row(ctx, `UPDATE vcs_integrations SET
settings=jsonb_set(settings,'{apiHost}',to_jsonb($3::text),true),version=version+1
WHERE account_id=$1 AND id=$2 RETURNING settings->>'apiHost'`,
		[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(g.w.IDs[integration]), host}, &stored); err != nil {
		return err
	}
	if stored != host {
		return fmt.Errorf("%w: integration has the wrong stored API host", harness.ErrStep)
	}
	return nil
}

func (g *githubWebhookSteps) sender(key string) (*githubfake.Sender, error) {
	if s := g.senders[key]; s != nil {
		return s, nil
	}
	id, err := strconv.ParseInt(g.w.IDs[key+"ID"], 10, 64)
	if err != nil || g.github.fake == nil {
		return nil, fmt.Errorf("%w: no GitHub App kept as %s", harness.ErrStep, key)
	}
	s, err := g.github.fake.Sender(id)
	if err != nil {
		return nil, fmt.Errorf("GitHub sender: %w", err)
	}
	g.senders[key] = s
	return s, nil
}

// send uses the provider fake's actual one-attempt sender, without an SDK
// credential. Repeated delivery IDs retain the original fixture bytes.
func (g *githubWebhookSteps) send(ctx context.Context, app, event, action, delivery, integration, replica string) error {
	s, err := g.sender(app)
	if err != nil {
		return err
	}
	d, exists := g.deliveries[delivery]
	if !exists {
		body, err := githubfake.Payload(event, action)
		if err != nil {
			return fmt.Errorf("GitHub payload: %w", err)
		}
		secret := g.w.IDs[app+"WebhookSecret"]
		if secret == "" {
			return fmt.Errorf("%w: no webhook secret kept for App %s", harness.ErrStep, app)
		}
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write(body)
		d = githubWebhookDelivery{event: event, signature: "sha256=" + hex.EncodeToString(mac.Sum(nil)), body: body}
		g.deliveries[delivery] = d
	}
	id := g.w.IDs[integration]
	if id == "" || d.event != event {
		return fmt.Errorf("%w: missing integration or changed event for delivery %s", harness.ErrStep, delivery)
	}
	status, err := s.Send(ctx, g.w.Base(replica)+"/webhooks/vcs/github_enterprise/"+id, delivery, event, d.body)
	g.w.Sent = append(g.w.Sent, harness.Response{Status: status})
	if err != nil {
		return fmt.Errorf("GitHub delivery: %w", err)
	}
	return nil
}

func (g *githubWebhookSteps) redeliver(ctx context.Context, app string) error {
	s, err := g.sender(app)
	if err != nil {
		return err
	}
	status, err := s.Redeliver(ctx, 0)
	g.w.Sent = append(g.w.Sent, harness.Response{Status: status})
	if err != nil {
		return fmt.Errorf("GitHub redelivery: %w", err)
	}
	return nil
}

func (g *githubWebhookSteps) statuses(app, csv string) error {
	s, err := g.sender(app)
	if err != nil {
		return err
	}
	parts, attempts := strings.Split(csv, ", "), s.Attempts()
	if len(parts) != len(attempts) {
		return fmt.Errorf("%w: %d GitHub attempts, want %d", harness.ErrStep, len(attempts), len(parts))
	}
	for i, part := range parts {
		want, err := strconv.Atoi(part)
		if err != nil || want != attempts[i].Status {
			return fmt.Errorf("%w: unexpected GitHub attempt status at %d", harness.ErrStep, i+1)
		}
	}
	return nil
}

// stored reads the actual delivery and its account-bound unique job. A
// current or completed job is counted, so fast background work is valid.
func (g *githubWebhookSteps) stored(ctx context.Context, delivery, integration string) error {
	want, exists := g.deliveries[delivery]
	if !exists {
		return fmt.Errorf("%w: no sent GitHub delivery %s", harness.ErrStep, delivery)
	}
	var id, bound kernel.ULID
	var provider, event, repository string
	var signature, headers, body []byte
	account := harness.UUIDOf(harness.Home.Account)
	if err := g.w.Row(ctx, `SELECT id,integration_id,provider,event,signature,headers,body,repository
FROM vcs_deliveries WHERE account_id=$1 AND delivery_id=$2`, []any{account, delivery},
		&id, &bound, &provider, &event, &signature, &headers, &body, &repository); err != nil {
		return err
	}
	var safe http.Header
	if bound.String() != g.w.IDs[integration] || provider != "GITHUB_ENTERPRISE" || event != want.event ||
		string(signature) != want.signature || !bytes.Equal(body, want.body) || repository != "7" ||
		json.Unmarshal(headers, &safe) != nil || safe.Get("X-Hub-Signature-256") != want.signature ||
		safe.Get("X-GitHub-Delivery") != delivery || safe.Get("X-GitHub-Event") != want.event ||
		safe.Get("Authorization") != "" {
		return fmt.Errorf("%w: GitHub delivery lost its account, provider, signature, body or safe headers", harness.ErrStep)
	}
	var rows, jobs int
	if err := g.w.Row(ctx, `SELECT
 (SELECT count(*) FROM vcs_deliveries WHERE account_id=$1 AND delivery_id=$2),
 (SELECT count(*) FROM jobs WHERE account_id=$1 AND kind='vcs.process_delivery' AND unique_key=$3)`,
		[]any{account, delivery, id.String()}, &rows, &jobs); err != nil {
		return err
	}
	if rows != 1 || jobs != 1 {
		return fmt.Errorf("%w: delivery rows/jobs = %d/%d, want 1/1", harness.ErrStep, rows, jobs)
	}
	return nil
}

func (g *githubWebhookSteps) dispatched(ctx context.Context, delivery string) error {
	return deps.Eventually(ctx, "GitHub delivery to be parsed and dispatched", func(ctx context.Context) (bool, error) {
		var state string
		err := g.w.Row(ctx, `SELECT state FROM vcs_deliveries WHERE account_id=$1 AND delivery_id=$2`,
			[]any{harness.UUIDOf(harness.Home.Account), delivery}, &state)
		return state == "dispatched", err
	}) //nolint:wrapcheck // the harness names the awaited state
}

func (g *githubWebhookSteps) noSecrets(ctx context.Context, apps string) error {
	for _, app := range strings.Split(apps, ", ") {
		secret := g.w.IDs[app+"WebhookSecret"]
		if secret == "" {
			return fmt.Errorf("%w: no webhook secret kept for App %s", harness.ErrStep, app)
		}
		for i, server := range g.w.Servers {
			if bytes.Contains(server.Stdout(), []byte(secret)) {
				return fmt.Errorf("%w: server %d logs the webhook secret of App %s", harness.ErrStep, i+1, app)
			}
		}
		var leaked int
		if err := g.w.Row(ctx, `SELECT
 (SELECT count(*) FROM vcs_deliveries e WHERE account_id=$1 AND strpos(e::text,$2)>0)
+(SELECT count(*) FROM jobs e WHERE account_id=$1 AND strpos(e::text,$2)>0)
+(SELECT count(*) FROM audit_trail_entries e WHERE account_id=$1 AND strpos(e::text,$2)>0)`,
			[]any{harness.UUIDOf(harness.Home.Account), secret}, &leaked); err != nil {
			return err
		}
		if leaked != 0 {
			return fmt.Errorf("%w: stored rows contain the webhook secret of App %s", harness.ErrStep, app)
		}
	}
	return nil
}
