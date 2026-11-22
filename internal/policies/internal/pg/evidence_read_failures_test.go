package pg_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/internal/platform/seal"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/sampling"
)

func TestAccEvidenceDocumentRefusesUnfencedAndUnpublishedOrExpiredInput(t *testing.T) {
	for _, state := range []string{"unfenced", "unpublished", "retired", "exact expiry"} {
		t.Run(state, func(t *testing.T) {
			w := newSampleWorld(t)
			id := w.evaluate(t).Receipts[0].ID
			if state != "unpublished" {
				w.uploadAll(t)
			}
			if state == "retired" {
				if _, err := w.f.db.Exec(t.Context(), `UPDATE policy_input_objects SET state='expired' WHERE account_id=$1 AND id=$2`, w.f.principal.AccountID, id); err != nil {
					t.Fatal(err)
				}
			}
			input, err := w.f.store.InspectInput(t.Context(), w.f.principal.AccountID, id, false)
			if err != nil {
				t.Fatal(err)
			}
			now := w.f.now
			if state == "exact expiry" {
				w.f.now = input.ExpiresAt
			}
			var raw []byte
			if state == "unfenced" {
				raw, err = w.samples.ReadDocument(t.Context(), input)
			} else {
				err = tx.Do(t.Context(), w.f.db, tx.Options{NoRetry: true}, func(ctx context.Context) error {
					var readErr error
					raw, readErr = w.samples.ReadDocument(ctx, input)
					return readErr
				})
			}
			w.f.now = now
			if !errors.Is(err, sampling.ErrNotFound) || raw != nil {
				t.Fatalf("unreadable input returned private bytes or lost its missing classification: %v", err)
			}
			after, err := w.f.store.InspectInput(t.Context(), input.Account, id, false)
			if err != nil || !reflect.DeepEqual(after, input) {
				t.Fatalf("refused read changed retained input metadata: %v", err)
			}
			if state == "unpublished" {
				w.uploadAll(t)
			}
			if state == "retired" {
				if _, err := w.f.db.Exec(t.Context(), `UPDATE policy_input_objects SET state='live' WHERE account_id=$1 AND id=$2`, input.Account, id); err != nil {
					t.Fatal(err)
				}
			}
			current, err := w.f.store.InspectInput(t.Context(), input.Account, id, false)
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.Do(t.Context(), w.f.db, tx.Options{NoRetry: true}, func(ctx context.Context) error {
				raw, err = w.samples.ReadDocument(ctx, current)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			var doc sampling.Payload
			if err := json.Unmarshal(raw, &doc); err != nil || !bytes.Contains(doc.Input, []byte("private-retained-input")) || doc.Outcome != policyapi.Deny {
				t.Fatalf("valid fenced publication did not recover its original OPA input and decision: %v", err)
			}
		})
	}
}

func TestAccEvidenceMalformedSealHeaderReturnsUnavailableWithoutPrivateBytes(t *testing.T) {
	w := newSampleWorld(t)
	receipt := w.evaluate(t).Receipts[0]
	w.uploadAll(t)
	svc := evidenceService(w, evidenceAuthz{})
	before, err := svc.ReadSample(t.Context(), w.f.principal, receipt.Policy, receipt.ID)
	if err != nil || !bytes.Contains(before.Input, []byte("private-retained-input")) {
		t.Fatalf("published sample control: %v", err)
	}
	input, err := w.f.store.InspectInput(t.Context(), w.f.principal.AccountID, receipt.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(w.root, string(objectstore.PolicyInputs), input.Key)
	sealed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	damaged := bytes.Clone(sealed)
	damaged[0] ^= 0xff
	if err := os.WriteFile(path, damaged, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.samples.Read(t.Context(), input.Account, receipt.ID); !errors.Is(err, seal.ErrFormat) {
		t.Fatalf("malformed header did not fail at the actual sealed-object opener: %v", err)
	}
	doc, err := svc.ReadSample(t.Context(), w.f.principal, receipt.Policy, receipt.ID)
	if !errors.Is(err, kernel.ErrUnavailable) || !reflect.DeepEqual(doc, policyapi.Sample{}) || strings.Contains(err.Error(), "private-retained") {
		t.Fatalf("malformed header exposed private bytes or backend context: %v", err)
	}
	var total, safe int
	if err := w.f.db.QueryRow(t.Context(), `SELECT count(*),count(*) FILTER(WHERE body='{"reason":"unavailable"}'::jsonb)
FROM audit_trail_entries WHERE account_id=$1 AND action='policy_evidence.read'`, input.Account).Scan(&total, &safe); err != nil || total != 1 || safe != 1 {
		t.Fatalf("malformed header lost its fixed post-rollback failure audit: %d/%d %v", total, safe, err)
	}
	if err := os.WriteFile(path, sealed, 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := svc.ReadSample(t.Context(), w.f.principal, receipt.Policy, receipt.ID)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("header failure changed the recoverable sample: %v", err)
	}
}

func TestAccEvidenceFailedCaptureReplacementPreservesThePublishedReader(t *testing.T) {
	for _, fault := range []struct{ name, table, predicate, stage string }{
		{"enqueue", "jobs", "kind <> 'policies.input-upload'", "enqueue capture upload"},
		{"binding", "policy_input_objects", "upload_job_id IS NULL", "bind capture upload job"},
	} {
		t.Run(fault.name, func(t *testing.T) {
			w := newSampleWorld(t)
			w.capturePolicy(t, rego.V1)
			w.captureEvaluate(t, "retained-before-failure")
			if err := w.samples.Upload(t.Context(), w.claim(t)); err != nil {
				t.Fatal(err)
			}
			current, pending := w.captureHead(t)
			if current == nil || pending != nil {
				t.Fatal("published capture control has no stable head")
			}
			svc := evidenceService(w, evidenceAuthz{})
			before, err := svc.ReadCapture(t.Context(), w.f.principal, current.String())
			if err != nil || before.Input == nil || !strings.Contains(*before.Input, "retained-before-failure") || strings.Contains(*before.Input, "capture-private-command") {
				t.Fatalf("published sanitized capture control: %v", err)
			}
			if _, err := w.f.db.Exec(
				t.Context(),
				`UPDATE policy_input_capture_heads SET reserved_at=statement_timestamp()-interval '2 minutes' WHERE account_id=$1`,
				w.f.principal.AccountID,
			); err != nil {
				t.Fatal(err)
			}
			snapshot := func() []byte {
				t.Helper()
				var state []byte
				if err := w.f.db.QueryRow(t.Context(), `SELECT jsonb_build_object(
 'heads',(SELECT jsonb_agg(to_jsonb(h) ORDER BY h.space_snapshot_id,h.capture_kind) FROM policy_input_capture_heads h WHERE h.account_id=$1),
 'inputs',(SELECT jsonb_agg(to_jsonb(i) ORDER BY i.id) FROM policy_input_objects i WHERE i.account_id=$1),
 'evaluations',(SELECT jsonb_agg(to_jsonb(e) ORDER BY e.id) FROM policy_evaluations e WHERE e.account_id=$1),
 'jobs',(SELECT jsonb_agg(to_jsonb(j) ORDER BY j.id) FROM jobs j WHERE j.account_id=$1),
 'quota',(SELECT jsonb_agg(to_jsonb(q) ORDER BY q.hour,q.bucket) FROM policy_sample_budgets q WHERE q.account_id=$1),
 'blobs',(SELECT jsonb_agg(to_jsonb(b) ORDER BY b.key) FROM blob_objects b WHERE b.store='policy-inputs' AND EXISTS(SELECT 1 FROM policy_input_objects i WHERE i.account_id=$1 AND i.object_key=b.key)))`, w.f.principal.AccountID).Scan(&state); err != nil {
					t.Fatal(err)
				}
				return state
			}
			original := snapshot()
			if _, err := w.f.db.Exec(t.Context(), "ALTER TABLE "+fault.table+" ADD CONSTRAINT refuse_capture_upload CHECK ("+fault.predicate+") NOT VALID"); err != nil {
				t.Fatal(err)
			}
			out, err := w.f.svc.Evaluate(t.Context(), fixtureEvaluationRequest(w.f, policyapi.Plan, evaluationInput(t, "plan-tracked-warn.json")))
			var violation *pgconn.PgError
			if !errors.As(err, &violation) || violation.Code != "23514" || violation.ConstraintName != "refuse_capture_upload" || !strings.Contains(err.Error(), fault.stage) ||
				out.Outcome == policyapi.Allow {
				t.Fatalf("capture replacement did not reach its intended real PostgreSQL failure: %v", err)
			}
			if !bytes.Equal(snapshot(), original) {
				t.Fatal("failed replacement changed the capture minute/head, staging, metadata, delivery, quota or blob lifecycle")
			}
			after, err := svc.ReadCapture(t.Context(), w.f.principal, current.String())
			if err != nil || !reflect.DeepEqual(after, before) {
				t.Fatalf("failed replacement damaged the existing capture reader: %v", err)
			}
			if _, err := w.f.db.Exec(t.Context(), "ALTER TABLE "+fault.table+" DROP CONSTRAINT refuse_capture_upload"); err != nil {
				t.Fatal(err)
			}
			w.captureEvaluate(t, "replacement-after-recovery")
			if err := w.samples.Upload(t.Context(), w.claim(t)); err != nil {
				t.Fatal(err)
			}
			old, err := svc.ReadCapture(t.Context(), w.f.principal, current.String())
			if err != nil || old.Input != nil {
				t.Fatalf("successful recovery did not retire the old advertised bytes: %v", err)
			}
			next, pending := w.captureHead(t)
			if next == nil || *next == *current || pending != nil {
				t.Fatal("recovered replacement did not publish a fresh stable capture")
			}
			fresh, err := svc.ReadCapture(t.Context(), w.f.principal, next.String())
			if err != nil || fresh.Input == nil || !strings.Contains(*fresh.Input, "replacement-after-recovery") || strings.Contains(*fresh.Input, "capture-private-command") {
				t.Fatalf("recovered capture did not publish sanitized replacement input: %v", err)
			}
		})
	}
}
