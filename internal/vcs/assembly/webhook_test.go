package assembly

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/blobs"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/fs"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/feedback"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/webhooktest"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/push"
)

// purgeBoundary commits a verified request at the integration deletion boundary.
// If delivery purge runs first, that newly committed delivery survives it.
type purgeBoundary struct {
	integration.Store
	before func(context.Context) error
}

func (s *purgeBoundary) Purge(ctx context.Context, acct kernel.AccountID, batch int) (bool, error) {
	if s.before != nil {
		before := s.before
		s.before = nil
		if err := before(ctx); err != nil {
			return false, err
		}
	}
	return s.Store.Purge(ctx, acct, batch)
}

func TestAccWebhookAccountPurgeBoundary(t *testing.T) {
	w := newWorld(t)
	stack := w.integration("GITHUB_ENTERPRISE", port.Settings{}, []byte("token"), "acme", "infra")
	id := *w.stacks.settings[stack].VCSIntegration
	secret := bytes.Repeat([]byte("h"), 32)
	in, err := w.store.Get(t.Context(), w.acct, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.store.Write(t.Context(), in, &integration.Secrets{
		Credential: []byte("token"), WebhookSecret: secret,
	}); err != nil {
		t.Fatal(err)
	}
	bucket, err := fs.New(objectstore.FSConfig{Root: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bucket.Close()
	store, err := objectstore.New(objectstore.Defaults(), func(string) (objectstore.Bucket, error) { return bucket, nil })
	if err != nil {
		t.Fatal(err)
	}
	q := queue.New(w.db)
	objects := blobs.New(w.db, q, store, w.keys, objectstore.Defaults())
	svc := intake.New(intake.Deps{
		DB: w.db, Store: pg.NewDeliveries(w.db, w.keys), Objects: objects, Jobs: q,
		Providers: map[string]port.Webhooks{"GITHUB_ENTERPRISE": webhooktest.Provider{}},
	})
	body := bytes.Repeat([]byte("x"), intake.InlineBody+1)
	receive := func(ctx context.Context, delivery string) error {
		h := http.Header{"X-Github-Event": {"push"}, "X-Github-Delivery": {delivery}, "X-Hub-Signature-256": {"sha256=" + string(webhooktest.Sign(body, secret))}}
		code, err := svc.Receive(ctx, "github_enterprise", id.String(), h, body)
		if err != nil || code != 202 {
			return fmt.Errorf("intake at purge boundary: %d %w", code, err)
		}
		return nil
	}
	if err := receive(t.Context(), "before"); err != nil {
		t.Fatal(err)
	}
	boundary := &purgeBoundary{Store: w.store, before: func(ctx context.Context) error { return receive(ctx, "racing") }}
	limit := 2
	if err := w.store.SetRunLimit(t.Context(), w.acct, &limit); err != nil {
		t.Fatal(err)
	}
	deployment := feedback.Deployment{
		Account: w.acct, Integration: id, Provider: "GITHUB_ENTERPRISE", Namespace: "acme", Repository: "infra",
		Run:   port.DeploymentRun{ID: kernel.NewULID(), Commit: strings.Repeat("a", 40), Ref: "main"},
		Stack: port.DeploymentStack{ID: stack, Slug: "infra", Environment: "infraweave/infra"}, Epoch: time.Now(), CreatedAt: time.Now(),
	}
	if err := tx.Do(t.Context(), w.db, tx.Options{}, func(ctx context.Context) error {
		if _, err := w.store.BindDeployment(ctx, deployment); err != nil {
			return err
		}
		if _, err := w.store.RequestDeployment(ctx, feedback.DeploymentRequest{Deployment: deployment, State: kernel.RunStateUnconfirmed, Sequence: 3}); err != nil {
			return err
		}
		return w.store.ReserveDeployment(ctx, deployment, "purge-boundary")
	}); err != nil {
		t.Fatal(err)
	}
	d := &Domain{
		integrations: integration.New(integration.Deps{Store: boundary}), intake: svc,
		feedback: feedback.NewDeployments(q, nil, feedback.DeploymentDeps{DB: w.db, Store: w.store}),
		limits:   &push.Limits{Store: w.store},
	}
	if err := d.Purge(t.Context(), w.acct); err != nil {
		t.Fatal(err)
	}
	var integrations, deliveries, live, deleting int
	err = w.db.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM vcs_integrations WHERE account_id=$1),
		(SELECT count(*) FROM vcs_deliveries WHERE account_id=$1),
		(SELECT count(*) FROM blob_objects WHERE store='inbox' AND starts_with(key,$2) AND state='live'),
		(SELECT count(*) FROM blob_objects WHERE store='inbox' AND starts_with(key,$2) AND state='deleting')`, w.acct, w.acct.String()+"/").Scan(&integrations, &deliveries, &live, &deleting)
	if err != nil || integrations != 0 || deliveries != 0 || live != 0 || deleting != 2 {
		t.Fatalf("purge integration/delivery/live/deleting=%d/%d/%d/%d: %v", integrations, deliveries, live, deleting, err)
	}
	for _, table := range []string{"vcs_deployment_runs", "vcs_deployment_states", "vcs_deployment_resources"} {
		var n int
		if err := w.db.QueryRow(t.Context(), "SELECT count(*) FROM "+table+" WHERE account_id=$1", w.acct).Scan(&n); err != nil || n != 0 {
			t.Fatalf("deployment purge %s=%d: %v", table, n, err)
		}
	}
	if _, err := svc.Receive(t.Context(), "github_enterprise", id.String(), http.Header{}, nil); err == nil {
		t.Fatal("purged integration still accepts intake")
	}
	if got, err := w.store.RunLimit(t.Context(), w.acct); err != nil || got != nil {
		t.Fatalf("purged account run limit = %v, %v", got, err)
	}
}
