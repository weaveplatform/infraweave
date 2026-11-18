package vcs

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

type gitlabWebhookSteps struct {
	w             *harness.World
	provider      *gitlabSteps
	secret        crypt.Value
	sender        *gitlabfake.WebhookSender
	capture       *gitlabWebhookCapture
	deliveries    map[string]gitlabfake.WebhookDelivery
	providerCalls int
}

func (s *gitlabWebhookSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the GitLab webhook secret is kept privately$`, s.keepSecret)
	sc.Step(`^GitLab sends an? "([^"]+)" delivery "(\w+)" for integration "(\w+)" with "(current|wrong|missing)" token through ([AB])$`, s.send)
	sc.Step(`^GitLab explicitly resends delivery "(\w+)" for integration "(\w+)" through ([AB])$`, s.resend)
	sc.Step(`^GitLab delivery "(\w+)" for integration "(\w+)" retains exact bytes and the body HMAC$`, s.retained)
	sc.Step(`^GitLab delivery "(\w+)" for integration "(\w+)" is eventually dispatched$`, s.dispatched)
	sc.Step(`^GitLab delivery "(\w+)" for integration "(\w+)" eventually records a safe parse failure$`, s.parseFailure)
	sc.Step(`^GitLab integration "(\w+)" retains exactly (\d+) deliver(?:y|ies)$`, s.count)
	sc.Step(`^GitLab made exactly (\d+) webhook attempts$`, s.attempts)
	sc.Step(`^GitLab webhook processing starts no run or evaluation job and makes no provider request$`, s.noEffects)
	sc.Step(`^no GitLab webhook token reaches retained rows or server logs$`, s.noLeaks)
}

func (s *gitlabWebhookSteps) keepSecret() error {
	var view struct {
		WebhookSecret string `json:"webhookSecret"`
	}
	if err := json.Unmarshal(s.w.Last().Body, &view); err != nil || view.WebhookSecret == "" {
		return fmt.Errorf("%w: GitLab create did not answer its webhook secret", harness.ErrStep)
	}
	s.secret = crypt.NewValue([]byte(view.WebhookSecret))
	s.deliveries = map[string]gitlabfake.WebhookDelivery{}
	s.capture = &gitlabWebhookCapture{}
	s.sender = gitlabfake.NewWebhookSender(&http.Client{Transport: s.capture, Timeout: 15 * time.Second})
	s.providerCalls = s.provider.fake.Requests()
	return nil
}

func (s *gitlabWebhookSteps) send(ctx context.Context, kind, name, integration, token, replica string) error {
	delivery, err := s.payload(kind)
	if err != nil {
		return err
	}
	delivery.ID = kernel.NewULID().UUID()
	s.deliveries[name] = delivery
	secret := s.secret.Reveal()
	if token == "wrong" {
		secret = []byte("wrong-gitlab-secret-at-least-32-bytes")
	}
	if token == "missing" {
		secret = nil
	}
	return s.sendDelivery(ctx, integration, replica, secret, delivery)
}

func (s *gitlabWebhookSteps) resend(ctx context.Context, name, integration, replica string) error {
	delivery, ok := s.deliveries[name]
	if !ok {
		return fmt.Errorf("%w: no GitLab delivery named %s", harness.ErrStep, name)
	}
	return s.sendDelivery(ctx, integration, replica, s.secret.Reveal(), delivery)
}

func (s *gitlabWebhookSteps) sendDelivery(ctx context.Context, integration, replica string, token []byte, delivery gitlabfake.WebhookDelivery) error {
	endpoint := s.w.Base(replica) + "/webhooks/vcs/gitlab/" + s.w.IDs[integration]
	status, err := s.sender.Send(ctx, endpoint, token, delivery)
	if err != nil {
		return fmt.Errorf("GitLab webhook through %s: %w", replica, err)
	}
	if s.capture.answer == nil || s.capture.answer.Status != status {
		return fmt.Errorf("%w: GitLab sender lost its actual response", harness.ErrStep)
	}
	answer := *s.capture.answer
	answer.Body = bytes.Clone(answer.Body)
	s.w.Sent = append(s.w.Sent, answer)
	return nil
}

