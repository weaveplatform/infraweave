package pg_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/githubfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/github"
	adapter "github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/http"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type capturedGitHub struct {
	*github.Provider
	events *[]vcsapi.Event
}

func (p capturedGitHub) Parse(h http.Header, body []byte) ([]vcsapi.Event, error) {
	events, err := p.Provider.Parse(h, body)
	*p.events = events
	return events, err
}

// The real inbox keeps GitHub's exact signature and invokes the provider's
// real parser when processing the claimed job. Neither host needs an API call.
func TestAccGitHubAppWebhook(t *testing.T) {
	for _, host := range []string{"github.com", "GHES"} {
		t.Run(host, func(t *testing.T) {
			w := newWebhookWorld(t)
			f := githubfake.New()
			app := f.AddApp("infraweave", "acme")
			in, err := w.f.store.Get(t.Context(), w.f.acct, w.id, false)
			if err != nil {
				t.Fatal(err)
			}
			in.Settings.AppID = strconv.FormatInt(app.ID, 10)
			if host == "GHES" {
				srv := f.Start()
				t.Cleanup(srv.Close)
				in.Settings.APIHost = srv.URL
			}
			w.secret = []byte(app.WebhookSecret)
			if err := w.f.store.Write(t.Context(), in, &integration.Secrets{Credential: app.PrivateKey, WebhookSecret: w.secret}); err != nil {
				t.Fatal(err)
			}
			var events []vcsapi.Event
			var logs bytes.Buffer
			w.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
			w.deps.Providers = map[string]port.Webhooks{"GITHUB_ENTERPRISE": capturedGitHub{Provider: github.New(github.Options{}), events: &events}}
			w.svc = intake.New(w.deps)
			r := chi.NewRouter()
			r.Post(adapter.WebhookPattern, adapter.Webhook(w.svc).ServeHTTP)
			srv := httptest.NewServer(r)
			t.Cleanup(srv.Close)
			url := srv.URL + "/webhooks/vcs/github_enterprise/" + w.id.String()
			sender, err := f.Sender(app.ID)
			if err != nil {
				t.Fatal(err)
			}
			body, err := githubfake.Payload("push", "")
			if err != nil {
				t.Fatal(err)
			}
			const id = "72d3162e-7c72-11e3-81e9-123456789abc"
			if status, err := sender.Send(t.Context(), url, id, "push", body); err != nil || status != 202 {
				t.Fatalf("signed push = %d, %v", status, err)
			}
			if status, err := sender.Redeliver(t.Context(), 0); err != nil || status != 200 {
				t.Fatalf("redelivery = %d, %v", status, err)
			}
			d := w.delivery(t, id)
			m := hmac.New(sha256.New, w.secret)
			_, _ = m.Write(body)
			signature := "sha256=" + hex.EncodeToString(m.Sum(nil))
			if d.Account != w.f.acct || d.Integration != w.id || d.Provider != "GITHUB_ENTERPRISE" ||
				d.Event != "push" || d.Repository != "7" || string(d.Signature) != signature ||
				d.Headers.Get("X-Hub-Signature-256") != signature || !bytes.Equal(d.Body, body) {
				t.Fatal("stored delivery changed verified bytes or account identity")
			}
			var ordering, stored string
			if err := w.f.db.QueryRow(t.Context(), `SELECT ordering_key FROM jobs WHERE account_id=$1 AND kind='vcs.process_delivery'`, w.f.acct).
				Scan(&ordering); err != nil ||
				ordering != w.id.String()+"/7" {
				t.Fatalf("repository ordering = %q, %v", ordering, err)
			}
			if err := w.f.db.QueryRow(t.Context(), `SELECT row_to_json(d)::text FROM vcs_deliveries d WHERE id=$1`, d.ID).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(stored, string(w.secret)) || strings.Contains(logs.String(), string(w.secret)) {
				t.Fatal("webhook secret leaked into storage or logs")
			}
			job := w.claim(t, w.queue, "github-parser")
			if err := w.svc.Processed(t.Context(), job); err != nil {
				t.Fatal(err)
			}
			if len(events) != 1 || events[0].Type != "push" || events[0].Repository.ID != "7" || events[0].Branch != "main" || events[0].Head != githubfake.AfterSHA ||
				!events[0].Forced {
				t.Fatalf("durable normalization = %+v", events)
			}
			if w.delivery(t, id).State != intake.Dispatched {
				t.Fatal("parsed delivery did not commit dispatched state")
			}
			filtered, err := githubfake.Payload("repository", "created")
			if err != nil {
				t.Fatal(err)
			}
			if status, err := sender.Send(t.Context(), url, "filtered", "repository", filtered); err != nil || status != 200 {
				t.Fatalf("filtered action = %d, %v", status, err)
			}
			var count int
			if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM vcs_deliveries WHERE account_id=$1`, w.f.acct).Scan(&count); err != nil || count != 1 {
				t.Fatalf("deduplicated/filtered deliveries = %d, %v", count, err)
			}
			if f.Requests() != 0 || len(sender.Attempts()) != 3 {
				t.Fatal("intake made an inline provider call or sender retried")
			}
		})
	}
}
