package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
	gitlabadapter "github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/gitlab"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/feedback"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type deploymentWorld struct {
	f       *fixture
	jobs    *queue.Queue
	service *feedback.Service
	fake    *gitlabfake.Fake
	binding feedback.Deployment
}

func newDeploymentWorld(t *testing.T) *deploymentWorld {
	t.Helper()
	f := newFixture(t)
	fake := gitlabfake.New()
	srv := fake.Start()
	t.Cleanup(srv.Close)
	token := fake.AddToken("infraweave", "api")
	fake.AddMember("acme", "infra", "infraweave", gitlabfake.Maintainer)
	sha := fake.Repo("acme", "infra").Commit("main", "infrastructure", map[string]string{"main.tf": "resource test"})
	id := kernel.NewULID()
	err := f.store.Create(
		t.Context(),
		integration.Integration{
			Account:   f.acct,
			ID:        id,
			Provider:  "GITLAB",
			Space:     f.acct.ULID,
			Slug:      "deployments",
			Name:      "Deployments",
			Labels:    []string{},
			Checks:    integration.Checks{Mode: "INDIVIDUAL"},
			Settings:  port.Settings{APIHost: srv.URL},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			Version:   1,
		},
		integration.Secrets{Credential: []byte(token)},
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	w := &deploymentWorld{f: f, jobs: queue.New(f.db), fake: fake, binding: feedback.Deployment{
		Account:     f.acct,
		Integration: id,
		Provider:    "GITLAB",
		Namespace:   "acme",
		Repository:  "infra",
		NativeHost:  srv.URL + "/api/v4/",
		Run: port.DeploymentRun{
			ID:     kernel.NewULID(),
			Commit: sha,
			Ref:    "main",
		},
		Stack:     port.DeploymentStack{ID: kernel.NewULID(), Slug: "infra", Environment: "infraweave/infra"},
		CreatedAt: now,
		Epoch:     now.Add(time.Minute),
	}}
	w.service = feedback.NewDeployments(w.jobs, func(ctx context.Context, acct kernel.AccountID) error {
		return f.deps.Spaces.Hold(ctx, acct, acct.ULID)
	}, feedback.DeploymentDeps{DB: f.db, Store: f.store, Integrations: f.svc, Providers: map[string]port.Deployments{"GITLAB": gitlabadapter.New(gitlabadapter.Options{RootCAs: srv.Pool()})}})
	return w
}

func (w *deploymentWorld) enqueue(t *testing.T, d feedback.Deployment, state kernel.RunState, sequence int64) {
	t.Helper()
	body, err := json.Marshal(feedback.DeploymentRequest{Deployment: d, State: state, Sequence: sequence})
	if err != nil {
		t.Fatal(err)
	}
	target := vcsapi.FeedbackTarget{
		Account: d.Account.ULID,
		EventID: kernel.NewULID(),
		Key:     "deployment:" + d.Run.ID.String() + ":" + string(state),
		Job:     feedback.ReportDeployment.Name,
		Payload: body,
	}
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error { return w.service.Enqueue(ctx, target) }); err != nil {
		t.Fatal(err)
	}
}

func (w *deploymentWorld) claim(t *testing.T) queue.Job {
	t.Helper()
	jobs, err := w.jobs.Claim(t.Context(), queue.Deliveries, []string{feedback.ReportDeployment.Name}, 1, time.Minute, "deployments")
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim %+v,%v", jobs, err)
	}
	return jobs[0]
}