//nolint:misspell // GitLab's documented noteable_type wire field.
func (s *gitlabWebhookSteps) payload(kind string) (gitlabfake.WebhookDelivery, error) {
	request := gitlabfake.WebhookMergeRequest{
		IID:             42,
		Action:          "open",
		SourceBranch:    "feature/notes",
		TargetBranch:    "main",
		Head:            s.w.IDs["seed"],
		SourceNamespace: "forks/team",
		Draft:           true,
		Labels:          []string{"review"},
	}
	switch kind {
	case "push":
		return s.provider.fake.PushWebhook("acme/platform", "infra", "main", strings.Repeat("0", 40), s.w.IDs["seed"])
	case "tag":
		return s.provider.fake.TagWebhook("acme/platform", "infra", "v1", strings.Repeat("0", 40), s.w.IDs["seed"])
	case "merge-request":
		return s.provider.fake.MergeRequestWebhook("acme/platform", "infra", request)
	case "merge-request-note":
		return s.provider.fake.NoteWebhook("acme/platform", "infra", request, "/spacelift plan")
	case "pipeline":
		return gitlabfake.WebhookDelivery{Event: "Pipeline Hook", Body: []byte(`{"object_kind":"pipeline"}`)}, nil
	case "malformed":
		return gitlabfake.WebhookDelivery{
			Event: "Merge Request Hook",
			Body:  []byte(`{"object_kind":"merge_request","object_attributes":{"action":"open"},"marker":"parse-body-marker-537"}`),
		}, nil
	case "issue-note":
		return gitlabfake.WebhookDelivery{
			Event: "Note Hook",
			Body:  []byte(`{"object_kind":"note","object_attributes":{"noteable_type":"Issue","note":"ignored"},"issue":{"iid":1}}`),
		}, nil
	default:
		return gitlabfake.WebhookDelivery{}, fmt.Errorf("%w: unknown GitLab payload kind %q", harness.ErrStep, kind)
	}
}

type gitlabWebhookRow struct {
	ID          string      `json:"deliveryId"`
	Provider    string      `json:"provider"`
	Event       string      `json:"event"`
	State       string      `json:"state"`
	LastError   string      `json:"lastError"`
	Headers     http.Header `json:"headers"`
	Signature   string      `json:"signature"`
	Body        []byte      `json:"body"`
	Repository  string      `json:"repository"`
	RunsCreated int         `json:"runsCreated"`
}

func (s *gitlabWebhookSteps) rows(ctx context.Context, integration string) ([]gitlabWebhookRow, error) {
	id, err := kernel.ParseULID(s.w.IDs[integration])
	if err != nil {
		return nil, fmt.Errorf("%w: invalid GitLab integration ID", harness.ErrStep)
	}
	db, err := s.w.Querier(ctx)
	if err != nil {
		return nil, err
	}
	// UUID() supplies a validated UUID literal; no arbitrary feature text enters SQL.
	query := fmt.Sprintf(`SELECT coalesce(jsonb_agg(jsonb_build_object('deliveryId',delivery_id,'provider',provider,'event',event,
		'state',state,'lastError',last_error,'headers',headers,'signature',convert_from(signature,'UTF8'),
		'body',encode(body,'base64'),'repository',repository,'runsCreated',runs_created)),'[]'::jsonb)::text
		FROM vcs_deliveries WHERE integration_id='%s'::uuid`, id.UUID())
	raw, err := db.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	var rows []gitlabWebhookRow
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return nil, fmt.Errorf("GitLab delivery rows: %w", err)
	}
	return rows, nil
}

func (s *gitlabWebhookSteps) row(ctx context.Context, name, integration string) (gitlabWebhookRow, error) {
	delivery, ok := s.deliveries[name]
	if !ok {
		return gitlabWebhookRow{}, fmt.Errorf("%w: no GitLab delivery named %s", harness.ErrStep, name)
	}
	rows, err := s.rows(ctx, integration)
	if err != nil {
		return gitlabWebhookRow{}, err
	}
	for _, row := range rows {
		if row.ID == delivery.ID {
			return row, nil
		}
	}
	return gitlabWebhookRow{}, fmt.Errorf("%w: GitLab delivery %s was not retained", harness.ErrStep, name)
}

func (s *gitlabWebhookSteps) retained(ctx context.Context, name, integration string) error {
	row, err := s.row(ctx, name, integration)
	if err != nil {
		return err
	}
	delivery := s.deliveries[name]
	mac := hmac.New(sha256.New, s.secret.Reveal())
	_, _ = mac.Write(delivery.Body)
	if row.Provider != "GITLAB" || row.Event != delivery.Event || !bytes.Equal(row.Body, delivery.Body) || row.Signature != hex.EncodeToString(mac.Sum(nil)) ||
		row.RunsCreated != 0 {
		return fmt.Errorf("%w: GitLab retained bytes/digest/provider changed", harness.ErrStep)
	}
	if row.Repository == "" && name != "malformed" {
		return fmt.Errorf("%w: GitLab stable project identity missing", harness.ErrStep)
	}
	if row.Headers.Get("X-Gitlab-Event") != delivery.Event || row.Headers.Get("X-Gitlab-Event-UUID") != delivery.ID {
		return fmt.Errorf("%w: GitLab retained event/UUID metadata changed", harness.ErrStep)
	}
	for key := range row.Headers {
		if strings.EqualFold(key, "X-Gitlab-Token") || strings.EqualFold(key, "Authorization") {
			return fmt.Errorf("%w: secret-bearing GitLab header retained", harness.ErrStep)
		}
	}
	fmt.Printf("GitLab webhook evidence: event=%s exact-body=true matched-secret-body-HMAC=true credential-headers=false\n", delivery.Event)
	return nil
}

