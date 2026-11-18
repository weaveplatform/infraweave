package pg_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/blobs"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/fs"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	adapter "github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/http"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/webhooktest"
)

type capturedWebhook struct {
	webhooktest.Provider
	body *[]byte
}

func (p capturedWebhook) Parse(h http.Header, b []byte) ([]vcsapi.Event, error) {
	*p.body = bytes.Clone(b)
	return p.Provider.Parse(h, b)
}

type webhookWorld struct {
	f       *fixture
	keys    *crypt.Keyring
	store   *pg.Deliveries
	objects *blobs.Objects
	queue   *queue.Queue
	svc     *intake.Service
	deps    intake.Deps
	id      kernel.ULID
	secret  []byte
	body    []byte
	parsed  int
}

func newWebhookWorld(t *testing.T) *webhookWorld {
	t.Helper()
	return newWebhookWorldOn(t, newFixture(t))
}

func newWebhookWorldOn(t *testing.T, f *fixture) *webhookWorld {
	t.Helper()
	k, err := kek.NewLocal(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypt.Load(t.Context(), f.db, k)
	if err != nil {
		t.Fatal(err)
	}
	in := integration.Integration{
		Account: f.acct, ID: kernel.NewULID(), Provider: "GITHUB_ENTERPRISE", Slug: "hook", Name: "Hook", Space: f.acct.ULID,
		Labels: []string{}, Checks: integration.Checks{Mode: "INDIVIDUAL"}, CreatedAt: time.Now(), UpdatedAt: time.Now(), Version: 1,
	}
	secret := []byte("webhook-secret-535")
	if err := f.store.Create(t.Context(), in, integration.Secrets{Credential: []byte("not-a-webhook-credential"), WebhookSecret: secret}); err != nil {
		t.Fatal(err)
	}
	bucket, err := fs.New(objectstore.FSConfig{Root: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := bucket.Close(); err != nil {
			t.Error(err)
		}
	})
	objects, err := objectstore.New(objectstore.Defaults(), func(string) (objectstore.Bucket, error) { return bucket, nil })
	if err != nil {
		t.Fatal(err)
	}
	q := queue.New(f.db)
	b := blobs.New(f.db, q, objects, keys, objectstore.Defaults())
	w := &webhookWorld{f: f, keys: keys, store: pg.NewDeliveries(f.db, keys), objects: b, queue: q, id: in.ID, secret: secret}
	w.deps = intake.Deps{DB: f.db, Store: w.store, Objects: b, Jobs: q, Providers: map[string]port.Webhooks{
		"GITHUB_ENTERPRISE": capturedWebhook{Provider: webhooktest.Provider{Parsed: &w.parsed}, body: &w.body},
	}}
	w.svc = intake.New(w.deps)
	t.Cleanup(func() { _, _ = f.db.Exec(context.Background(), `DELETE FROM jobs WHERE account_id=$1`, f.acct) })
	return w
}

func (w *webhookWorld) headers(id string, b []byte) http.Header {
	return http.Header{
		"X-Github-Event": {"push"}, "X-Github-Delivery": {id},
		"X-Hub-Signature-256": {"sha256=" + string(webhooktest.Sign(b, w.secret))},
		"Authorization":       {"Bearer webhook-secret-535"}, "X-Gitlab-Token": {"webhook-secret-535"},
	}
}

func (w *webhookWorld) receive(t *testing.T, id string, b []byte) int {
	t.Helper()
	code, err := w.svc.Receive(t.Context(), "github_enterprise", w.id.String(), w.headers(id, b), b)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func (w *webhookWorld) delivery(t *testing.T, id string) intake.Delivery {
	t.Helper()
	var key kernel.ULID
	if err := w.f.db.QueryRow(t.Context(), `SELECT id FROM vcs_deliveries WHERE account_id=$1 AND delivery_id=$2`, w.f.acct, id).Scan(&key); err != nil {
		t.Fatal(err)
	}
	d, err := w.store.Read(t.Context(), w.f.acct, key)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func (w *webhookWorld) claim(t *testing.T, q *queue.Queue, owner string) queue.Job {
	t.Helper()
	jobs, err := q.Claim(t.Context(), queue.VCSInbox, []string{intake.Process.Name}, 1, time.Minute, owner)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim=%v,%v", jobs, err)
	}
	return jobs[0]
}

func webhookBody(size int) []byte {
	const prefix = `{"type":"push","padding":"`
	const suffix = `"}`
	return []byte(prefix + strings.Repeat("p", size-len(prefix)-len(suffix)) + suffix)
}

// TestAccDurableWebhookIntake commits real delivery/job/blob rows, verifies the
// stored digest, and reads exact bytes through the real processor and fs store.
func TestAccDurableWebhookIntake(t *testing.T) {
	for _, size := range []int{128, intake.InlineBody, intake.InlineBody + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			w := newWebhookWorld(t)
			body := webhookBody(size)
			if code := w.receive(t, "one", body); code != 202 {
				t.Fatal(code)
			}
			d := w.delivery(t, "one")
			if !bytes.Equal(d.Signature, webhooktest.Sign(body, w.secret)) || d.Headers.Get("Authorization") != "" || d.Headers.Get("X-Gitlab-Token") != "" {
				t.Fatal("unsafe retained verification data")
			}
			if (d.BodyKey == "") != (size <= intake.InlineBody) || size <= intake.InlineBody && !bytes.Equal(d.Body, body) {
				t.Fatal("wrong body storage boundary")
			}
			if code := w.receive(t, "one", webhookBody(size+1)); code != 200 {
				t.Fatal(code)
			}
			var jobs int
			var key, ordering string
			var account kernel.ULID
			if err := w.f.db.QueryRow(t.Context(), `SELECT count(*),min(unique_key),min(ordering_key),min(account_id::text)::uuid
				FROM jobs WHERE kind=$1 AND account_id=$2`, intake.Process.Name, w.f.acct).Scan(&jobs, &key, &ordering, &account); err != nil {
				t.Fatal(err)
			}
			if jobs != 1 || key != d.ID.String() || ordering != w.id.String()+"/repository-7" || account != w.f.acct.ULID {
				t.Fatal("wrong durable queue identity")
			}
			j := w.claim(t, w.queue, "worker")
			if err := w.svc.Processed(t.Context(), j); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(w.body, body) || w.parsed != 1 || w.delivery(t, "one").State != intake.Dispatched {
				t.Fatal("processor did not read original verified bytes")
			}
			var state string
			if err := w.f.db.QueryRow(t.Context(), `SELECT state FROM jobs WHERE id=$1`, j.ID).Scan(&state); err != nil || state != "succeeded" {
				t.Fatal(state, err)
			}
			if d.BodyKey != "" {
				r, err := w.objects.Open(t.Context(), objectstore.Inbox, d.BodyKey)
				if err != nil {
					t.Fatal("duplicate removed winner", err)
				}
				got, err := io.ReadAll(r)
				r.Close()
				if err != nil || !bytes.Equal(got, body) {
					t.Fatal("winner body changed")
				}
			}
		})
	}
}

