package pg_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/rego"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
)

func TestAccEvidenceMetadataSnapshotsActualInputWithoutSampleOrObjectRead(t *testing.T) {
	w := newSampleWorld(t)
	if _, err := w.f.svc.Create(
		t.Context(),
		w.f.principal,
		core.Input{Name: "Metadata evidence", Type: rego.Plan, Body: "package infraweave\nsample := false\n", Labels: []string{"autoattach:*"}},
	); err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(evaluationInput(t, "plan-tracked-warn.json"), &document); err != nil {
		t.Fatal(err)
	}
	context := document["infraweave"].(map[string]any)
	run := context["run"].(map[string]any)
	run["type"], run["drift_detection"], run["command"] = "TRACKED", false, "private-evidence-command"
	stack := context["stack"].(map[string]any)
	stack["name"], stack["repository"] = "Original evaluated stack", "original-repository"
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	out, err := w.f.svc.Evaluate(t.Context(), fixtureEvaluationRequest(w.f, policyapi.Plan, raw))
	if err != nil || len(out.Receipts) != 1 || out.Receipts[0].SampleEvaluation != nil {
		t.Fatalf("actual unsampled evaluation: %+v %v", out, err)
	}
	// No upload has run. Searchable metadata already belongs to the same
	// committed evaluation, independent of any readable sample/capture.
	w.f.stacks.stack.Name = "Current mutable stack name"
	var runType, name, repository, metadata string
	var drift *bool
	var sampled bool
	if err := w.f.db.QueryRow(t.Context(), `SELECT run_type,run_drift_detection,stack_name,stack_repository,sample_input_id IS NOT NULL,to_jsonb(e)::text FROM policy_evaluations e WHERE account_id=$1 AND id=$2`, w.f.principal.AccountID, out.Receipts[0].ID).
		Scan(&runType, &drift, &name, &repository, &sampled, &metadata); err != nil {
		t.Fatal(err)
	}
	if runType != "TRACKED" || drift == nil || *drift || name != "Original evaluated stack" || repository != "original-repository" || sampled ||
		bytes.Contains([]byte(metadata), []byte("private-evidence-command")) {
		t.Fatalf("metadata changed, lost false, became sampled or leaked private input: %s/%v/%s/%s", runType, drift, name, repository)
	}
	var blobs, budgets int
	if err := w.f.db.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM blob_objects WHERE store='policy-inputs'),(SELECT count(*) FROM policy_sample_budgets WHERE account_id=$1)`, w.f.principal.AccountID).
		Scan(&blobs, &budgets); err != nil ||
		blobs != 0 ||
		budgets != 0 {
		t.Fatalf("metadata depended on retained objects or quota: %d/%d %v", blobs, budgets, err)
	}
}
