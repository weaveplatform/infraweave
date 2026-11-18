package pg_test

import (
	"bytes"
	"crypto/fips140"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
	gitlabprovider "github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/gitlab"
	adapter "github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/http"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type gitlabParsed struct {
	port.Webhooks
	events *[]vcsapi.Event
}

func (p gitlabParsed) Parse(h http.Header, b []byte) ([]vcsapi.Event, error) {
	events, err := p.Webhooks.Parse(h, b)
	*p.events = slices.Clone(events)
	return events, err
}

func gitlabWebhookWorld(t *testing.T, secret []byte) (*webhookWorld, *httptest.Server, *[]vcsapi.Event) {
	t.Helper()
	w := newWebhookWorld(t)
	in := integration.Integration{
		Account:   w.f.acct,
		ID:        kernel.NewULID(),
		Provider:  integration.GitLab,
		Slug:      "gitlab",
		Name:      "GitLab",
		Space:     w.f.acct.ULID,
		Labels:    []string{},
		Checks:    integration.Checks{Mode: "INDIVIDUAL"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Version:   1,
	}
	if err := w.f.store.Create(t.Context(), in, integration.Secrets{Credential: []byte("source-credential-is-not-webhook-token"), WebhookSecret: secret}); err != nil {
		t.Fatal(err)
	}
	var parsed []vcsapi.Event
	w.id, w.secret = in.ID, secret
	w.deps.Providers = map[string]port.Webhooks{integration.GitLab: gitlabParsed{Webhooks: gitlabprovider.New(gitlabprovider.Options{}), events: &parsed}}
	w.svc = intake.New(w.deps)
	r := chi.NewRouter()
	r.Method(http.MethodPost, adapter.WebhookPattern, adapter.Webhook(w.svc))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return w, srv, &parsed
}

func gitlabDigest(secret, body []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(body)
	return []byte(hex.EncodeToString(mac.Sum(nil)))
}

func TestAccGitLabWebhookIntake(t *testing.T) {
	secret := []byte("gitlab-current-secret-at-least-32-bytes")
	w, srv, parsed := gitlabWebhookWorld(t, secret)
	fake := gitlabfake.New()
	sender := gitlabfake.NewWebhookSender(srv.Client())
	endpoint := srv.URL + "/webhooks/vcs/gitlab/" + w.id.String()
	request := gitlabfake.WebhookMergeRequest{
		IID:             42,
		Action:          "open",
		SourceBranch:    "feature",
		TargetBranch:    "main",
		Head:            strings.Repeat("2", 40),
		SourceNamespace: "forks/team",
		Draft:           true,
	}
	for _, tc := range []struct {
		name, kind string
		payload    func() (gitlabfake.WebhookDelivery, error)
	}{
		{"push", "push", func() (gitlabfake.WebhookDelivery, error) {
			return fake.PushWebhook("acme/platform", "infra", "main", strings.Repeat("1", 40), strings.Repeat("2", 40))
		}},
		{"mr", "pull_request", func() (gitlabfake.WebhookDelivery, error) {
			return fake.MergeRequestWebhook("acme/platform", "infra", request)
		}},
		{"note", "comment", func() (gitlabfake.WebhookDelivery, error) {
			return fake.NoteWebhook("acme/platform", "infra", request, "/spacelift plan")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			delivery, err := tc.payload()
			if err != nil {
				t.Fatal(err)
			}
			delivery.ID = tc.name
			status, err := sender.Send(t.Context(), endpoint, secret, delivery)
			if err != nil || status != 202 {
				t.Fatal("valid GitLab delivery refused", status, err)
			}
			stored := w.delivery(t, delivery.ID)
			if stored.Provider != integration.GitLab || stored.Event != delivery.Event || !bytes.Equal(stored.Body, delivery.Body) ||
				!hmac.Equal(stored.Signature, gitlabDigest(secret, stored.Body)) ||
				stored.Headers.Get("X-Gitlab-Token") != "" ||
				stored.Headers.Get("Authorization") != "" ||
				stored.Headers.Get("X-Gitlab-Event") != delivery.Event ||
				stored.Headers.Get("X-Gitlab-Event-UUID") != delivery.ID {
				t.Fatal("retained GitLab body/signature/safe metadata changed")
			}
			if stored.Repository == "" || stored.State != intake.Received {
				t.Fatal("stable repository or received state missing")
			}
			status, err = sender.Send(t.Context(), endpoint, secret, delivery)
			if err != nil || status != 200 {
				t.Fatal("explicit resend was not deduplicated", status, err)
			}
			job := w.claim(t, w.queue, "gitlab-test")
			if err := w.svc.Processed(t.Context(), job); err != nil {
				t.Fatal(err)
			}
			if len(*parsed) != 1 || (*parsed)[0].Type != tc.kind || (*parsed)[0].Repository.ID != stored.Repository {
				t.Fatal("real processor did not use the GitLab normalizer")
			}
			if (*parsed)[0].PullRequest != nil && ((*parsed)[0].PullRequest.Number != 42 || !(*parsed)[0].PullRequest.Fork || (*parsed)[0].PullRequest.HeadOwner != "forks/team") {
				t.Fatal("MR IID/fork metadata lost")
			}
			if w.delivery(t, delivery.ID).State != intake.Dispatched {
				t.Fatal("processed delivery did not settle")
			}
		})
	}
	if len(sender.Attempts()) != 6 {
		t.Fatal("fake retried without an explicit resend")
	}
	var leaked, fanout int
	if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM vcs_deliveries d WHERE account_id=$1 AND strpos(row_to_json(d)::text,$2)>0`, w.f.acct, string(secret)).
		Scan(&leaked); err != nil ||
		leaked != 0 {
		t.Fatal("raw token reached a delivery row", leaked, err)
	}
	if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE account_id=$1 AND kind='vcs.evaluate_event'`, w.f.acct).Scan(&fanout); err != nil || fanout != 0 {
		t.Fatal("intake started out-of-scope fanout", fanout, err)
	}
}

//nolint:misspell // GitLab's documented JSON key is noteable_type.
func TestAccGitLabWebhookFilterAndParseFailure(t *testing.T) {
	secret := []byte("gitlab-filter-secret-at-least-32-bytes")
	w, srv, _ := gitlabWebhookWorld(t, secret)
	sender := gitlabfake.NewWebhookSender(srv.Client())
	endpoint := srv.URL + "/webhooks/vcs/gitlab/" + w.id.String()
	for _, delivery := range []gitlabfake.WebhookDelivery{
		{ID: "issue", Event: "Note Hook", Body: []byte(`{"object_kind":"note","object_attributes":{"noteable_type":"Issue","note":"ignored"},"issue":{"iid":1}}`)},
		{ID: "approval", Event: "Merge Request Hook", Body: []byte(`{"object_kind":"merge_request","object_attributes":{"action":"approval"}}`)},
		{ID: "pipeline", Event: "Pipeline Hook", Body: []byte(`{}`)},
	} {
		status, err := sender.Send(t.Context(), endpoint, secret, delivery)
		if err != nil || status != 200 {
			t.Fatal("unsupported event was not filtered", status, err)
		}
	}
	for _, token := range [][]byte{nil, []byte("wrong-token")} {
		status, err := sender.Send(t.Context(), endpoint, token, gitlabfake.WebhookDelivery{ID: "refused", Event: "Push Hook", Body: []byte(`{}`)})
		if err != nil || status != 401 {
			t.Fatal("invalid token was not refused before parsing", status, err)
		}
	}
	var count int
	if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM vcs_deliveries WHERE account_id=$1`, w.f.acct).Scan(&count); err != nil || count != 0 {
		t.Fatal("filtered/refused delivery was retained", count, err)
	}
	bad := gitlabfake.WebhookDelivery{
		ID:    "malformed",
		Event: "Merge Request Hook",
		Body:  []byte(`{"object_kind":"merge_request","object_attributes":{"action":"open"},"private":"body-marker-must-not-be-in-error"}`),
	}
	status, err := sender.Send(t.Context(), endpoint, secret, bad)
	if err != nil || status != 202 {
		t.Fatal("malformed consumed payload was silently filtered", status, err)
	}
	if _, err := w.f.db.Exec(t.Context(), `UPDATE jobs SET max_attempts=1 WHERE account_id=$1 AND kind=$2`, w.f.acct, intake.Process.Name); err != nil {
		t.Fatal(err)
	}
	job := w.claim(t, w.queue, "gitlab-parser")
	err = w.svc.Processed(t.Context(), job)
	if err == nil {
		t.Fatal("malformed consumed payload parsed successfully")
	}
	if dead, fail := w.queue.Fail(t.Context(), job, err); fail != nil || !dead {
		t.Fatal("parse job did not become dead", dead, fail)
	}
	stored := w.delivery(t, bad.ID)
	if stored.State != intake.Failed || stored.LastError != "vcs: webhook payload could not be parsed" || strings.Contains(stored.LastError, "body-marker") {
		t.Fatal("malformed payload did not fail with a safe diagnostic")
	}
}

func TestAccGitLabShortWebhookTokenPolicy(t *testing.T) {
	secret := []byte("hook")
	w, srv, _ := gitlabWebhookWorld(t, secret)
	delivery, err := gitlabfake.New().PushWebhook("acme", "infra", "main", strings.Repeat("1", 40), strings.Repeat("2", 40))
	if err != nil {
		t.Fatal(err)
	}
	delivery.ID = "short-token"
	status, err := gitlabfake.NewWebhookSender(srv.Client()).Send(t.Context(), srv.URL+"/webhooks/vcs/gitlab/"+w.id.String(), secret, delivery)
	want := 202
	if fips140.Enforced() {
		want = 401
	}
	if err != nil || status != want {
		t.Fatal("short token did not follow cryptographic policy", status, err)
	}
	if want == 202 && !hmac.Equal(w.delivery(t, delivery.ID).Signature, gitlabDigest(secret, delivery.Body)) {
		t.Fatal("ordinary short-token HMAC changed")
	}
}

func TestAccGitLabPreviousTokenWindow(t *testing.T) {
	current := []byte("gitlab-current-secret-at-least-32-bytes")
	previous := []byte("gitlab-previous-secret-at-least-32-bytes")
	w, _, _ := gitlabWebhookWorld(t, current)
	clock := time.Now().UTC().Truncate(time.Microsecond)
	expires := clock.Add(time.Hour)
	var column crypt.Column
	for _, candidate := range pg.SealedColumns() {
		if candidate.Name == "previous_webhook_secret_sealed" {
			column = candidate
		}
	}
	if column.Name == "" {
		t.Fatal("previous secret column is not registered")
	}
	sealed, err := w.keys.SealColumn(t.Context(), column, []string{w.f.acct.UUID(), w.id.UUID()}, previous)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.f.db.Exec(t.Context(), `UPDATE vcs_integrations SET previous_webhook_secret_sealed=$2,
		previous_secret_valid_until=$3,credentials_sealed=$4 WHERE id=$1`, w.id, sealed, expires, []byte("unreadable-source-credential")); err != nil {
		t.Fatal(err)
	}
	w.deps.Now = func() time.Time { return clock }
	w.svc = intake.New(w.deps)
	delivery, err := gitlabfake.New().PushWebhook("acme/platform", "infra", "main", strings.Repeat("1", 40), strings.Repeat("2", 40))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id     string
		secret []byte
		at     time.Time
		code   int
	}{
		{"current", current, clock, 202},
		{"previous", previous, expires.Add(-time.Nanosecond), 202},
		{"expired", previous, expires, 401},
		{"current-after", current, expires, 202},
	} {
		clock = tc.at
		headers := http.Header{}
		headers.Set("X-Gitlab-Token", string(tc.secret))
		headers.Set("X-Gitlab-Event", delivery.Event)
		headers.Set("X-Gitlab-Event-UUID", tc.id)
		status, err := w.svc.Receive(t.Context(), "gitlab", w.id.String(), headers, delivery.Body)
		if tc.code == 401 {
			if !errors.Is(err, intake.ErrVerification) {
				t.Fatal("expired token was not refused", err)
			}
			continue
		}
		if err != nil || status != tc.code {
			t.Fatal("allowed token was refused", status, err)
		}
		if !hmac.Equal(w.delivery(t, tc.id).Signature, gitlabDigest(tc.secret, delivery.Body)) {
			t.Fatal("digest did not use the exact matched current/previous secret")
		}
	}
	var count int
	if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM vcs_deliveries WHERE account_id=$1`, w.f.acct).Scan(&count); err != nil || count != 3 {
		t.Fatal("expired token left a delivery row", count, err)
	}
}