func TestAccWebhookSecretLookup(t *testing.T) {
	w := newWebhookWorld(t)
	ctx := t.Context()
	until := time.Now().Add(time.Hour)
	previous := []byte("previous-secret-535")
	col := pg.SealedColumns()[2]
	sealed, err := w.keys.SealColumn(ctx, col, []string{w.f.acct.UUID(), w.id.UUID()}, previous)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.f.db.Exec(
		ctx,
		`UPDATE vcs_integrations SET credentials_sealed='broken',previous_webhook_secret_sealed=$2,previous_secret_valid_until=$3 WHERE id=$1`,
		w.id,
		sealed,
		until,
	); err != nil {
		t.Fatal(err)
	}
	in, err := w.store.Integration(ctx, w.id, time.Now())
	if err != nil || len(in.Secrets(time.Now())) != 2 || !bytes.Equal(in.Previous, previous) || in.Account != w.f.acct {
		t.Fatal("lookup opened credential or lost owner/window", err)
	}
	if _, err := w.f.db.Exec(ctx, `UPDATE vcs_integrations SET previous_webhook_secret_sealed='broken' WHERE id=$1`, w.id); err != nil {
		t.Fatal(err)
	}
	if _, err := w.store.Integration(ctx, w.id, until); err != nil {
		t.Fatal("expired previous ciphertext blocked current secret", err)
	}
	if _, err := w.store.Integration(ctx, kernel.NewULID(), time.Now()); !errors.Is(err, intake.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := w.store.Read(ctx, kernel.NewAccountID(), kernel.NewULID()); !errors.Is(err, intake.ErrNotFound) {
		t.Fatal("account-scoped read", err)
	}
	// Ciphertext copied to another primary key must fail its column binding.
	if _, err := w.f.db.Exec(ctx, `UPDATE vcs_integrations SET id=$2 WHERE id=$1`, w.id, kernel.NewULID()); err != nil {
		t.Fatal(err)
	}
	var moved kernel.ULID
	if err := w.f.db.QueryRow(ctx, `SELECT id FROM vcs_integrations WHERE account_id=$1`, w.f.acct).Scan(&moved); err != nil {
		t.Fatal(err)
	}
	if _, err := w.store.Integration(ctx, moved, until); err == nil {
		t.Fatal("copied secret opened under another row")
	}
}

type rejectCommit struct{ pgx.Tx }

func (t rejectCommit) Commit(ctx context.Context) error { _ = t.Rollback(ctx); return errDown }

type rejectBegin struct{ pool *pgxpool.Pool }

func (b rejectBegin) BeginTx(ctx context.Context, o pgx.TxOptions) (pgx.Tx, error) {
	tr, err := b.pool.BeginTx(ctx, o)
	return rejectCommit{tr}, err
}

func TestAccWebhookRollbackAndDeletion(t *testing.T) {
	w := newWebhookWorld(t)
	ctx := t.Context()
	deps := w.deps
	deps.DB = rejectBegin{w.f.db}
	svc := intake.New(deps)
	body := webhookBody(128)
	if _, err := svc.Receive(ctx, "github_enterprise", w.id.String(), w.headers("rollback", body), body); err == nil {
		t.Fatal("commit failure acknowledged")
	}
	var deliveries, jobs int
	if err := w.f.db.QueryRow(ctx, `SELECT (SELECT count(*) FROM vcs_deliveries WHERE account_id=$1),
		(SELECT count(*) FROM jobs WHERE account_id=$1 AND kind=$2)`, w.f.acct, intake.Process.Name).Scan(&deliveries, &jobs); err != nil || deliveries != 0 || jobs != 0 {
		t.Fatal("rollback left delivery/job", deliveries, jobs, err)
	}
	// The integration lock prevents a delivery from appearing after deletion.
	in, err := w.store.Integration(ctx, w.id, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.f.store.Delete(ctx, w.f.acct, w.id); err != nil {
		t.Fatal(err)
	}
	err = tx.Do(ctx, w.f.db, tx.Options{}, func(ctx context.Context) error {
		_, err := w.store.Insert(ctx, intake.Delivery{Account: in.Account, ID: kernel.NewULID(), Integration: in.ID, Provider: in.Provider})
		return err
	})
	if !errors.Is(err, intake.ErrNotFound) {
		t.Fatal("deleted integration accepted", err)
	}
}

// TestAccWebhookReplicaRecovery acknowledges on A, stops it before processing,
// and recovers its expired queue lease through an independent pool and worker B.
func TestAccWebhookReplicaRecovery(t *testing.T) {
	w := newWebhookWorld(t)
	r := chi.NewRouter()
	r.Method("POST", adapter.WebhookPattern, adapter.Webhook(w.svc))
	a := httptest.NewServer(r)
	body := webhookBody(intake.InlineBody + 1)
	req, err := http.NewRequestWithContext(t.Context(), "POST", a.URL+"/webhooks/vcs/github_enterprise/"+w.id.String(), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header = w.headers("recovery", body)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 202 {
		t.Fatal(resp.StatusCode)
	}
	old := w.claim(t, w.queue, "A")
	a.Close()
	pool, err := pgxpool.NewWithConfig(t.Context(), w.f.db.Config().Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	q := queue.New(pool)
	if _, err := pool.Exec(t.Context(), `UPDATE jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, old.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := q.Recover(t.Context()); err != nil || n < 1 {
		t.Fatal("lease recovery", n, err)
	}
	j := w.claim(t, q, "B")
	deps := w.deps
	deps.DB = pool
	deps.Store = pg.NewDeliveries(pool, w.keys)
	deps.Jobs = q
	other := intake.New(deps)
	if err := other.Processed(t.Context(), j); err != nil || w.parsed != 1 || w.delivery(t, "recovery").State != intake.Dispatched {
		t.Fatal("B did not process once", err, w.parsed)
	}
	if jobs, err := q.Claim(t.Context(), queue.VCSInbox, []string{intake.Process.Name}, 1, time.Minute, "B"); err != nil || len(jobs) != 0 {
		t.Fatal("job remained claimable", jobs, err)
	}
}

func TestAccWebhookFencedAttempts(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			w := newWebhookWorld(t)
			body := webhookBody(128)
			w.receive(t, "fenced", body)
			old := w.claim(t, w.queue, "old")
			if _, err := w.f.db.Exec(t.Context(), `UPDATE jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, old.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := w.queue.Recover(t.Context()); err != nil {
				t.Fatal(err)
			}
			fresh := w.claim(t, w.queue, "new")
			deps := w.deps
			if fail {
				deps.Providers = map[string]port.Webhooks{"GITHUB_ENTERPRISE": webhooktest.Provider{ParseError: errors.New("sensitive payload")}}
				old.Attempt = old.MaxAttempts
			}
			if err := intake.New(deps).Processed(t.Context(), old); !errors.Is(err, queue.ErrLeaseLost) {
				t.Fatal("stale attempt not fenced", err)
			}
			d := w.delivery(t, "fenced")
			if d.State != intake.Received || d.LastError != "" {
				t.Fatal("stale generation changed metadata", d.State, d.LastError)
			}
			if err := w.svc.Processed(t.Context(), fresh); err != nil || w.delivery(t, "fenced").State != intake.Dispatched {
				t.Fatal("new generation could not dispatch", err)
			}
		})
	}
}

func TestAccWebhookRetention(t *testing.T) {
	w := newWebhookWorld(t)
	foreignFixture := *w.f
	foreignFixture.acct = kernel.NewAccountID()
	foreign := newWebhookWorldOn(t, &foreignFixture)
	body := webhookBody(intake.InlineBody + 1)
	w.receive(t, "old", body)
	w.receive(t, "new", body)
	foreign.receive(t, "foreign", body)
	old := w.delivery(t, "old")
	newer := w.delivery(t, "new")
	other := foreign.delivery(t, "foreign")
	if _, err := w.f.db.Exec(t.Context(), `UPDATE vcs_deliveries SET received_at=now()-interval '8 days' WHERE id=$1`, old.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Pruned(t.Context(), queue.Job{}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.store.Read(t.Context(), w.f.acct, old.ID); !errors.Is(err, intake.ErrNotFound) {
		t.Fatal("old row retained", err)
	}
	if err := w.svc.Purge(t.Context(), w.f.acct); err != nil {
		t.Fatal(err)
	}
	if _, err := w.store.Read(t.Context(), w.f.acct, newer.ID); !errors.Is(err, intake.ErrNotFound) {
		t.Fatal("purge retained own row", err)
	}
	if _, err := foreign.store.Read(t.Context(), foreign.f.acct, other.ID); err != nil {
		t.Fatal("purge removed foreign row", err)
	}
	for _, key := range []string{old.BodyKey, newer.BodyKey} {
		var state string
		if err := w.f.db.QueryRow(t.Context(), `SELECT state FROM blob_objects WHERE store='inbox' AND key=$1`, key).Scan(&state); err != nil || state != "deleting" {
			t.Fatal("body deletion not scheduled", state, err)
		}
	}
}

func TestAccWebhookIntegrationHold(t *testing.T) {
	w := newWebhookWorld(t)
	ctx := t.Context()
	body := webhookBody(128)
	other, err := pgxpool.NewWithConfig(ctx, w.f.db.Config().Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	for _, owner := range []intake.Delivery{
		{Account: kernel.NewAccountID(), Integration: w.id, Provider: "GITHUB_ENTERPRISE"},
		{Account: w.f.acct, Integration: w.id, Provider: "GITLAB"},
	} {
		if err := tx.Do(ctx, w.f.db, tx.Options{}, func(ctx context.Context) error { _, err := w.store.Insert(ctx, owner); return err }); !errors.Is(err, intake.ErrNotFound) {
			t.Fatal("integration hold ignored ownership/provider", err)
		}
	}
	if _, err := w.f.db.Exec(ctx, `INSERT INTO vcs_integrations(account_id,id,provider,slug,name,space_id)
		SELECT $1,id,provider,'duplicate','Duplicate',$1 FROM vcs_integrations WHERE id=$2`, kernel.NewAccountID(), w.id); err == nil {
		t.Fatal("public integration id is not globally unique")
	} else if pe, ok := errors.AsType[*pgconn.PgError](err); !ok || pe.Code != "23505" {
		t.Fatal(err)
	}
	err = tx.Do(ctx, w.f.db, tx.Options{}, func(ctx context.Context) error {
		code, err := w.svc.Receive(ctx, "github_enterprise", w.id.String(), w.headers("locked", body), body)
		if err != nil {
			return err
		}
		if code != 202 {
			return fmt.Errorf("intake before lock returned %d", code)
		}
		// A real DELETE on a different connection must wait for the intake
		// transaction's key-share lock, then fail its deliberately short bound.
		tr, err := other.Begin(ctx)
		if err != nil {
			return err
		}
		defer tr.Rollback(ctx)
		if _, err := tr.Exec(ctx, `SET LOCAL lock_timeout='50ms'`); err != nil {
			return err
		}
		_, err = tr.Exec(ctx, `DELETE FROM vcs_integrations WHERE account_id=$1 AND id=$2`, w.f.acct, w.id)
		if pe, ok := errors.AsType[*pgconn.PgError](err); !ok || pe.Code != "55P03" {
			return fmt.Errorf("concurrent deletion was not locked: %w", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.f.store.Delete(ctx, w.f.acct, w.id); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Processed(ctx, w.claim(t, w.queue, "after-delete")); err != nil || w.delivery(t, "locked").State != intake.Dispatched {
		t.Fatal("retained provider could not parse after integration deletion", err)
	}
}

func TestAccWebhookFailedAttempts(t *testing.T) {
	for _, last := range []bool{false, true} {
		t.Run(fmt.Sprint(last), func(t *testing.T) {
			w := newWebhookWorld(t)
			w.receive(t, "failure", webhookBody(128))
			if last {
				if _, err := w.f.db.Exec(t.Context(), `UPDATE jobs SET max_attempts=1 WHERE account_id=$1 AND kind=$2`, w.f.acct, intake.Process.Name); err != nil {
					t.Fatal(err)
				}
			}
			j := w.claim(t, w.queue, "failure")
			deps := w.deps
			deps.Providers = map[string]port.Webhooks{"GITHUB_ENTERPRISE": webhooktest.Provider{ParseError: errors.New("Authorization webhook-secret-535 payload")}}
			err := intake.New(deps).Processed(t.Context(), j)
			if err == nil {
				t.Fatal("parser failure ignored")
			}
			dead, failure := w.queue.Fail(t.Context(), j, err)
			if failure != nil || dead != last {
				t.Fatal("queue settlement", dead, failure)
			}
			d := w.delivery(t, "failure")
			want := intake.Received
			if last {
				want = intake.Failed
			}
			if d.State != want || d.LastError == "" || strings.Contains(d.LastError, "webhook-secret-535") {
				t.Fatal("unsafe/wrong attempt state", d.State, d.LastError)
			}
			var queuedError string
			if err := w.f.db.QueryRow(t.Context(), `SELECT last_error FROM jobs WHERE id=$1`, j.ID).
				Scan(&queuedError); err != nil ||
				strings.Contains(queuedError, "webhook-secret-535") {
				t.Fatal("queue error leaked", queuedError, err)
			}
		})
	}
	w := newWebhookWorld(t)
	w.receive(t, "expired", webhookBody(128))
	j := w.claim(t, w.queue, "deadline")
	if _, err := w.f.db.Exec(t.Context(), `UPDATE vcs_deliveries SET deadline=now()-interval '1 second' WHERE account_id=$1`, w.f.acct); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Processed(t.Context(), j); err != nil || w.delivery(t, "expired").State != intake.Expired || w.parsed != 0 {
		t.Fatal("expired delivery acted", err)
	}
}

func TestAccWebhookStoreUnavailable(t *testing.T) {
	w := newWebhookWorld(t)
	w.f.db.Close()
	ctx := t.Context()
	_, lookup := w.store.Integration(ctx, w.id, time.Now())
	_, insert := w.store.Insert(ctx, intake.Delivery{})
	_, read := w.store.Read(ctx, w.f.acct, kernel.NewULID())
	state := w.store.State(ctx, w.f.acct, kernel.NewULID(), 0, intake.Failed, "safe")
	_, _, drop := w.store.Drop(ctx, &w.f.acct, nil, 1)
	_, pending := w.store.Pending(ctx, kernel.ULID{}, 1)
	for name, err := range map[string]error{"lookup": lookup, "insert": insert, "read": read, "state": state, "drop": drop, "pending": pending} {
		if err == nil || errors.Is(err, intake.ErrNotFound) {
			t.Errorf("%s on closed pool=%v", name, err)
		}
	}
}

func (w *webhookWorld) claimReconcile(t *testing.T) queue.Job {
	t.Helper()
	jobs, err := w.queue.Claim(t.Context(), queue.Maintenance, []string{intake.Reconcile.Name}, 1, time.Minute, "reconcile")
	if err != nil || len(jobs) != 1 {
		t.Fatalf("reconcile claim=%v,%v", jobs, err)
	}
	return jobs[0]
}

func TestAccWebhookFinalLeaseRecovery(t *testing.T) {
	for _, discard := range []bool{false, true} {
		t.Run(fmt.Sprint(discard), func(t *testing.T) {
			w := newWebhookWorld(t)
			w.receive(t, "last-lease", webhookBody(128))
			if _, err := w.f.db.Exec(t.Context(), `UPDATE jobs SET max_attempts=1 WHERE account_id=$1 AND kind=$2`, w.f.acct, intake.Process.Name); err != nil {
				t.Fatal(err)
			}
			j := w.claim(t, w.queue, "crashed-final-attempt")
			if _, err := w.f.db.Exec(t.Context(), `UPDATE jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, j.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := w.queue.Recover(t.Context()); err != nil {
				t.Fatal(err)
			}
			if discard {
				if err := w.queue.Discard(t.Context(), j.ID); err != nil {
					t.Fatal(err)
				}
			}
			if w.delivery(t, "last-lease").State != intake.Received {
				t.Fatal("fixture did not exercise unreturned handler")
			}
			if _, err := w.queue.Enqueue(t.Context(), intake.Reconcile, nil); err != nil {
				t.Fatal(err)
			}
			if err := w.svc.Reconciled(t.Context(), w.claimReconcile(t)); err != nil {
				t.Fatal(err)
			}
			d := w.delivery(t, "last-lease")
			if d.State != intake.Failed || d.LastError == "" || w.parsed != 0 {
				t.Fatal("dead final lease was not safely settled", d.State, d.LastError)
			}
		})
	}
}

func TestAccWebhookReconcileContinuation(t *testing.T) {
	w := newWebhookWorld(t)
	body := webhookBody(128)
	for i := range intake.ReconcileBatch + 1 {
		w.receive(t, fmt.Sprint(i), body)
	}
	middle := w.delivery(t, "250")
	last := w.delivery(t, fmt.Sprint(intake.ReconcileBatch))
	// Seed dead jobs at both sides of the batch boundary. Other received rows
	// stay live, so repeatedly starting at zero cannot reach the final dead row.
	if _, err := w.f.db.Exec(t.Context(), `UPDATE jobs SET state='dead',finished_at=now(),last_error='sensitive raw failure'
		WHERE account_id=$1 AND kind=$2 AND unique_key=ANY($3)`, w.f.acct, intake.Process.Name, []string{middle.ID.String(), last.ID.String()}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.queue.Enqueue(t.Context(), intake.Reconcile, nil); err != nil {
		t.Fatal(err)
	}
	j := w.claimReconcile(t)
	deps := w.deps
	deps.DB = rejectBegin{w.f.db}
	if err := intake.New(deps).Reconciled(t.Context(), j); err == nil {
		t.Fatal("reconcile commit failure ignored")
	}
	if w.delivery(t, "250").State != intake.Received {
		t.Fatal("failed reconcile committed metadata")
	}
	var continuations int
	if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE kind=$1 AND unique_key IS NOT NULL`, intake.Reconcile.Name).
		Scan(&continuations); err != nil ||
		continuations != 0 {
		t.Fatal("failed reconcile committed continuation", continuations, err)
	}
	if err := w.svc.Reconciled(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	if w.delivery(t, "250").State != intake.Failed || w.delivery(t, fmt.Sprint(intake.ReconcileBatch)).State != intake.Received {
		t.Fatal("batch did not remain bounded")
	}
	if err := w.svc.Reconciled(t.Context(), w.claimReconcile(t)); err != nil {
		t.Fatal(err)
	}
	d := w.delivery(t, fmt.Sprint(intake.ReconcileBatch))
	if d.State != intake.Failed || strings.Contains(d.LastError, "sensitive") {
		t.Fatal("later dead row starved or leaked raw error", d.State, d.LastError)
	}
}
