package pg_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketcloudfake"
	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketdcfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/bitbucketcloud"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/bitbucketdc"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type bitbucketCapture struct {
	port.Webhooks
	events *[]vcsapi.Event
}

func (p bitbucketCapture) Parse(h http.Header, b []byte) ([]vcsapi.Event, error) {
	events, err := p.Webhooks.Parse(h, b)
	*p.events = events
	return events, err
}

type bitbucketCase struct {
	provider, route, event, idHeader, repository string
	adapter                                      port.Webhooks
	payload                                      func(string) ([]byte, error)
}

func bitbucketCases() []bitbucketCase {
	return []bitbucketCase{
		{
			"BITBUCKET_CLOUD",
			"bitbucket_cloud",
			"repo:push",
			"X-Request-UUID",
			bitbucketcloudfake.RepositoryUUID,
			bitbucketcloud.New(bitbucketcloud.Options{APIHost: ":invalid"}),
			bitbucketcloudfake.Payload,
		},
		{"BITBUCKET_DATACENTER", "bitbucket_datacenter", "repo:refs_changed", "X-Request-Id", "7", bitbucketdc.New(bitbucketdc.Options{}), bitbucketdcfake.Payload},
	}
}

func bitbucketHeaders(tc bitbucketCase, id, event string, b, secret []byte) http.Header {
	m := hmac.New(sha256.New, secret)
	_, _ = m.Write(b)
	h := http.Header{}
	h.Set("X-Hub-Signature", "sha256="+hex.EncodeToString(m.Sum(nil)))
	h.Set(tc.idHeader, id)
	h.Set("X-Event-Key", event)
	h.Set("Authorization", "Bearer private-provider-token")
	h.Set("X-Gitlab-Token", "private-provider-token")
	return h
}