func (w *deploymentWorld) report(t *testing.T) {
	t.Helper()
	j := w.claim(t)
	if err := w.service.Reported(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	if err := w.jobs.Complete(t.Context(), j); !errors.Is(err, queue.ErrLeaseLost) {
		t.Fatalf("completion did not commit with cursor: %v", err)
	}
}

func TestAccDeploymentReports(t *testing.T) {
	w := newDeploymentWorld(t)
	w.enqueue(t, w.binding, kernel.RunStateUnconfirmed, 3)
	w.report(t)
	w.enqueue(t, w.binding, kernel.RunStateApplying, 5)
	w.report(t)
	w.enqueue(t, w.binding, kernel.RunStateFinished, 7)
	w.report(t)
	got := w.fake.Deployments("acme", "infra")
	if len(got) != 1 || !slices.Equal(got[0].States, []string{"running", "success"}) {
		t.Fatalf("native states %+v", got)
	}
	// The receipt remains after queue completion; a duplicate event cannot
	// recreate the same run/state job or the provider effect.
	w.enqueue(t, w.binding, kernel.RunStateFinished, 7)
	jobs, err := w.jobs.Claim(t.Context(), queue.Deliveries, []string{feedback.ReportDeployment.Name}, 1, time.Minute, "duplicate")
	if err != nil || len(jobs) != 0 {
		t.Fatalf("duplicate job %+v,%v", jobs, err)
	}
	kept := w.binding
	kept.Stack.Environment = "changed"
	kept.Integration = kernel.NewULID()
	kept.Repository = "different"
	w.enqueue(t, kept, kernel.RunStateFailed, 8)
	w.report(t)
	stored, err := w.f.store.Deployment(t.Context(), w.f.acct, w.binding.Run.ID)
	if err != nil || stored != w.binding {
		t.Fatalf("first binding not retained: %+v %v", stored, err)
	}
	got = w.fake.Deployments("acme", "infra")
	if len(got) != 1 || got[0].Status != "failed" || len(w.fake.Environments("acme", "infra")) != 1 {
		t.Fatalf("snapshot/report %+v", got)
	}
	if _, err := w.f.store.Deployment(t.Context(), kernel.NewAccountID(), w.binding.Run.ID); !errors.Is(err, feedback.ErrNoDeployment) {
		t.Fatalf("cross-account read %v", err)
	}
	for {
		done, err := w.f.store.PurgeDeployments(t.Context(), w.f.acct, 1)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
	}
	for _, table := range []string{"vcs_deployment_runs", "vcs_deployment_states", "vcs_deployment_resources"} {
		var n int
		if err := w.f.db.QueryRow(t.Context(), "SELECT count(*) FROM "+table+" WHERE account_id=$1", w.f.acct).Scan(&n); err != nil || n != 0 {
			t.Fatalf("purge %s=%d %v", table, n, err)
		}
	}
}

func TestAccDeploymentEpochOrdering(t *testing.T) {
	for _, laterEligible := range []bool{false, true} {
		t.Run(fmt.Sprintf("older_created_later_eligible_%t", laterEligible), func(t *testing.T) {
			w := newDeploymentWorld(t)
			older, newer := w.binding, w.binding
			older.Run.ID = kernel.NewULID()
			newer.Run.ID = kernel.NewULID()
			older.CreatedAt = older.CreatedAt.Add(-time.Hour)
			newer.Epoch = older.Epoch.Add(time.Minute)
			w.enqueue(t, newer, kernel.RunStateFinished, 7)
			w.report(t)
			if laterEligible {
				older.Epoch = newer.Epoch.Add(time.Minute)
			}
			w.enqueue(t, older, kernel.RunStateApplying, 5)
			w.report(t)
			got := w.fake.Deployments("acme", "infra")
			want := []string{"success"}
			if laterEligible {
				want = append(want, "running")
			}
			if len(got) != 1 || !slices.Equal(got[0].States, want) {
				t.Fatalf("epoch order: %+v want %v", got, want)
			}
		})
	}
}

func TestAccDeploymentOutOfOrderAndReservation(t *testing.T) {
	w := newDeploymentWorld(t)
	// Terminal delivered first creates the native deployment with success;
	// a subsequently delivered older sequence must leave it there.
	w.enqueue(t, w.binding, kernel.RunStateFinished, 7)
	w.enqueue(t, w.binding, kernel.RunStateApplying, 5)
	w.report(t)
	w.report(t)
	got := w.fake.Deployments("acme", "infra")
	if len(got) != 1 || !slices.Equal(got[0].States, []string{"success"}) {
		t.Fatalf("sequence regressed %+v", got)
	}
	older, newer := w.binding, w.binding
	older.Run.ID = kernel.NewULID()
	newer.Run.ID = kernel.NewULID()
	older.Epoch = older.Epoch.Add(time.Hour)
	newer.Epoch = older.Epoch.Add(time.Minute)
	w.enqueue(t, older, kernel.RunStateApplying, 5)
	w.enqueue(t, newer, kernel.RunStateFinished, 7)
	w.report(t) // Older queued first, but newer eligibility already owns resource.
	if n := len(w.fake.DeploymentWrites("acme", "infra")); n != 1 {
		t.Fatalf("old reservation issued provider call: %d", n)
	}
	w.report(t)
	if got := w.fake.Deployments("acme", "infra"); len(got) != 1 || got[0].Status != "success" {
		t.Fatalf("shared native ID %+v", got)
	}
}

func TestAccDeploymentGenerationAndResponseLoss(t *testing.T) {
	w := newDeploymentWorld(t)
	w.enqueue(t, w.binding, kernel.RunStateApplying, 5)
	old := w.claim(t)
	if _, err := w.f.db.Exec(t.Context(), `UPDATE jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.jobs.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	current := w.claim(t)
	if err := w.service.Reported(t.Context(), old); !errors.Is(err, queue.ErrLeaseLost) || len(w.fake.Deployments("acme", "infra")) != 0 {
		t.Fatalf("old generation effect=%v", err)
	}
	w.fake.FailNextDeploymentCreate()
	if err := w.service.Reported(t.Context(), current); !errors.Is(err, feedback.ErrDeploymentReport) {
		t.Fatalf("lost create=%v", err)
	}
	if got := w.fake.Deployments("acme", "infra"); len(got) != 1 {
		t.Fatalf("native create %+v", got)
	}
	if err := w.service.Reported(t.Context(), current); err != nil {
		t.Fatal(err)
	}
	if got := w.fake.Deployments("acme", "infra"); len(got) != 1 || got[0].Status != "running" {
		t.Fatalf("retry duplicated %+v", got)
	}
}

func TestAccDeploymentEnqueueAtomicity(t *testing.T) {
	w := newDeploymentWorld(t)
	body, err := json.Marshal(feedback.DeploymentRequest{Deployment: w.binding, State: kernel.RunStateUnconfirmed, Sequence: 3})
	if err != nil {
		t.Fatal(err)
	}
	target := vcsapi.FeedbackTarget{Account: w.f.acct.ULID, EventID: kernel.NewULID(), Key: "deployment", Job: feedback.ReportDeployment.Name, Payload: body}
	rollback := errors.New("outer fanout failed")
	err = tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		if err := w.service.Enqueue(ctx, target); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if _, err := w.f.store.Deployment(t.Context(), w.f.acct, w.binding.Run.ID); !errors.Is(err, feedback.ErrNoDeployment) {
		t.Fatalf("binding survived rollback %v", err)
	}
	var n int
	if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE kind=$1 AND account_id=$2`, feedback.ReportDeployment.Name, w.f.acct).Scan(&n); err != nil || n != 0 {
		t.Fatalf("job survived rollback %d %v", n, err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			errs <- tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error { return w.service.Enqueue(ctx, target) })
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE kind=$1 AND account_id=$2`, feedback.ReportDeployment.Name, w.f.acct).Scan(&n); err != nil || n != 1 {
		t.Fatalf("concurrent enqueue %d %v", n, err)
	}
	w.report(t)
	if err := tx.Do(
		t.Context(),
		w.f.db,
		tx.Options{},
		func(ctx context.Context) error { return w.service.Reported(ctx, queue.Job{}) },
	); err == nil ||
		!strings.Contains(err.Error(), "inherited transaction") {
		t.Fatalf("nested effect transaction accepted %v", err)
	}
}

func TestAccDeploymentBindingNeedsNoCredential(t *testing.T) {
	w := newDeploymentWorld(t)
	if _, err := w.f.db.Exec(
		t.Context(),
		`UPDATE vcs_integrations SET credentials_sealed=$3,webhook_secret_sealed=$3 WHERE account_id=$1 AND id=$2`,
		w.f.acct,
		w.binding.Integration,
		[]byte("cannot decrypt"),
	); err != nil {
		t.Fatal(err)
	}
	id, ok, err := w.f.svc.Binding(t.Context(), w.f.acct, "GITLAB", &w.binding.Integration)
	if err != nil || !ok || id.ID != w.binding.Integration || len(id.Credential) != 0 || len(id.WebhookSecret) != 0 {
		t.Fatalf("metadata opened credentials: %+v %t %v", id, ok, err)
	}
	if _, _, err := w.f.svc.Use(t.Context(), w.f.acct, "GITLAB", &w.binding.Integration); err == nil {
		t.Fatal("fixture ciphertext unexpectedly valid")
	}
	if _, ok, err := w.f.svc.Binding(t.Context(), kernel.NewAccountID(), "GITLAB", &w.binding.Integration); err != nil || ok {
		t.Fatalf("binding crossed account: %t %v", ok, err)
	}
	if _, _, err := w.f.svc.Binding(t.Context(), w.f.acct, "GITHUB", &w.binding.Integration); err == nil {
		t.Fatal("binding crossed provider")
	}
}

func TestAccDeploymentEffectRollbackDoesNotReplay(t *testing.T) {
	w := newDeploymentWorld(t)
	w.enqueue(t, w.binding, kernel.RunStateApplying, 5)
	j := w.claim(t)
	// An acknowledgement failure occurs after the native API has accepted
	// the report. The outer NoRetry transaction must not repeat that effect.
	_, err := w.f.db.Exec(t.Context(), `CREATE FUNCTION refuse_deployment_ack() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN RAISE EXCEPTION 'acknowledgement unavailable' USING ERRCODE='40001'; END $$;
	CREATE TRIGGER refuse_deployment_ack BEFORE UPDATE OF sequence ON vcs_deployment_resources
	FOR EACH ROW EXECUTE FUNCTION refuse_deployment_ack()`)
	if err != nil {
		t.Fatal(err)
	}
	err = w.service.Reported(t.Context(), j)
	if err == nil {
		t.Fatal("acknowledgement failure ignored")
	}
	if got := w.fake.DeploymentWrites("acme", "infra"); len(got) != 1 {
		t.Fatalf("external effect replayed inside transaction: %+v", got)
	}
	var sequence int64
	if err := w.f.db.QueryRow(t.Context(), `SELECT sequence FROM vcs_deployment_resources WHERE account_id=$1`, w.f.acct).Scan(&sequence); err != nil || sequence != 0 {
		t.Fatalf("cursor survived failed acknowledgement: %d %v", sequence, err)
	}
	var state string
	if err := w.f.db.QueryRow(t.Context(), `SELECT state FROM jobs WHERE id=$1`, j.ID).Scan(&state); err != nil || state != "running" {
		t.Fatalf("completion survived rollback: %s %v", state, err)
	}
	if _, err := w.f.db.Exec(t.Context(), `DROP TRIGGER refuse_deployment_ack ON vcs_deployment_resources; DROP FUNCTION refuse_deployment_ack()`); err != nil {
		t.Fatal(err)
	}
	if err := w.service.Reported(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	if got := w.fake.Deployments("acme", "infra"); len(got) != 1 || len(got[0].States) != 1 {
		t.Fatalf("later queue attempt did not recover remote effect: %+v", got)
	}
}

func TestAccDeploymentSharedHostAcrossIntegrations(t *testing.T) {
	w := newDeploymentWorld(t)
	in, err := w.f.store.Get(t.Context(), w.f.acct, w.binding.Integration, false)
	if err != nil {
		t.Fatal(err)
	}
	sec, err := w.f.store.Secrets(t.Context(), w.f.acct, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	in.ID = kernel.NewULID()
	in.Slug = "another"
	in.Name = "Another"
	in.Settings.APIHost = strings.TrimSuffix(w.binding.NativeHost, "/") // Alternate accepted API-prefix form.
	if err := w.f.store.Create(t.Context(), in, sec); err != nil {
		t.Fatal(err)
	}
	newer := w.binding
	newer.Run.ID = kernel.NewULID()
	newer.Integration = in.ID
	newer.Epoch = newer.Epoch.Add(time.Minute)
	w.enqueue(t, newer, kernel.RunStateFinished, 7)
	w.report(t)
	w.enqueue(t, w.binding, kernel.RunStateApplying, 5)
	w.report(t)
	got := w.fake.Deployments("acme", "infra")
	if len(got) != 1 || !slices.Equal(got[0].States, []string{"success"}) {
		t.Fatalf("integration IDs split native fence: %+v", got)
	}
	var resources int
	if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM vcs_deployment_resources WHERE account_id=$1`, w.f.acct).Scan(&resources); err != nil || resources != 1 {
		t.Fatalf("resource identity count %d %v", resources, err)
	}
}

func TestAccDeploymentRetargetRefusalAndCredentialRotation(t *testing.T) {
	w := newDeploymentWorld(t)
	w.enqueue(t, w.binding, kernel.RunStateUnconfirmed, 3)
	w.report(t)
	in, err := w.f.store.Get(t.Context(), w.f.acct, w.binding.Integration, false)
	if err != nil {
		t.Fatal(err)
	}
	rotated := w.fake.AddToken("infraweave", "api")
	in.Settings.APIHost = w.binding.NativeHost
	if err := w.f.store.Write(t.Context(), in, &integration.Secrets{Credential: []byte(rotated)}); err != nil {
		t.Fatal(err)
	}
	w.enqueue(t, w.binding, kernel.RunStateApplying, 5)
	w.report(t)
	if got := w.fake.Deployments("acme", "infra"); len(got) != 1 {
		t.Fatalf("same-host rotation changed resource %+v", got)
	}
	in.Settings.APIHost = "https://different.example.com"
	if err := w.f.store.Write(t.Context(), in, nil); err != nil {
		t.Fatal(err)
	}
	w.enqueue(t, w.binding, kernel.RunStateFinished, 7)
	j := w.claim(t)
	before := w.fake.Requests()
	if err := w.service.Reported(t.Context(), j); !errors.Is(err, feedback.ErrDeploymentReport) || w.fake.Requests() != before {
		t.Fatalf("retargeted binding escaped fence: %v", err)
	}
	if got := w.fake.Deployments("acme", "infra"); got[0].Status != "running" {
		t.Fatalf("retarget refusal changed native status %+v", got)
	}
}
