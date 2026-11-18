package pg_test

import (
	"bytes"
	"crypto/fips140"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/azuredevopsfake"
	azureprovider "github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/azuredevops"
	adapter "github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/http"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type azureParsed struct {
	port.Webhooks
	events *[]vcsapi.Event
}

func (p azureParsed) Parse(headers http.Header, body []byte) ([]vcsapi.Event, error) {
	events, err := p.Webhooks.Parse(headers, body)
	*p.events = slices.Clone(events)
	return events, err
}

func azureWebhookWorld(t *testing.T, password []byte) (*webhookWorld, *httptest.Server, *[]vcsapi.Event) {
	t.Helper()
	w := newWebhookWorld(t)
	in := integration.Integration{
		Account:   w.f.acct,
		ID:        kernel.NewULID(),
		Provider:  integration.AzureDevOps,
		Slug:      "azure-hook",
		Name:      "Azure hook",
		Space:     w.f.acct.ULID,
		Labels:    []string{},
		Checks:    integration.Checks{Mode: "INDIVIDUAL"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Version:   1,
	}
	if err := w.f.store.Create(t.Context(), in, integration.Secrets{Credential: []byte("unrelated-source-personal-access-token"), WebhookSecret: password}); err != nil {
		t.Fatal(err)
	}
	var parsed []vcsapi.Event
	w.id, w.secret = in.ID, password
	w.deps.Providers = map[string]port.Webhooks{integration.AzureDevOps: azureParsed{Webhooks: azureprovider.New(azureprovider.Options{}), events: &parsed}}
	w.svc = intake.New(w.deps)
	r := chi.NewRouter()
	r.Method(http.MethodPost, adapter.WebhookPattern, adapter.Webhook(w.svc))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return w, srv, &parsed
}

func azureDigest(password, body []byte) []byte {
	mac := hmac.New(sha256.New, password)
	_, _ = mac.Write(body)
	return []byte(hex.EncodeToString(mac.Sum(nil)))
}

func TestAccAzureWebhookIntake(t *testing.T) {
	password := []byte("azure-current-password-at-least-32-bytes")
	w, srv, parsed := azureWebhookWorld(t, password)
	fake := azuredevopsfake.New()
	sender := azuredevopsfake.NewWebhookSender(azuredevopsfake.WebhookSenderOptions{Client: srv.Client()})
	endpoint := srv.URL + "/webhooks/vcs/azure_devops/" + w.id.String()
	request := azuredevopsfake.WebhookPullRequest{
		ID:            42,
		SourceBranch:  "feature",
		TargetBranch:  "main",
		Head:          strings.Repeat("2", 40),
		Fork:          true,
		SourceProject: "Fork",
		Votes:         []int{5},
		Draft:         true,
	}
	for _, tc := range []struct {
		name, event, kind string
		count             int
		payload           func(string) (azuredevopsfake.WebhookDelivery, error)
	}{
		{"push", "git.push", "push", 2, func(id string) (azuredevopsfake.WebhookDelivery, error) {
			return fake.PushWebhook(id, "Platform", "infra", []azuredevopsfake.WebhookRefUpdate{{Name: "refs/heads/main", Before: strings.Repeat("1", 40), Head: strings.Repeat("2", 40)}, {Name: "refs/tags/v1", Before: strings.Repeat("0", 40), Head: strings.Repeat("2", 40)}})
		}},
		{"request", "git.pullrequest.created", "pull_request", 1, func(id string) (azuredevopsfake.WebhookDelivery, error) {
			return fake.PullRequestWebhook(id, "git.pullrequest.created", "Platform", "infra", request)
		}},
		{"comment", "ms.vss-code.git-pullrequest-comment-event", "comment", 1, func(id string) (azuredevopsfake.WebhookDelivery, error) {
			return fake.CommentWebhook(id, "Platform", "infra", request, "/spacelift plan")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := kernel.NewULID().UUID()
			delivery, err := tc.payload(id)
			if err != nil {
				t.Fatal(err)
			}
			status, err := sender.Send(t.Context(), endpoint, "", password, delivery)
			if err != nil || status != 202 {
				t.Fatal("Azure delivery refused", status, err)
			}
			stored := w.delivery(t, id)
			if stored.Provider != integration.AzureDevOps || stored.Event != tc.event || stored.Repository == "" || !bytes.Equal(stored.Body, delivery.Body) ||
				!hmac.Equal(stored.Signature, azureDigest(password, stored.Body)) {
				t.Fatal("Azure body identity/signature changed")
			}
			for key := range stored.Headers {
				if strings.EqualFold(key, "Authorization") {
					t.Fatal("Azure Authorization retained")
				}
			}
			status, err = sender.Send(t.Context(), endpoint, "", password, delivery)
			if err != nil || status != 200 {
				t.Fatal("Azure duplicate not deduplicated", status, err)
			}
			job := w.claim(t, w.queue, "azure-test")
			if err := w.svc.Processed(t.Context(), job); err != nil {
				t.Fatal(err)
			}
			if len(*parsed) != tc.count || (*parsed)[0].Type != tc.kind || (*parsed)[0].Repository.ID != stored.Repository {
				t.Fatal("real processor lost Azure normalization")
			}
			if pr := (*parsed)[0].PullRequest; pr != nil && (pr.Number != 42 || !pr.Fork || pr.HeadOwner == "" || !pr.Approved || pr.Mergeable || pr.Undiverged) {
				t.Fatalf("unsafe Azure request facts %+v", pr)
			}
			if w.delivery(t, id).State != intake.Dispatched {
				t.Fatal("Azure processing did not settle")
			}
		})
	}
	for _, needle := range []string{string(password), "Basic " + base64.StdEncoding.EncodeToString(append([]byte(":"), password...))} {
		var count int
		if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM vcs_deliveries d WHERE account_id=$1 AND strpos(row_to_json(d)::text,$2)>0`, w.f.acct, needle).
			Scan(&count); err != nil ||
			count != 0 {
			t.Fatal("Azure credential reached retained row", count, err)
		}
	}
}

func TestAccAzureWebhookFiltersAndInvalidEnvelopes(t *testing.T) {
	password := []byte("azure-filter-password-at-least-32-bytes")
	w, srv, _ := azureWebhookWorld(t, password)
	sender := azuredevopsfake.NewWebhookSender(azuredevopsfake.WebhookSenderOptions{Client: srv.Client()})
	endpoint := srv.URL + "/webhooks/vcs/azure_devops/" + w.id.String()
	provider := azureprovider.New(azureprovider.Options{})
	sentinel := provider.Event(nil, []byte(`not JSON`))
	for _, event := range []string{"build.complete", sentinel} {
		body, err := json.Marshal(map[string]any{"eventType": event, "resource": 42})
		if err != nil {
			t.Fatal(err)
		}
		if status, err := sender.Send(t.Context(), endpoint, "", password, azuredevopsfake.WebhookDelivery{Body: body}); err != nil || status != 200 {
			t.Fatal("unsupported typed envelope retained", status, err)
		}
	}
	for _, credentials := range []struct {
		user     string
		password []byte
	}{{"pat", password}, {"", []byte("wrong")}, {"", nil}} {
		if status, err := sender.Send(
			t.Context(),
			endpoint,
			credentials.user,
			credentials.password,
			azuredevopsfake.WebhookDelivery{Body: []byte(`not JSON`)},
		); err != nil ||
			status != 401 {
			t.Fatal("bad Basic did not precede malformed routing", status, err)
		}
	}
	var count int
	if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM vcs_deliveries WHERE account_id=$1`, w.f.acct).Scan(&count); err != nil || count != 0 {
		t.Fatal("refused/filtered Azure envelope retained", count, err)
	}
	for index, body := range [][]byte{[]byte(`not JSON body-marker-539`), []byte(`{"eventType":null}`), []byte(`{"eventType":"git.push","resource":17}`)} {
		delivery := azuredevopsfake.WebhookDelivery{Body: body}
		if status, err := sender.Send(t.Context(), endpoint, "", password, delivery); err != nil || status != 202 {
			t.Fatal("invalid authenticated envelope silently filtered", status, err)
		}
		hash := sha256.Sum256(body)
		id := hex.EncodeToString(hash[:])
		stored := w.delivery(t, id)
		wantEvent := sentinel
		if index == 2 {
			wantEvent = "git.push"
		}
		if stored.Event != wantEvent || !bytes.Equal(stored.Body, body) || !hmac.Equal(stored.Signature, azureDigest(password, body)) {
			t.Fatal("invalid Azure envelope lost safe routing/body digest")
		}
		if _, err := w.f.db.Exec(t.Context(), `UPDATE jobs SET max_attempts=1 WHERE account_id=$1 AND kind=$2 AND state='pending'`, w.f.acct, intake.Process.Name); err != nil {
			t.Fatal(err)
		}
		job := w.claim(t, w.queue, "azure-parser")
		err := w.svc.Processed(t.Context(), job)
		if err == nil {
			t.Fatal("invalid Azure payload parsed")
		}
		if dead, fail := w.queue.Fail(t.Context(), job, err); fail != nil || !dead {
			t.Fatal("Azure parse job did not settle failed", dead, fail)
		}
		stored = w.delivery(t, id)
		if stored.State != intake.Failed || stored.LastError != "vcs: webhook payload could not be parsed" {
			t.Fatal("Azure parse failure was not safe and durable")
		}
	}
}

func TestAccAzurePreviousPasswordWindow(t *testing.T) {
	previous := []byte("azure-previous-password-at-least-32-bytes")
	w, _, _ := azureWebhookWorld(t, previous)
	clock := time.Now().UTC().Truncate(time.Microsecond)
	expires := clock.Add(time.Hour)
	deps := w.f.deps
	deps.Now = func() time.Time { return clock }
	deps.SecretOverlap = time.Hour
	svc := integration.New(deps)
	if _, err := w.f.db.Exec(t.Context(), `UPDATE vcs_integrations SET credentials_sealed=$2 WHERE id=$1`, w.id, []byte("unreadable-source-credential")); err != nil {
		t.Fatal(err)
	}
	rotation, err := svc.RotateWebhookSecret(t.Context(), w.f.admin, w.id.String(), integration.AzureDevOps, "")
	if err != nil {
		t.Fatal(err)
	}
	current := []byte(svc.RotationDetails(t.Context(), rotation).WebhookSecret)
	w.deps.Now = func() time.Time { return clock }
	w.svc = intake.New(w.deps)
	for _, tc := range []struct {
		id       string
		password []byte
		at       time.Time
		allowed  bool
	}{{"current", current, clock, true}, {"previous", previous, expires.Add(-time.Nanosecond), true}, {"expired", previous, expires, false}, {"current-after", current, expires, true}} {
		clock = tc.at
		delivery, err := azuredevopsfake.New().
			PushWebhook(tc.id, "Platform", "infra", []azuredevopsfake.WebhookRefUpdate{{Name: "refs/heads/main", Before: strings.Repeat("1", 40), Head: strings.Repeat("2", 40)}})
		if err != nil {
			t.Fatal(err)
		}
		request := http.Request{Header: http.Header{}}
		request.SetBasicAuth("", string(tc.password))
		status, err := w.svc.Receive(t.Context(), "azure_devops", w.id.String(), request.Header, delivery.Body)
		if !tc.allowed {
			if !errors.Is(err, intake.ErrVerification) {
				t.Fatal("expired password accepted", err)
			}
			continue
		}
		if err != nil || status != 202 || !hmac.Equal(w.delivery(t, tc.id).Signature, azureDigest(tc.password, delivery.Body)) {
			t.Fatal("allowed password did not produce its own exact digest", status, err)
		}
	}
	var count int
	if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM vcs_deliveries WHERE account_id=$1`, w.f.acct).Scan(&count); err != nil || count != 3 {
		t.Fatal("expired password retained work", count, err)
	}
}

func TestAccAzureShortWebhookPasswordPolicy(t *testing.T) {
	password := []byte("hook")
	w, srv, _ := azureWebhookWorld(t, password)
	delivery, err := azuredevopsfake.New().
		PushWebhook("short", "Platform", "infra", []azuredevopsfake.WebhookRefUpdate{{Name: "refs/heads/main", Before: strings.Repeat("1", 40), Head: strings.Repeat("2", 40)}})
	if err != nil {
		t.Fatal(err)
	}
	status, err := azuredevopsfake.NewWebhookSender(azuredevopsfake.WebhookSenderOptions{Client: srv.Client()}).
		Send(t.Context(), srv.URL+"/webhooks/vcs/azure_devops/"+w.id.String(), "", password, delivery)
	want := 202
	if fips140.Enforced() {
		want = 401
	}
	if err != nil || status != want {
		t.Fatal("short Azure password did not follow crypto policy", status, err)
	}
	if want == 202 && !hmac.Equal(w.delivery(t, "short").Signature, azureDigest(password, delivery.Body)) {
		t.Fatal("ordinary short Azure password HMAC changed")
	}
}
