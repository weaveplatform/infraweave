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
	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketcloudfake"
	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketdcfake"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

type bitbucketWebhookSender interface {
	Send(context.Context, string, string, string, []byte) (int, error)
}

type bitbucketWebhookDelivery struct {
	id, event, repository string
	body                  []byte
}

type bitbucketWebhookSteps struct {
	w             *harness.World
	cloud         *bitbucketCloudSteps
	dc            *bitbucketDCSteps
	provider      string
	secret        crypt.Value
	sender        bitbucketWebhookSender
	capture       *bitbucketWebhookCapture
	deliveries    map[string]bitbucketWebhookDelivery
	providerCalls int
}

func (s *bitbucketWebhookSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the Bitbucket (Cloud|Data Center) webhook secret is kept privately$`, s.keepSecret)
	sc.Step(`^Bitbucket sends an? "([^"]+)" delivery "(\w+)" for integration "(\w+)" with "(current|wrong|missing)" signature through ([AB])$`, s.send)
	sc.Step(`^Bitbucket explicitly resends delivery "(\w+)" for integration "(\w+)" through ([AB])$`, s.resend)
	sc.Step(`^Bitbucket delivery "(\w+)" belongs to integration "(\w+)" with its exact signed bytes and one processing job$`, s.stored)
	sc.Step(`^Bitbucket delivery "(\w+)" is eventually dispatched$`, s.dispatched)
	sc.Step(`^Bitbucket delivery "(\w+)" eventually records a safe parse failure$`, s.parseFailure)
	sc.Step(`^Bitbucket integration "(\w+)" retains exactly (\d+) deliver(?:y|ies)$`, s.count)
	sc.Step(`^Bitbucket made exactly (\d+) webhook attempts$`, s.attempts)
	sc.Step(`^Bitbucket webhook processing starts no run or evaluation job and makes no provider request$`, s.noEffects)
	sc.Step(`^no Bitbucket webhook secret or malformed payload reaches server logs$`, s.noLeaks)
}

func (s *bitbucketWebhookSteps) keepSecret(provider string) error {
	var view struct {
		WebhookSecret string `json:"webhookSecret"`
	}
	if err := json.Unmarshal(s.w.Last().Body, &view); err != nil || view.WebhookSecret == "" {
		return fmt.Errorf("%w: Bitbucket create did not answer its webhook secret", harness.ErrStep)
	}
	s.provider, s.secret = provider, crypt.NewValue([]byte(view.WebhookSecret))
	s.capture = &bitbucketWebhookCapture{}
	s.sender = s.newSender(s.secret.Reveal())
	s.deliveries = map[string]bitbucketWebhookDelivery{}
	s.providerCalls = s.requests()
	return nil
}

func (s *bitbucketWebhookSteps) newSender(secret []byte) bitbucketWebhookSender {
	client := &http.Client{Transport: s.capture, Timeout: 15 * time.Second}
	if s.provider == "Cloud" {
		return bitbucketcloudfake.NewSender(secret, client)
	}
	return bitbucketdcfake.NewSender(secret, client)
}

func (s *bitbucketWebhookSteps) requests() int {
	if s.provider == "Cloud" {
		return s.cloud.fake.Requests()
	}
	return s.dc.fake.Requests()
}

func (s *bitbucketWebhookSteps) route() (provider, idHeader string) {
	if s.provider == "Cloud" {
		return "bitbucket_cloud", "X-Request-UUID"
	}
	return "bitbucket_datacenter", "X-Request-Id"
}

func (s *bitbucketWebhookSteps) send(ctx context.Context, kind, name, integration, signature, replica string) error {
	delivery, err := s.payload(kind)
	if err != nil {
		return err
	}
	delivery.id = kernel.NewULID().UUID()
	s.deliveries[name] = delivery
	sender := s.sender
	switch signature {
	case "wrong":
		sender = s.newSender([]byte("wrong-bitbucket-secret-at-least-32-bytes"))
	case "missing":
		sender = s.newSender(nil)
	}
	return s.sendDelivery(ctx, sender, delivery, integration, replica)
}

func (s *bitbucketWebhookSteps) resend(ctx context.Context, name, integration, replica string) error {
	delivery, ok := s.deliveries[name]
	if !ok {
		return fmt.Errorf("%w: no Bitbucket delivery named %s", harness.ErrStep, name)
	}
	// An explicit second send preserves the original ID and bytes while
	// allowing the request to reach the other replica.
	return s.sendDelivery(ctx, s.sender, delivery, integration, replica)
}

