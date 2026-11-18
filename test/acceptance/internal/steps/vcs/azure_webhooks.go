package vcs

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/vcs/azuredevopsfake"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

type azureWebhookSteps struct {
	w        *harness.World
	azure    *azureDevOpsSteps
	password crypt.Value
	delivery azuredevopsfake.WebhookDelivery
	id       string
	event    string
}

func (s *azureWebhookSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the Azure webhook password is captured privately$`, s.capture)
	sc.Step(`^an Azure (push|request|comment|unsupported|invalid|malformed) webhook named "([^"]+)" is prepared$`, s.prepare)
	sc.Step(`^the Azure webhook is sent with (valid|wrong|named|missing) Basic credentials through ([AB])$`, s.send)
	sc.Step(`^the Azure webhook is retained with its exact body HMAC and reaches (dispatched|failed parsing)$`, s.retained)
	sc.Step(`^there are (\d+) Azure webhook deliveries$`, s.count)
	sc.Step(`^Azure webhook credentials never reach rows or server logs$`, s.noLeaks)
}

func (s *azureWebhookSteps) capture() error {
	var answer struct {
		Password string `json:"webhookPassword"`
	}
	if json.Unmarshal(s.w.Last().Body, &answer) != nil || len(answer.Password) < 32 {
		return fmt.Errorf("%w: Azure admin answer lacks a strong webhook password", harness.ErrStep)
	}
	s.password = crypt.NewValue([]byte(answer.Password))
	return nil
}

func (s *azureWebhookSteps) prepare(kind, id string) error {
	s.id = id
	request := azuredevopsfake.WebhookPullRequest{
		ID: 42, SourceBranch: "feature", TargetBranch: "main", Head: s.w.IDs["seed"],
		Fork: true, SourceProject: "Fork", Draft: true, Votes: []int{5},
	}
	var err error
	switch kind {
	case "push":
		s.event = "git.push"
		s.delivery, err = s.azure.fake.PushWebhook(id, "Platform", "infra", []azuredevopsfake.WebhookRefUpdate{
			{Name: "refs/heads/main", Before: strings.Repeat("0", 40), Head: s.w.IDs["seed"]},
			{Name: "refs/tags/v1", Before: strings.Repeat("0", 40), Head: s.w.IDs["seed"]},
		})
	case "request":
		s.event = "git.pullrequest.created"
		s.delivery, err = s.azure.fake.PullRequestWebhook(id, s.event, "Platform", "infra", request)
	case "comment":
		s.event = "ms.vss-code.git-pullrequest-comment-event"
		s.delivery, err = s.azure.fake.CommentWebhook(id, "Platform", "infra", request, "/spacelift plan")
	case "unsupported":
		s.event = "build.complete"
		s.delivery.Body, err = json.Marshal(map[string]any{"id": id, "eventType": s.event, "resource": 17})
	case "invalid":
		s.event = "azure-invalid-envelope"
		s.delivery.Body = []byte(`{"eventType":null,"resource":"invalid-envelope-marker"}`)
		hash := sha256.Sum256(s.delivery.Body)
		s.id = hex.EncodeToString(hash[:])
	case "malformed":
		s.event = "git.push"
		s.delivery.Body, err = json.Marshal(map[string]any{"id": id, "eventType": s.event, "resource": 17})
	}
	if err != nil {
		return fmt.Errorf("azure webhook fixture: %w", err)
	}
	return nil
}

type azureWebhookTransport struct {
	w *harness.World
}