func newBitbucketWorld(t *testing.T, tc bitbucketCase, events *[]vcsapi.Event) *webhookWorld {
	t.Helper()
	w := newWebhookWorld(t)
	w.secret = []byte("bitbucket-current-hook-secret-32-bytes")
	in, err := w.f.store.Get(t.Context(), w.f.acct, w.id, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.f.store.Write(t.Context(), in, &integration.Secrets{Credential: []byte("private-provider-token"), WebhookSecret: w.secret}); err != nil {
		t.Fatal(err)
	}
	if _, err = w.f.db.Exec(t.Context(), `UPDATE vcs_integrations SET provider=$2 WHERE id=$1`, w.id, tc.provider); err != nil {
		t.Fatal(err)
	}
	w.deps.Providers = map[string]port.Webhooks{tc.provider: bitbucketCapture{Webhooks: tc.adapter, events: events}}
	w.svc = intake.New(w.deps)
	return w
}

func TestAccBitbucketWebhookProviders(t *testing.T) {
	for _, tc := range bitbucketCases() {
		t.Run(tc.provider, func(t *testing.T) {
			var parsed []vcsapi.Event
			w := newBitbucketWorld(t, tc, &parsed)
			b, err := tc.payload(tc.event)
			if err != nil {
				t.Fatal(err)
			}
			// A single delivery can carry multiple branch or tag changes.
			var d map[string]json.RawMessage
			if err = json.Unmarshal(b, &d); err != nil {
				t.Fatal(err)
			}
			if tc.provider == "BITBUCKET_CLOUD" {
				var push map[string][]json.RawMessage
				if err = json.Unmarshal(d["push"], &push); err != nil {
					t.Fatal(err)
				}
				push["changes"] = append(push["changes"], push["changes"][0])
				d["push"], err = json.Marshal(push)
			} else {
				var changes []json.RawMessage
				if err = json.Unmarshal(d["changes"], &changes); err != nil {
					t.Fatal(err)
				}
				d["changes"], err = json.Marshal(append(changes, changes[0]))
			}
			if err != nil {
				t.Fatal(err)
			}
			b, err = json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			h := bitbucketHeaders(tc, "push", tc.event, b, w.secret)
			for i, want := range []int{202, 200} {
				code, err := w.svc.Receive(t.Context(), tc.route, w.id.String(), h, b)
				if err != nil || code != want {
					t.Fatalf("receive%d=%d %v", i, code, err)
				}
			}
			stored := w.delivery(t, "push")
			if !bytes.Equal(stored.Signature, []byte(h.Get("X-Hub-Signature"))) || !bytes.Equal(stored.Body, b) || stored.Account != w.f.acct || stored.Integration != w.id ||
				stored.Repository != tc.repository ||
				stored.Headers.Get("Authorization") != "" ||
				stored.Headers.Get("X-Gitlab-Token") != "" {
				t.Fatal("wrong owner/body/identity or unsafe stored signature/headers")
			}
			job := w.claim(t, w.queue, "bitbucket-worker")
			var ordering string
			if err = w.f.db.QueryRow(t.Context(), `SELECT ordering_key FROM jobs WHERE id=$1`, job.ID).Scan(&ordering); err != nil || ordering != w.id.String()+"/"+tc.repository {
				t.Fatalf("ordering=%q err=%v", ordering, err)
			}
			if err = w.svc.Processed(t.Context(), job); err != nil {
				t.Fatal(err)
			}
			if len(parsed) != 2 || parsed[0].Type != "push" || parsed[0].Head != bitbucketcloudfake.AfterSHA || w.delivery(t, "push").State != intake.Dispatched {
				t.Fatalf("actual parser did not dispatch multiple refs: %+v", parsed)
			}
			var count int
			if err = w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE account_id=$1 AND kind=$2`, w.f.acct, intake.Process.Name).
				Scan(&count); err != nil ||
				count != 1 {
				t.Fatal("duplicate created another job", count, err)
			}
		})
	}
}

func TestAccBitbucketWebhookPreviousAndFailure(t *testing.T) {
	for _, tc := range bitbucketCases() {
		t.Run(tc.provider, func(t *testing.T) {
			var parsed []vcsapi.Event
			w := newBitbucketWorld(t, tc, &parsed)
			previous := []byte("bitbucket-previous-hook-secret-32-bytes")
			now := time.Now()
			until := now.Add(time.Hour)
			sealed, err := w.keys.SealColumn(t.Context(), pg.SealedColumns()[2], []string{w.f.acct.UUID(), w.id.UUID()}, previous)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = w.f.db.Exec(
				t.Context(),
				`UPDATE vcs_integrations SET previous_webhook_secret_sealed=$2,previous_secret_valid_until=$3,credentials_sealed='broken' WHERE id=$1`,
				w.id,
				sealed,
				until,
			); err != nil {
				t.Fatal(err)
			}
			w.deps.Now = func() time.Time { return now }
			w.svc = intake.New(w.deps)
			body, err := tc.payload(tc.event)
			if err != nil {
				t.Fatal(err)
			}
			h := bitbucketHeaders(tc, "previous", tc.event, body, previous)
			code, err := w.svc.Receive(t.Context(), tc.route, w.id.String(), h, body)
			if err != nil || code != 202 {
				t.Fatalf("active previous=%d %v", code, err)
			}
			if got := w.delivery(t, "previous"); !bytes.Equal(got.Signature, []byte(h.Get("X-Hub-Signature"))) {
				t.Fatal("previous header not retained exactly")
			}
			if err = w.svc.Processed(t.Context(), w.claim(t, w.queue, "previous")); err != nil {
				t.Fatal(err)
			}
			if len(parsed) != 1 || w.delivery(t, "previous").State != intake.Dispatched {
				t.Fatal("previous-secret delivery did not parse and dispatch")
			}
			w.deps.Now = func() time.Time { return until }
			w.svc = intake.New(w.deps)
			for _, header := range []http.Header{h, bitbucketHeaders(tc, "wrong", tc.event, body, []byte("unrelated-hook-secret-32-bytes-long")), {}} {
				code, err = w.svc.Receive(t.Context(), tc.route, w.id.String(), header, body)
				if !errors.Is(err, intake.ErrVerification) || code != 0 {
					t.Fatalf("invalid/expired signature=%d %v", code, err)
				}
			}
			// Verification precedes filtering even for unsigned ping/unknown events.
			filtered := bitbucketHeaders(tc, "filtered", "diagnostics:ping", []byte(`{}`), w.secret)
			if code, err = w.svc.Receive(t.Context(), tc.route, w.id.String(), filtered, []byte(`{}`)); err != nil || code != 200 {
				t.Fatalf("filtered=%d %v", code, err)
			}
			malformed := []byte(`{"private-payload":"never echoed"`)
			bad := bitbucketHeaders(tc, "malformed", tc.event, malformed, w.secret)
			if code, err = w.svc.Receive(t.Context(), tc.route, w.id.String(), bad, malformed); err != nil || code != 202 {
				t.Fatalf("malformed intake=%d %v", code, err)
			}
			if _, err = w.f.db.Exec(t.Context(), `UPDATE jobs SET max_attempts=1 WHERE account_id=$1 AND kind=$2`, w.f.acct, intake.Process.Name); err != nil {
				t.Fatal(err)
			}
			malformedJob := w.claim(t, w.queue, "malformed")
			err = w.svc.Processed(t.Context(), malformedJob)
			if err == nil || strings.Contains(err.Error(), "private-payload") {
				t.Fatalf("unsafe/missing processor refusal: %v", err)
			}
			if dead, failure := w.queue.Fail(t.Context(), malformedJob, err); failure != nil || !dead {
				t.Fatalf("queue settlement dead=%t err=%v", dead, failure)
			}
			failed := w.delivery(t, "malformed")
			if failed.State != intake.Failed || failed.LastError == "" || strings.Contains(failed.LastError, "private-payload") {
				t.Fatalf("unsafe/non-durable failure state=%s error=%s", failed.State, failed.LastError)
			}
			var count int
			if err = w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM vcs_deliveries WHERE account_id=$1`, w.f.acct).Scan(&count); err != nil || count != 2 {
				t.Fatalf("filtered/invalid delivery persisted: %d %v", count, err)
			}
		})
	}
}