func (s *bitbucketWebhookSteps) sendDelivery(ctx context.Context, sender bitbucketWebhookSender, delivery bitbucketWebhookDelivery, integration, replica string) error {
	provider, _ := s.route()
	endpoint := s.w.Base(replica) + "/webhooks/vcs/" + provider + "/" + s.w.IDs[integration]
	status, err := sender.Send(ctx, endpoint, delivery.id, delivery.event, delivery.body)
	if err != nil {
		return fmt.Errorf("bitbucket webhook through %s: %w", replica, err)
	}
	if s.capture.answer == nil || s.capture.answer.Status != status {
		return fmt.Errorf("%w: Bitbucket sender lost its actual response", harness.ErrStep)
	}
	answer := *s.capture.answer
	answer.Body = bytes.Clone(answer.Body)
	s.w.Sent = append(s.w.Sent, answer)
	return nil
}

func (s *bitbucketWebhookSteps) payload(kind string) (bitbucketWebhookDelivery, error) {
	var event, repository string
	if s.provider == "Cloud" {
		repository = "{00000000-0000-4000-8000-000000000007}"
		switch kind {
		case "push", "malformed":
			event = "repo:push"
		case "pull-request":
			event = "pullrequest:created"
		case "pull-request-comment":
			event = "pullrequest:comment_created"
		case "unsupported":
			event = "repo:fork"
		}
	} else {
		repository = "7"
		switch kind {
		case "push", "malformed":
			event = "repo:refs_changed"
		case "pull-request":
			event = "pr:opened"
		case "pull-request-comment":
			event = "pr:comment:added"
		case "unsupported":
			event = "repo:modified"
		case "ping":
			event = "diagnostics:ping"
		}
	}
	if event == "" {
		return bitbucketWebhookDelivery{}, fmt.Errorf("%w: unknown Bitbucket payload kind %q", harness.ErrStep, kind)
	}
	delivery := bitbucketWebhookDelivery{event: event, repository: repository}
	if kind == "malformed" {
		delivery.body = []byte(`{"marker":"parse-body-marker-538"}`)
		delivery.repository = ""
		return delivery, nil
	}
	if kind == "unsupported" || kind == "ping" {
		delivery.body = []byte(`{}`)
		return delivery, nil
	}
	var err error
	if s.provider == "Cloud" {
		delivery.body, err = bitbucketcloudfake.Payload(event)
	} else {
		delivery.body, err = bitbucketdcfake.Payload(event)
	}
	if err != nil {
		return bitbucketWebhookDelivery{}, fmt.Errorf("bitbucket fixture payload: %w", err)
	}
	return delivery, nil
}

func (s *bitbucketWebhookSteps) stored(ctx context.Context, name, integration string) error {
	want, ok := s.deliveries[name]
	if !ok {
		return fmt.Errorf("%w: no sent Bitbucket delivery %s", harness.ErrStep, name)
	}
	var id, bound kernel.ULID
	var provider, event, repository string
	var signature, headers, body []byte
	account := harness.UUIDOf(harness.Home.Account)
	if err := s.w.Row(ctx, `SELECT id,integration_id,provider,event,signature,headers,body,repository
FROM vcs_deliveries WHERE account_id=$1 AND delivery_id=$2`, []any{account, want.id},
		&id, &bound, &provider, &event, &signature, &headers, &body, &repository); err != nil {
		return err
	}
	mac := hmac.New(sha256.New, s.secret.Reveal())
	_, _ = mac.Write(want.body)
	wantSignature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	wantProvider, idHeader := s.route()
	var safe http.Header
	if bound.String() != s.w.IDs[integration] || provider != strings.ToUpper(wantProvider) || event != want.event ||
		string(signature) != wantSignature || !bytes.Equal(body, want.body) || repository != want.repository ||
		json.Unmarshal(headers, &safe) != nil || safe.Get("X-Hub-Signature") != wantSignature ||
		safe.Get(idHeader) != want.id || safe.Get("X-Event-Key") != want.event || safe.Get("Authorization") != "" {
		return fmt.Errorf("%w: Bitbucket retained identity, signature, body or safe headers changed", harness.ErrStep)
	}
	var rows, jobs int
	if err := s.w.Row(ctx, `SELECT
 (SELECT count(*) FROM vcs_deliveries WHERE account_id=$1 AND delivery_id=$2),
 (SELECT count(*) FROM jobs WHERE account_id=$1 AND kind='vcs.process_delivery' AND unique_key=$3)`,
		[]any{account, want.id, id.String()}, &rows, &jobs); err != nil {
		return err
	}
	if rows != 1 || jobs != 1 {
		return fmt.Errorf("%w: Bitbucket delivery rows/jobs = %d/%d, want 1/1", harness.ErrStep, rows, jobs)
	}
	fmt.Printf("Bitbucket %s evidence: exact signed bytes, account/integration identity and one processing job retained\n", s.provider)
	return nil
}

