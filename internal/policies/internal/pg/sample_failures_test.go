package pg_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
	"github.com/weaveplatform/infraweave/internal/policies/internal/sampling"
)

func TestAccSampleEvaluationWriteFailuresRollBackEveryEvidenceEffect(t *testing.T) {
	for _, fault := range []struct{ name, table, predicate string }{
		{"quota", "policy_sample_budgets", "used < 0"},
		{"sample staging", "policy_input_objects", "false"},
		{"upload enqueue", "jobs", "kind <> 'policies.input-upload'"},
		{"job binding", "policy_input_objects", "upload_job_id IS NULL"},
		{"evaluation metadata", "policy_evaluations", "false"},
		{"capture gate", "policy_input_capture_heads", "false"},
		{"capture staging", "policy_input_objects", "capture_kind = ''"},
		{"capture head", "policy_input_capture_heads", "pending_input_id IS NULL"},
	} {
		t.Run(fault.name, func(t *testing.T) {
			w := newSampleWorld(t)
			if _, err := w.f.svc.Create(
				t.Context(),
				w.f.principal,
				core.Input{
					Name:   "Atomic denied evidence",
					Type:   rego.Plan,
					Body:   "package infraweave\nsample := true\ndeny contains \"denied\" if true\n",
					Labels: []string{"autoattach:*"},
				},
			); err != nil {
				t.Fatal(err)
			}
			// NOT VALID leaves unrelated existing rows alone, but the actual
			// PostgreSQL write fails at the selected transaction boundary.
			if _, err := w.f.db.Exec(t.Context(), "ALTER TABLE "+fault.table+" ADD CONSTRAINT refuse_evidence_write CHECK ("+fault.predicate+") NOT VALID"); err != nil {
				t.Fatal(err)
			}
			out, err := w.f.svc.Evaluate(t.Context(), fixtureEvaluationRequest(w.f, policyapi.Plan, evaluationInput(t, "plan-tracked-warn.json")))
			if err == nil || out.Outcome == policyapi.Allow {
				t.Fatalf("storage failure authorized or disappeared: %s %v", out.Outcome, err)
			}
			for _, table := range []string{"policy_evaluations", "policy_input_objects", "policy_sample_budgets", "policy_input_capture_heads"} {
				var count int
				if err := w.f.db.QueryRow(t.Context(), "SELECT count(*) FROM "+table+" WHERE account_id=$1", w.f.principal.AccountID).Scan(&count); err != nil || count != 0 {
					t.Fatalf("failed evaluation partially committed %s: %d %v", table, count, err)
				}
			}
			var jobs int
			if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE account_id=$1 AND kind='policies.input-upload'`, w.f.principal.AccountID).
				Scan(&jobs); err != nil ||
				jobs != 0 {
				t.Fatalf("failed evidence left an upload delivery: %d %v", jobs, err)
			}
		})
	}
}

func TestAccSamplePublicationWriteFailuresKeepARecoverableUnpublishedObject(t *testing.T) {
	for _, fault := range []struct{ name, table, predicate string }{
		{"blob publication", "blob_objects", "state <> 'live'"},
		{"input publication", "policy_input_objects", "state <> 'live'"},
		{"delivery completion", "jobs", "state <> 'succeeded'"},
	} {
		t.Run(fault.name, func(t *testing.T) {
			w := newSampleWorld(t)
			id := w.evaluate(t).Receipts[0].ID
			job := w.claim(t)
			if _, err := w.f.db.Exec(t.Context(), "ALTER TABLE "+fault.table+" ADD CONSTRAINT refuse_publication CHECK ("+fault.predicate+") NOT VALID"); err != nil {
				t.Fatal(err)
			}
			if err := w.samples.Upload(t.Context(), job); err == nil {
				t.Fatal("publication failure was hidden")
			}
			var inputState, blobState, jobState string
			var staged, complete bool
			if err := w.f.db.QueryRow(t.Context(), `SELECT i.state,b.state,j.state,i.staging_sealed IS NOT NULL,b.sha256 IS NOT NULL
    FROM policy_input_objects i JOIN blob_objects b ON b.store='policy-inputs' AND b.key=i.object_key JOIN jobs j ON j.id=i.upload_job_id
    WHERE i.account_id=$1 AND i.id=$2`, w.f.principal.AccountID, id).Scan(&inputState, &blobState, &jobState, &staged, &complete); err != nil || inputState != "uploading" || blobState != "pending" || jobState != "running" || !staged || !complete {
				t.Fatalf("publication partially committed: %s/%s/%s staging=%t uploaded=%t %v", inputState, blobState, jobState, staged, complete, err)
			}
			if _, err := w.samples.Read(t.Context(), w.f.principal.AccountID, id); !errors.Is(err, sampling.ErrNotFound) {
				t.Fatalf("failed publication was readable: %v", err)
			}
			if _, err := w.f.db.Exec(t.Context(), "ALTER TABLE "+fault.table+" DROP CONSTRAINT refuse_publication"); err != nil {
				t.Fatal(err)
			}
			if err := w.samples.Upload(t.Context(), job); err != nil {
				t.Fatalf("completed sealed upload could not recover publication: %v", err)
			}
			if _, err := w.samples.Read(t.Context(), w.f.principal.AccountID, id); err != nil {
				t.Fatalf("recovered publication unreadable: %v", err)
			}
		})
	}
}

func TestAccSamplingTransactionBoundariesRefuseUnfencedWrites(t *testing.T) {
	w := newSampleWorld(t)
	account := w.f.principal.AccountID
	id := kernel.NewULID()
	input := sampling.Input{Account: account, ID: id, SpaceSnapshot: &account.ULID, CaptureKind: "PLAN"}
	for name, call := range map[string]func(context.Context) error{
		"create staging": func(ctx context.Context) error { return w.f.store.CreateInput(ctx, input, []byte(`{}`)) },
		"metadata": func(ctx context.Context) error {
			return w.f.store.RecordEvaluation(ctx, policyapi.EvaluationRequest{}, account.ULID, policyapi.Receipt{}, nil, inputs.EvaluationFacts{})
		},
		"upload binding": func(ctx context.Context) error { return w.f.store.AttachUploadJob(ctx, account, id, 1) },
		"start upload":   func(ctx context.Context) error { return w.f.store.StartUpload(ctx, account, id) },
		"finish input":   func(ctx context.Context) error { return w.f.store.FinishInput(ctx, account, id, "failed", w.f.now) },
		"capture gate": func(ctx context.Context) error {
			_, _, err := w.f.store.CaptureSlot(ctx, account, account.ULID, "PLAN")
			return err
		},
		"pending capture": func(ctx context.Context) error { return w.f.store.SetCapturePending(ctx, input) },
		"publish capture": func(ctx context.Context) error { _, _, err := w.f.store.PublishCapture(ctx, input); return err },
		"clear capture":   func(ctx context.Context) error { return w.f.store.ClearCapturePending(ctx, input) },
		"retirement":      func(ctx context.Context) error { _, err := w.f.store.RetirementDue(ctx, input, w.f.now); return err },
		"purge input":     func(ctx context.Context) error { return w.f.store.DropInput(ctx, input) },
		"purge metadata":  func(ctx context.Context) error { _, err := w.f.store.PurgeMetadata(ctx, account, 10); return err },
		"locked input":    func(ctx context.Context) error { _, err := w.f.store.Input(ctx, account, id, true); return err },
		"evaluation": func(ctx context.Context) error {
			_, err := w.samples.Record(ctx, policyapi.EvaluationRequest{}, id, nil)
			return err
		},
		"delete policy": func(ctx context.Context) error { return w.samples.PolicyDeleted(ctx, account, id) },
	} {
		if err := call(t.Context()); !errors.Is(err, queue.ErrNoTransaction) {
			t.Errorf("%s allowed a partial write outside its owner transaction: %v", name, err)
		}
	}
	// Storage errors in a real aborted PostgreSQL transaction must remain
	// errors; reporting a miss could turn an incomplete cleanup into success.
	for name, call := range map[string]func(context.Context) error{
		"capture gate": func(ctx context.Context) error {
			_, _, err := w.f.store.CaptureSlot(ctx, account, id, "PLAN")
			return err
		},
		"pending capture": func(ctx context.Context) error { return w.f.store.SetCapturePending(ctx, input) },
		"start upload":    func(ctx context.Context) error { return w.f.store.StartUpload(ctx, account, id) },
		"finish input":    func(ctx context.Context) error { return w.f.store.FinishInput(ctx, account, id, "failed", w.f.now) },
		"bind upload":     func(ctx context.Context) error { return w.f.store.AttachUploadJob(ctx, account, id, 1) },
		"metadata": func(ctx context.Context) error {
			return w.f.store.RecordEvaluation(ctx, policyapi.EvaluationRequest{}, id, policyapi.Receipt{}, nil, inputs.EvaluationFacts{})
		},
		"due inputs":      func(ctx context.Context) error { _, err := w.f.store.DueInputs(ctx, w.f.now, 10); return err },
		"retirement due":  func(ctx context.Context) error { _, err := w.f.store.RetirementDue(ctx, input, w.f.now); return err },
		"policy inputs":   func(ctx context.Context) error { _, err := w.f.store.PolicyInputs(ctx, account, id, 10); return err },
		"account inputs":  func(ctx context.Context) error { _, err := w.f.store.AccountInputs(ctx, account, 10); return err },
		"drop input":      func(ctx context.Context) error { return w.f.store.DropInput(ctx, input) },
		"purge metadata":  func(ctx context.Context) error { _, err := w.f.store.PurgeMetadata(ctx, account, 10); return err },
		"clear pending":   func(ctx context.Context) error { return w.f.store.ClearCapturePending(ctx, input) },
		"publish capture": func(ctx context.Context) error { _, _, err := w.f.store.PublishCapture(ctx, input); return err },
	} {
		err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
			_, _ = tx.Q(ctx, w.f.db).Exec(ctx, `SELECT 1/0`)
			err := call(ctx)
			if err == nil || errors.Is(err, sampling.ErrNotFound) {
				t.Errorf("%s hid an aborted PostgreSQL transaction: %v", name, err)
			}
			return err
		})
		if err == nil {
			t.Errorf("%s committed a broken transaction", name)
		}
	}
}

func TestAccSampleCleanupFailureRollsBackPrivateRetirementAndRetries(t *testing.T) {
	for _, table := range []string{"policy_input_objects", "blob_objects"} {
		t.Run(table, func(t *testing.T) {
			w := newSampleWorld(t)
			id := w.evaluate(t).Receipts[0].ID
			w.uploadAll(t)
			w.f.now = time.Now().UTC().Add(sampling.Retention + time.Minute)
			if _, err := w.f.db.Exec(t.Context(), "ALTER TABLE "+table+" ADD CONSTRAINT refuse_retirement CHECK (state NOT IN ('expired','deleting')) NOT VALID"); err != nil {
				t.Fatal(err)
			}
			if err := w.samples.Cleanup(t.Context()); err == nil {
				t.Fatal("cleanup swallowed failed retirement")
			}
			var input, blob string
			if err := w.f.db.QueryRow(t.Context(), `SELECT i.state,b.state FROM policy_input_objects i JOIN blob_objects b ON b.store='policy-inputs' AND b.key=i.object_key WHERE i.account_id=$1 AND i.id=$2`, w.f.principal.AccountID, id).
				Scan(&input, &blob); err != nil ||
				input != "live" ||
				blob != "live" {
				t.Fatalf("failed cleanup partially retired a sample: %s/%s %v", input, blob, err)
			}
			if _, err := w.f.db.Exec(t.Context(), "ALTER TABLE "+table+" DROP CONSTRAINT refuse_retirement"); err != nil {
				t.Fatal(err)
			}
			if err := w.samples.Cleanup(t.Context()); err != nil {
				t.Fatal(err)
			}
			w.inputState(t, id, "expired", true)
		})
	}
}

func TestAccSampleDeliveryRejectsInvalidIdentityAndExposesStorageOutages(t *testing.T) {
	w := newSampleWorld(t)
	for _, payload := range []string{`{`, `{}`, `{"id":"invalid"}`} {
		job := queue.Job{Account: w.f.principal.AccountID.ULID, Payload: []byte(payload)}
		if err := w.samples.Upload(t.Context(), job); err == nil {
			t.Errorf("upload accepted malformed identity %q", payload)
		}
		if err := w.samples.RetirePolicySamples(t.Context(), job); err == nil {
			t.Errorf("retirement accepted malformed identity %q", payload)
		}
	}
	receipt := w.evaluate(t).Receipts[0]
	job := w.claim(t)
	w.f.db.Close()
	for name, call := range map[string]func() error{
		"upload":        func() error { return w.samples.Upload(t.Context(), job) },
		"cleanup":       func() error { return w.samples.Cleanup(t.Context()) },
		"account purge": func() error { return w.samples.PurgeAccount(t.Context(), w.f.principal.AccountID) },
		"policy retirement": func() error {
			return w.samples.RetirePolicySamples(t.Context(), queue.Job{Account: w.f.principal.AccountID.ULID, Payload: []byte(`{"policy":"` + receipt.Policy.String() + `"}`)})
		},
		"sample read": func() error { _, err := w.samples.Read(t.Context(), w.f.principal.AccountID, receipt.ID); return err },
		"sample status": func() error {
			_, err := w.samples.SampleStatuses(t.Context(), w.f.principal.AccountID, []kernel.ULID{receipt.ID})
			return err
		},
	} {
		if err := call(); err == nil || errors.Is(err, sampling.ErrNotFound) {
			t.Errorf("%s converted a storage outage to successful absence: %v", name, err)
		}
	}
}

func TestAccPolicyDeletionDoesNotCommitWithoutItsRetirementJob(t *testing.T) {
	w := newSampleWorld(t)
	receipt := w.evaluate(t).Receipts[0]
	w.uploadAll(t)
	if _, err := w.f.db.Exec(t.Context(), `ALTER TABLE jobs ADD CONSTRAINT refuse_retirement_job CHECK (kind<>'policies.sample-retire') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.f.svc.Delete(t.Context(), w.f.principal, receipt.Policy.String(), kernel.Expect{}, true); err == nil {
		t.Fatal("policy deleted without its required retirement delivery")
	}
	if _, err := w.samples.Read(t.Context(), w.f.principal.AccountID, receipt.ID); err != nil {
		t.Fatalf("failed deletion changed the retained sample: %v", err)
	}
}

func TestAccExpiredUploadRetirementCannotPartiallyComplete(t *testing.T) {
	for _, table := range []string{"policy_input_objects", "jobs"} {
		t.Run(table, func(t *testing.T) {
			w := newSampleWorld(t)
			id := w.evaluate(t).Receipts[0].ID
			job := w.claim(t)
			if _, err := w.f.db.Exec(
				t.Context(),
				`UPDATE policy_input_objects SET expires_at=$3 WHERE account_id=$1 AND id=$2`,
				w.f.principal.AccountID,
				id,
				w.f.now.Add(-time.Minute),
			); err != nil {
				t.Fatal(err)
			}
			if _, err := w.f.db.Exec(t.Context(), "ALTER TABLE "+table+" ADD CONSTRAINT refuse_discard CHECK (state NOT IN ('failed','succeeded')) NOT VALID"); err != nil {
				t.Fatal(err)
			}
			if err := w.samples.Upload(t.Context(), job); err == nil {
				t.Fatal("expired upload swallowed a discard failure")
			}
			var state string
			var staged bool
			if err := w.f.db.QueryRow(t.Context(), `SELECT state,staging_sealed IS NOT NULL FROM policy_input_objects WHERE account_id=$1 AND id=$2`, w.f.principal.AccountID, id).
				Scan(&state, &staged); err != nil || state != "pending" ||
				!staged {
				t.Fatalf("discard partially committed: %s/%t %v", state, staged, err)
			}
			if _, err := w.f.db.Exec(t.Context(), "ALTER TABLE "+table+" DROP CONSTRAINT refuse_discard"); err != nil {
				t.Fatal(err)
			}
			if err := w.samples.Upload(t.Context(), job); err != nil {
				t.Fatal(err)
			}
			w.inputState(t, id, "failed", false)
		})
	}
}

func TestAccStalePolicyRetirementJobCannotMutateSampleMetadata(t *testing.T) {
	w := newSampleWorld(t)
	receipt := w.evaluate(t).Receipts[0]
	if _, err := w.f.svc.Delete(t.Context(), w.f.principal, receipt.Policy.String(), kernel.Expect{}, true); err != nil {
		t.Fatal(err)
	}
	jobs, err := w.q.Claim(t.Context(), sampling.DeletedPolicyKind.Queue, []string{sampling.DeletedPolicyKind.Name}, 1, time.Minute, "stale-retirement")
	if err != nil || len(jobs) != 1 {
		t.Fatalf("retirement delivery: %d %v", len(jobs), err)
	}
	if err := w.q.Kill(t.Context(), jobs[0], errors.New("retirement generation no longer active")); err != nil {
		t.Fatal(err)
	}
	if err := w.samples.RetirePolicySamples(t.Context(), jobs[0]); !errors.Is(err, queue.ErrLeaseLost) {
		t.Fatalf("stale retirement retained authority: %v", err)
	}
	input, err := w.f.store.Input(t.Context(), w.f.principal.AccountID, receipt.ID, false)
	if err != nil || input.State != "pending" || len(input.Staging.Reveal()) == 0 {
		t.Fatalf("stale retirement changed private staging: %s %v", input.State, err)
	}
	if err := w.samples.Cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	w.inputState(t, receipt.ID, "failed", false)
}

func TestAccAccountPurgeRetriesAfterARealForeignReferenceRefusesDeletion(t *testing.T) {
	w := newSampleWorld(t)
	receipt := w.evaluate(t).Receipts[0]
	w.uploadAll(t)
	if _, err := w.f.db.Exec(
		t.Context(),
		`CREATE TABLE evidence_archive_anchor(account_id uuid,input_id uuid,FOREIGN KEY(account_id,input_id) REFERENCES policy_input_objects(account_id,id))`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := w.f.db.Exec(t.Context(), `INSERT INTO evidence_archive_anchor VALUES($1,$2)`, w.f.principal.AccountID, receipt.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.samples.PurgeAccount(t.Context(), w.f.principal.AccountID); err == nil {
		t.Fatal("purge hid a real foreign-reference failure")
	}
	if _, err := w.samples.Read(t.Context(), w.f.principal.AccountID, receipt.ID); err != nil {
		t.Fatalf("failed purge broke the still-owned object reference: %v", err)
	}
	if _, err := w.f.db.Exec(t.Context(), `DROP TABLE evidence_archive_anchor`); err != nil {
		t.Fatal(err)
	}
	if err := w.samples.PurgeAccount(t.Context(), w.f.principal.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.samples.Read(t.Context(), w.f.principal.AccountID, receipt.ID); !errors.Is(err, sampling.ErrNotFound) {
		t.Fatalf("purge retry retained the sample: %v", err)
	}
}