func (s *gitlabWebhookSteps) dispatched(ctx context.Context, name, integration string) error {
	return deps.Eventually(ctx, "GitLab delivery to be dispatched", func(ctx context.Context) (bool, error) {
		row, err := s.row(ctx, name, integration)
		return err == nil && row.State == "dispatched", err
	})
}

func (s *gitlabWebhookSteps) parseFailure(ctx context.Context, name, integration string) error {
	return deps.Eventually(ctx, "GitLab consumed payload to record a safe parse failure", func(ctx context.Context) (bool, error) {
		row, err := s.row(ctx, name, integration)
		if err != nil {
			return false, err
		}
		if row.LastError == "" {
			return false, nil
		}
		if row.LastError != "vcs: webhook payload could not be parsed" || (row.State != "received" && row.State != "failed") {
			return false, fmt.Errorf("%w: GitLab parse failure diagnostic/state changed", harness.ErrStep)
		}
		fmt.Println("GitLab webhook evidence: consumed malformed body retained; parser diagnostic is fixed and redacted")
		return true, nil
	})
}

func (s *gitlabWebhookSteps) count(ctx context.Context, integration string, want int) error {
	rows, err := s.rows(ctx, integration)
	if err != nil {
		return err
	}
	if len(rows) != want {
		return fmt.Errorf("%w: GitLab retained %d deliveries, want %d", harness.ErrStep, len(rows), want)
	}
	return nil
}

func (s *gitlabWebhookSteps) attempts(want int) error {
	if len(s.sender.Attempts()) != want {
		return fmt.Errorf("%w: GitLab attempted %d deliveries, want %d", harness.ErrStep, len(s.sender.Attempts()), want)
	}
	return nil
}

func (s *gitlabWebhookSteps) noEffects(ctx context.Context) error {
	db, err := s.w.Querier(ctx)
	if err != nil {
		return err
	}
	raw, err := db.Query(ctx, `SELECT (SELECT count(*) FROM runs)+(SELECT count(*) FROM jobs WHERE kind='vcs.evaluate_event')`)
	if err != nil {
		return err
	}
	if strings.TrimSpace(raw) != "0" || s.provider.fake.Requests() != s.providerCalls {
		return fmt.Errorf("%w: GitLab intake created a run/evaluation or queried its provider", harness.ErrStep)
	}
	return nil
}

func (s *gitlabWebhookSteps) noLeaks(ctx context.Context) error {
	db, err := s.w.Querier(ctx)
	if err != nil {
		return err
	}
	raw, err := db.Query(ctx, `SELECT jsonb_build_object('deliveries',(SELECT coalesce(jsonb_agg(to_jsonb(d)),'[]'::jsonb) FROM vcs_deliveries d),
		'jobs',(SELECT coalesce(jsonb_agg(to_jsonb(j)),'[]'::jsonb) FROM jobs j))::text`)
	if err != nil {
		return err
	}
	if bytes.Contains([]byte(raw), s.secret.Reveal()) {
		return fmt.Errorf("%w: GitLab token reached retained metadata", harness.ErrStep)
	}
	for _, server := range s.w.Servers {
		if bytes.Contains(server.Stdout(), s.secret.Reveal()) || bytes.Contains(server.Stdout(), []byte("parse-body-marker-537")) {
			return fmt.Errorf("%w: GitLab token or parser payload reached a server log", harness.ErrStep)
		}
	}
	return nil
}

type gitlabWebhookCapture struct{ answer *harness.Response }

func (c *gitlabWebhookCapture) RoundTrip(request *http.Request) (*http.Response, error) {
	answer, err := http.DefaultTransport.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	c.answer = &harness.Response{Status: answer.StatusCode, Header: answer.Header.Clone()}
	answer.Body = &gitlabWebhookBody{ReadCloser: answer.Body, answer: c.answer}
	return answer, nil
}

type gitlabWebhookBody struct {
	io.ReadCloser
	answer *harness.Response
}

func (b *gitlabWebhookBody) Read(buffer []byte) (int, error) {
	n, err := b.ReadCloser.Read(buffer)
	b.answer.Body = append(b.answer.Body, buffer[:n]...)
	return n, err
}