func (s *bitbucketWebhookSteps) dispatched(ctx context.Context, name string) error {
	return deps.Eventually(ctx, "Bitbucket delivery to be parsed and dispatched", func(ctx context.Context) (bool, error) {
		var state string
		err := s.w.Row(ctx, `SELECT state FROM vcs_deliveries WHERE account_id=$1 AND delivery_id=$2`,
			[]any{harness.UUIDOf(harness.Home.Account), s.deliveries[name].id}, &state)
		return state == "dispatched", err
	}) //nolint:wrapcheck // the harness names the awaited state
}

func (s *bitbucketWebhookSteps) parseFailure(ctx context.Context, name string) error {
	return deps.Eventually(ctx, "Bitbucket delivery to record a safe parse failure", func(ctx context.Context) (bool, error) {
		var state, diagnostic string
		err := s.w.Row(ctx, `SELECT state,last_error FROM vcs_deliveries WHERE account_id=$1 AND delivery_id=$2`,
			[]any{harness.UUIDOf(harness.Home.Account), s.deliveries[name].id}, &state, &diagnostic)
		if err != nil || diagnostic == "" {
			return false, err
		}
		if diagnostic != "vcs: webhook payload could not be parsed" || (state != "received" && state != "failed") {
			return false, fmt.Errorf("%w: Bitbucket parse failure state/diagnostic changed", harness.ErrStep)
		}
		return true, nil
	}) //nolint:wrapcheck // the harness names the awaited diagnostic
}

func (s *bitbucketWebhookSteps) count(ctx context.Context, integration string, want int) error {
	id, err := kernel.ParseULID(s.w.IDs[integration])
	if err != nil {
		return fmt.Errorf("%w: invalid Bitbucket integration ID", harness.ErrStep)
	}
	var got int
	if err := s.w.Row(ctx, `SELECT count(*) FROM vcs_deliveries WHERE account_id=$1 AND integration_id=$2`,
		[]any{harness.UUIDOf(harness.Home.Account), id.UUID()}, &got); err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("%w: Bitbucket retained %d deliveries, want %d", harness.ErrStep, got, want)
	}
	return nil
}

func (s *bitbucketWebhookSteps) attempts(want int) error {
	if s.capture.attempts != want {
		return fmt.Errorf("%w: Bitbucket attempted %d deliveries, want %d", harness.ErrStep, s.capture.attempts, want)
	}
	return nil
}

func (s *bitbucketWebhookSteps) noEffects(ctx context.Context) error {
	var runs, evaluations int
	if err := s.w.Row(ctx, `SELECT
 (SELECT count(*) FROM runs WHERE account_id=$1),
 (SELECT count(*) FROM jobs WHERE account_id=$1 AND kind='vcs.evaluate_event')`,
		[]any{harness.UUIDOf(harness.Home.Account)}, &runs, &evaluations); err != nil {
		return err
	}
	if runs != 0 || evaluations != 0 || s.requests() != s.providerCalls {
		return fmt.Errorf("%w: Bitbucket intake created a run/evaluation or queried its provider", harness.ErrStep)
	}
	return nil
}

func (s *bitbucketWebhookSteps) noLeaks(ctx context.Context) error {
	var leaked int
	if err := s.w.Row(ctx, `SELECT
 (SELECT count(*) FROM vcs_deliveries e WHERE account_id=$1 AND strpos(e::text,$2)>0)
+(SELECT count(*) FROM jobs e WHERE account_id=$1 AND strpos(e::text,$2)>0)
+(SELECT count(*) FROM audit_trail_entries e WHERE account_id=$1 AND strpos(e::text,$2)>0)`,
		[]any{harness.UUIDOf(harness.Home.Account), string(s.secret.Reveal())}, &leaked); err != nil {
		return err
	}
	if leaked != 0 {
		return fmt.Errorf("%w: a retained Bitbucket row contains its webhook secret", harness.ErrStep)
	}
	for _, server := range s.w.Servers {
		if bytes.Contains(server.Stdout(), s.secret.Reveal()) || bytes.Contains(server.Stdout(), []byte("parse-body-marker-538")) {
			return fmt.Errorf("%w: Bitbucket webhook secret or malformed body reached a server log", harness.ErrStep)
		}
	}
	return nil
}

type bitbucketWebhookCapture struct {
	answer   *harness.Response
	attempts int
}

func (c *bitbucketWebhookCapture) RoundTrip(request *http.Request) (*http.Response, error) {
	c.attempts++
	answer, err := http.DefaultTransport.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	c.answer = &harness.Response{Status: answer.StatusCode, Header: answer.Header.Clone()}
	answer.Body = &bitbucketWebhookBody{ReadCloser: answer.Body, answer: c.answer}
	return answer, nil
}

type bitbucketWebhookBody struct {
	io.ReadCloser
	answer *harness.Response
}

func (b *bitbucketWebhookBody) Read(buffer []byte) (int, error) {
	n, err := b.ReadCloser.Read(buffer)
	b.answer.Body = append(b.answer.Body, buffer[:n]...)
	return n, err
}
