package vcs

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

type webhookSteps struct{ w *harness.World }

func (s *webhookSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^a webhook body of (\d+) bytes is sent to "([^"]+)" through ([AB])$`, s.sized)
	sc.Step(`^a correctly signed GitHub webhook for integration "(\w+)" is sent through ([AB])$`, s.signed)
	sc.Step(`^the webhook answer is empty with the shared headers$`, s.empty)
	sc.Step(`^no webhook delivery, processing job or inbox object is stored$`, s.nothing)
	sc.Step(`^two infraweave servers, A and B, on a PostgreSQL of their own$`, s.ownDatabase)
	sc.Step(`^the webhook database stops$`, s.stopDatabase)
	sc.Step(`^the webhook database starts again$`, s.startDatabase)
}

func (s *webhookSteps) sized(ctx context.Context, size int, path, replica string) error {
	return s.send(ctx, replica, s.w.Resolve(path), strings.Repeat("x", size), nil)
}

// signed uses the sealed secret of vcsSteps.integration. Its signature is
// correct even though an exhausted budget refuses it before verification.
func (s *webhookSteps) signed(ctx context.Context, name, replica string) error {
	body := `{"ref":"refs/heads/main","before":"1111111111111111111111111111111111111111","after":"2222222222222222222222222222222222222222","repository":{"id":1,"name":"infra","owner":{"login":"acme"}}}`
	mac := hmac.New(sha256.New, []byte("webhook_secret_sealed of "+name))
	_, _ = mac.Write([]byte(body))
	headers := http.Header{
		"X-Github-Event":      {"push"},
		"X-Github-Delivery":   {"bounded-delivery"},
		"X-Hub-Signature-256": {"sha256=" + hex.EncodeToString(mac.Sum(nil))},
	}
	return s.send(ctx, replica, "/webhooks/vcs/github_enterprise/"+s.w.IDs[name], body, headers)
}

// send is a provider request, with no SDK authentication or retry. The bogus
// bearer header proves that intake ignores API credentials on its own prefix.
func (s *webhookSteps) send(ctx context.Context, replica, path, body string, headers http.Header) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.w.Base(replica)+path, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook request: %w", err)
	}
	req.Header = headers.Clone()
	if req.Header == nil {
		req.Header = http.Header{}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer unrelated-api-credential")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("webhook through %s: %w", replica, err)
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("webhook answer: %w", err)
	}
	s.w.Sent = append(s.w.Sent, harness.Response{Status: resp.StatusCode, Header: resp.Header, Body: answer})
	return nil
}

func (s *webhookSteps) empty() error {
	r := s.w.Last()
	if len(r.Body) != 0 || r.Header.Get("X-Request-Id") == "" ||
		r.Header.Get("Cache-Control") != "no-store" || r.Header.Get("X-Content-Type-Options") != "nosniff" ||
		r.Header.Get("WWW-Authenticate") != "" || r.Header.Get("Content-Type") != "" {
		return fmt.Errorf("%w: webhook status %d, headers %v, body %q", harness.ErrStep, r.Status, r.Header, r.Body)
	}
	return nil
}

func (s *webhookSteps) nothing(ctx context.Context) error {
	db, err := s.w.Querier(ctx)
	if err != nil {
		return err //nolint:wrapcheck // the harness names the database
	}
	out, err := db.Query(ctx, `SELECT
		(SELECT count(*) FROM vcs_deliveries) +
		(SELECT count(*) FROM jobs WHERE kind = 'vcs.process_delivery') +
		(SELECT count(*) FROM blob_objects WHERE store = 'inbox')`)
	if err != nil {
		return err //nolint:wrapcheck // the harness names the query
	}
	if strings.TrimSpace(out) != "0" {
		return fmt.Errorf("%w: a refused webhook left deliveries, jobs or objects: %q", harness.ErrStep, out)
	}
	return nil
}

func (s *webhookSteps) ownDatabase(ctx context.Context) error {
	pg, err := s.w.Suite.Deps.OwnPostgres(ctx)
	if err != nil {
		return err //nolint:wrapcheck // the dependency names the container
	}
	s.w.OwnPG = pg
	config, err := harness.ServerConfig(pg.DSN, s.w.Objects(), nil)
	if err != nil {
		return err //nolint:wrapcheck // the harness names the configuration
	}
	for range 2 {
		if _, err := s.w.Launch(ctx, "all", config); err != nil {
			return err //nolint:wrapcheck // the harness names the launch
		}
	}
	return nil
}

func (s *webhookSteps) stopDatabase(ctx context.Context) error {
	return s.w.OwnPG.Stop(ctx) //nolint:wrapcheck // the dependency names the container
}

func (s *webhookSteps) startDatabase(ctx context.Context) error {
	return s.w.OwnPG.Start(ctx) //nolint:wrapcheck // the dependency names the container
}