func (t azureWebhookTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := http.DefaultTransport.RoundTrip(request)
	if err != nil {
		return nil, fmt.Errorf("azure webhook transport: %w", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("azure webhook answer: %w", err)
	}
	t.w.Sent = append(t.w.Sent, harness.Response{Status: response.StatusCode, Header: response.Header.Clone(), Body: body})
	response.Body = http.NoBody
	return response, nil
}

func (s *azureWebhookSteps) send(ctx context.Context, mode, replica string) error {
	endpoint := s.w.Base(replica) + "/webhooks/vcs/azure_devops/" + s.w.IDs["integration"]
	client := &http.Client{Transport: azureWebhookTransport{w: s.w}, Timeout: 15 * time.Second}
	if mode == "missing" {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(s.delivery.Body))
		if err != nil {
			return fmt.Errorf("azure webhook request: %w", err)
		}
		response, err := client.Do(request)
		if err != nil {
			return fmt.Errorf("azure webhook send: %w", err)
		}
		_ = response.Body.Close()
		return nil
	}
	username, password := "", s.password.Reveal()
	if mode == "wrong" {
		password = []byte("wrong-azure-webhook-password-at-least-32-bytes")
	}
	if mode == "named" {
		username = "personal-access-token"
	}
	sender := azuredevopsfake.NewWebhookSender(azuredevopsfake.WebhookSenderOptions{Client: client})
	if _, err := sender.Send(ctx, endpoint, username, password, s.delivery); err != nil {
		return fmt.Errorf("azure fake sender: %w", err)
	}
	if len(sender.Attempts()) != 1 {
		return fmt.Errorf("%w: Azure healthy/terminal answer unexpectedly retried", harness.ErrStep)
	}
	return nil
}

func (s *azureWebhookSteps) retained(ctx context.Context, state string) error {
	var body, signature, headers []byte
	var event string
	args := []any{harness.UUIDOf(s.w.IDs["integration"]), s.id}
	if err := s.w.Row(
		ctx,
		`SELECT body, signature, headers, event FROM vcs_deliveries WHERE integration_id=$1 AND delivery_id=$2`,
		args,
		&body,
		&signature,
		&headers,
		&event,
	); err != nil {
		return err
	}
	mac := hmac.New(sha256.New, s.password.Reveal())
	_, _ = mac.Write(body)
	if !bytes.Equal(body, s.delivery.Body) || !hmac.Equal(signature, []byte(hex.EncodeToString(mac.Sum(nil)))) || event != s.event ||
		bytes.Contains(bytes.ToLower(headers), []byte("authorization")) {
		return fmt.Errorf("%w: Azure retained body, event, HMAC or header privacy differs", harness.ErrStep)
	}
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		var status, lastError string
		if err := s.w.Row(ctx, `SELECT state, coalesce(last_error,'') FROM vcs_deliveries WHERE integration_id=$1 AND delivery_id=$2`, args, &status, &lastError); err != nil {
			return err
		}
		if state == "dispatched" && status == "dispatched" || state == "failed parsing" && lastError == "vcs: webhook payload could not be parsed" {
			fmt.Printf("Azure webhook %s: event=%s state=%s bodyHMAC=verified noAuthorization=true\n", s.id, event, status)
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("azure webhook processing: %w", ctx.Err())
		case <-deadline.C:
			return fmt.Errorf("%w: Azure delivery did not reach %s", harness.ErrStep, state)
		case <-tick.C:
		}
	}
}

func (s *azureWebhookSteps) count(ctx context.Context, want int) error {
	var count int
	if err := s.w.Row(ctx, `SELECT count(*) FROM vcs_deliveries WHERE integration_id=$1`, []any{harness.UUIDOf(s.w.IDs["integration"])}, &count); err != nil {
		return err
	}
	if count != want {
		return fmt.Errorf("%w: Azure delivery count=%d want=%d", harness.ErrStep, count, want)
	}
	return nil
}

func (s *azureWebhookSteps) noLeaks(ctx context.Context) error {
	password := s.password.Reveal()
	for _, needle := range [][]byte{password, []byte("Basic " + base64.StdEncoding.EncodeToString(append([]byte(":"), password...)))} {
		for _, server := range s.w.Servers {
			if bytes.Contains(server.Stdout(), needle) {
				return fmt.Errorf("%w: Azure credential reached a server log", harness.ErrStep)
			}
		}
		var count int
		if err := s.w.Row(ctx, `SELECT count(*) FROM vcs_deliveries d WHERE integration_id=$1 AND strpos(row_to_json(d)::text,$2)>0`,
			[]any{harness.UUIDOf(s.w.IDs["integration"]), string(needle)}, &count); err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("%w: Azure credential reached retained storage", harness.ErrStep)
		}
	}
	return nil
}
