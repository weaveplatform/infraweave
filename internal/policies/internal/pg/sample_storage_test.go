package pg_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/blobs"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/fs"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
	"github.com/weaveplatform/infraweave/internal/policies/internal/pg"
	"github.com/weaveplatform/infraweave/internal/policies/internal/sampling"
)

type sampleWorld struct {
	f       *fixture
	samples *sampling.Service
	objects *blobs.Objects
	raw     objectstore.Store
	keys    *crypt.Keyring
	q       *queue.Queue
}

func newSampleWorld(t *testing.T) *sampleWorld {
	t.Helper()
	f := newFixture(t)
	cfg := f.db.Config()
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	f.db = pool
	provider, err := kek.NewLocal(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypt.Load(t.Context(), pool, provider)
	if err != nil {
		t.Fatal(err)
	}
	c := objectstore.Defaults()
	c.Driver, c.FS.Root, c.FS.URL = objectstore.DriverFS, t.TempDir(), "http://infraweave.test"
	raw, err := objectstore.New(c, func(string) (objectstore.Bucket, error) { return fs.New(c.FS, keys) })
	if err != nil {
		t.Fatal(err)
	}
	q := queue.New(pool)
	objects := blobs.New(pool, q, objectstore.WriterOnly(raw), keys, c)
	f.store = pg.NewWithSamples(pool, keys)
	samples := sampling.New(sampling.Deps{DB: pool, Store: f.store, Queue: q, Objects: objects, Now: func() time.Time { return f.now }})
	f.svc = core.New(
		core.Deps{
			DB:       pool,
			Store:    f.store,
			Spaces:   f.spaces,
			Authz:    f.authz,
			Runtime:  f.runtime,
			Events:   f.events,
			Stacks:   f.stacks,
			Targets:  f.stacks,
			Queue:    q,
			Now:      func() time.Time { return f.now },
			Sampling: samples,
		},
	)
	return &sampleWorld{f: f, samples: samples, objects: objects, raw: raw, keys: keys, q: q}
}

func (w *sampleWorld) evaluate(t *testing.T) policyapi.Evaluation {
	t.Helper()
	body := `package infraweave
sample := true
deny contains "denied sample" if {print("private-retained-print")}
private_source := "private-retained-source"`
	if _, err := w.f.svc.Create(t.Context(), w.f.principal, core.Input{Name: "Retained denial", Type: rego.Plan, Body: body, Labels: []string{"autoattach:*"}}); err != nil {
		t.Fatal(err)
	}
	input := evaluationInput(t, "plan-tracked-warn.json")
	var document map[string]any
	if err := json.Unmarshal(input, &document); err != nil {
		t.Fatal(err)
	}
	document["infraweave"].(map[string]any)["run"].(map[string]any)["command"] = "private-retained-input"
	var err error
	input, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	got, err := w.f.svc.Evaluate(t.Context(), fixtureEvaluationRequest(w.f, policyapi.Plan, input))
	if err != nil || got.Outcome != policyapi.Deny || len(got.Receipts) != 1 || got.Receipts[0].SampleEvaluation == nil {
		t.Fatalf("real OPA sampled denial not staged: %+v %v", got, err)
	}
	return got
}

func (w *sampleWorld) claim(t *testing.T) queue.Job {
	t.Helper()
	jobs, err := w.q.Claim(t.Context(), sampling.UploadKind.Queue, []string{sampling.UploadKind.Name}, 1, time.Minute, "sample-test")
	if err != nil || len(jobs) != 1 {
		t.Fatalf("upload job: %+v %v", jobs, err)
	}
	return jobs[0]
}

func TestAccSampleSealedPublicationWithOneConnection(t *testing.T) {
	w := newSampleWorld(t)
	got := w.evaluate(t)
	receipt := got.Receipts[0]
	var sealed []byte
	var state, key string
	if err := w.f.db.QueryRow(t.Context(), `SELECT staging_sealed,state,object_key FROM policy_input_objects WHERE account_id=$1 AND id=$2`, w.f.principal.AccountID, receipt.ID).
		Scan(&sealed, &state, &key); err != nil {
		t.Fatal(err)
	}
	if state != "pending" || len(sealed) == 0 || bytes.Contains(sealed, []byte("private-retained")) {
		t.Fatal("evaluation did not commit only sealed staging")
	}
	if ok, err := w.objects.Live(t.Context(), objectstore.PolicyInputs, key); err != nil || ok {
		t.Fatalf("pending sample readable: %t %v", ok, err)
	}
	job := w.claim(t)
	for _, marker := range []string{"private-retained", "denied sample"} {
		if bytes.Contains(job.Payload, []byte(marker)) {
			t.Fatal("private sample entered queue payload")
		}
	}
	// A synchronous upload inside the evaluation TX would require a second
	// connection and deadlock this actual one-connection pool.
	if err := w.samples.Upload(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	var staged bool
	var until time.Time
	if err := w.f.db.QueryRow(t.Context(), `SELECT i.state,i.staging_sealed IS NOT NULL,b.expires_at FROM policy_input_objects i JOIN blob_objects b ON b.store='policy-inputs' AND b.key=i.object_key WHERE i.account_id=$1 AND i.id=$2`, w.f.principal.AccountID, receipt.ID).
		Scan(&state, &staged, &until); err != nil {
		t.Fatal(err)
	}
	if state != "live" || staged || !until.Equal(receipt.CreatedAt.Add(sampling.Retention)) {
		t.Fatalf("publication retained staging or extended TTL: %s/%t/%s", state, staged, until)
	}
	reader, err := w.raw.Get(t.Context(), objectstore.PolicyInputs, key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("private-retained")) {
		t.Fatal("sample plaintext in raw stored bytes")
	}
	opened, err := w.objects.Open(t.Context(), objectstore.PolicyInputs, key)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(opened)
	_ = opened.Close()
	if err != nil {
		t.Fatal(err)
	}
	var payload sampling.Payload
	if err := json.Unmarshal(
		plain,
		&payload,
	); err != nil || payload.Outcome != policyapi.Deny || !strings.Contains(payload.Print, "private-retained-print") ||
		!strings.Contains(payload.Body, "private-retained-source") {
		t.Fatalf("sealed sample lost evaluated evidence: %v", err)
	}
	if !bytes.Equal(payload.Input, got.Samples[0].Input) || !bytes.Equal(payload.Result, receipt.Decision) {
		t.Fatal("sample changed input or result")
	}
	if _, err := w.f.store.Input(t.Context(), kernel.NewAccountID(), receipt.ID, false); !errors.Is(err, sampling.ErrNotFound) {
		t.Fatalf("cross-account sample: %v", err)
	}
	var publications int
	if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM policy_input_objects WHERE account_id=$1 AND published_at IS NOT NULL`, w.f.principal.AccountID).
		Scan(&publications); err != nil ||
		publications != 1 {
		t.Fatalf("publication count: %d %v", publications, err)
	}
}

func TestAccSampleMetadataBudgetAndStagingRollbackTogether(t *testing.T) {
	w := newSampleWorld(t)
	account := w.f.principal.AccountID
	sentinel := errors.New("abort admitted evaluation transaction")
	receipt := policyapi.Receipt{
		ID:            kernel.NewULID(),
		Policy:        kernel.NewULID(),
		PolicyName:    "rolled back",
		PolicyVersion: 1,
		Type:          policyapi.Task,
		Engine:        string(rego.V1),
		Outcome:       policyapi.Deny,
		Sample:        true,
		CreatedAt:     w.f.now,
	}
	req := policyapi.EvaluationRequest{Account: account, Type: policyapi.Task, Target: policyapi.Target{Kind: policyapi.StackTarget, ID: w.f.stacks.stack.ID}}
	err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		if _, err := w.samples.Record(ctx, req, account.ULID, []sampling.Evaluation{{Receipt: receipt, Input: json.RawMessage(`{"private":"rollback"}`)}}); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	var evaluations, inputs, budgets, jobs int
	if err := w.f.db.QueryRow(t.Context(), `SELECT
        (SELECT count(*) FROM policy_evaluations WHERE account_id=$1),
        (SELECT count(*) FROM policy_input_objects WHERE account_id=$1),
        (SELECT count(*) FROM policy_sample_budgets WHERE account_id=$1),
        (SELECT count(*) FROM jobs WHERE account_id=$1 AND kind='policies.input-upload')`, account).Scan(&evaluations, &inputs, &budgets, &jobs); err != nil || evaluations+inputs+budgets+jobs != 0 {
		t.Fatalf("rollback leaked sample effects %d/%d/%d/%d %v", evaluations, inputs, budgets, jobs, err)
	}
}

func TestAccSampleStatusRequiresPublicationAndExpiresBeforeSweep(t *testing.T) {
	w := newSampleWorld(t)
	got := w.evaluate(t)
	id := got.Receipts[0].ID
	account := w.f.principal.AccountID
	check := func(sampled, expired bool) {
		t.Helper()
		statuses, err := w.samples.SampleStatuses(t.Context(), account, []kernel.ULID{id})
		if err != nil || statuses[id].Sampled != sampled || statuses[id].Expired != expired {
			t.Fatalf("sample status %v: %+v %v", id, statuses, err)
		}
	}
	check(false, false)
	if _, err := w.samples.Read(t.Context(), account, id); !errors.Is(err, sampling.ErrNotFound) {
		t.Fatalf("pending input was readable: %v", err)
	}
	if err := w.samples.Upload(t.Context(), w.claim(t)); err != nil {
		t.Fatal(err)
	}
	check(true, false)
	if payload, err := w.samples.Read(t.Context(), account, id); err != nil || !strings.Contains(payload.Print, "private-retained-print") {
		t.Fatalf("live sample read failed: %v", err)
	}
	foreign, err := w.samples.SampleStatuses(t.Context(), kernel.NewAccountID(), []kernel.ULID{id})
	if err != nil || len(foreign) != 0 {
		t.Fatalf("foreign sample status leaked: %+v %v", foreign, err)
	}
	w.f.now = time.Now().UTC()
	// Blob retention may be configured shorter than the business ceiling.
	// Its elapsed deadline must be respected before the reaper runs.
	if _, err := w.f.db.Exec(t.Context(), `UPDATE blob_objects SET expires_at=$2
        WHERE store='policy-inputs' AND key=(SELECT object_key FROM policy_input_objects WHERE account_id=$1 AND id=$3)`, account, w.f.now.Add(-time.Minute), id); err != nil {
		t.Fatal(err)
	}
	check(false, true)
	if _, err := w.samples.Read(t.Context(), account, id); !errors.Is(err, sampling.ErrNotFound) {
		t.Fatalf("expired input readable before sweep: %v", err)
	}
	reaper := blobs.NewReaper(w.f.db, w.q, w.raw)
	if _, err := reaper.Expire(t.Context(), 1000); err != nil {
		t.Fatal(err)
	}
	check(false, true)
	var state string
	if err := w.f.db.QueryRow(t.Context(), `SELECT b.state FROM blob_objects b JOIN policy_input_objects i ON i.object_key=b.key WHERE b.store='policy-inputs' AND i.account_id=$1 AND i.id=$2`, account, id).
		Scan(&state); err != nil ||
		state != "deleting" {
		t.Fatalf("expiry did not retire object: %s %v", state, err)
	}
}

func TestAccSampleStagingRejectsCiphertextCopiedToAnotherRow(t *testing.T) {
	w := newSampleWorld(t)
	account := w.f.principal.AccountID
	first, second := kernel.NewULID(), kernel.NewULID()
	err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		for _, id := range []kernel.ULID{first, second} {
			input := sampling.Input{Account: account, ID: id, Key: account.String() + "/staging/" + id.String(), CreatedAt: w.f.now, ExpiresAt: w.f.now.Add(sampling.Retention)}
			if err := w.f.store.CreateInput(ctx, input, []byte(`{"input":"row-bound-private"}`)); err != nil {
				return err
			}
		}
		_, err := tx.Q(ctx, w.f.db).
			Exec(ctx, `UPDATE policy_input_objects SET staging_sealed=(SELECT staging_sealed FROM policy_input_objects WHERE account_id=$1 AND id=$2) WHERE account_id=$1 AND id=$3`, account, first, second)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.f.store.Input(t.Context(), account, first, false); err != nil {
		t.Fatalf("original staging failed: %v", err)
	}
	if _, err := w.f.store.Input(t.Context(), account, second, false); err == nil {
		t.Fatal("copied staging opened under another primary key")
	}
}
