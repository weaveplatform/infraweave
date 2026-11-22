package app

import (
	"bytes"
	"encoding/json"
	"maps"
	"reflect"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
)

const policyInputUploadKind = "policies.input-upload"

// Safe row identities and lifecycle metadata must remain byte-for-byte stable
// after an aborted transaction or a stale replica's retry, including capture cadence.
func policyEvidenceSnapshot(t *testing.T, w holdWorld) json.RawMessage {
	t.Helper()
	var out json.RawMessage
	err := w.q.QueryRow(t.Context(), `SELECT jsonb_build_object(
        'events',(SELECT jsonb_agg(jsonb_build_array(id,kind,subject_kind,subject_id,sequence) ORDER BY id) FROM events WHERE account_id=$1),
        'jobs',(SELECT jsonb_agg(jsonb_build_array(id,kind,state,generation,unique_key,payload) ORDER BY id) FROM jobs WHERE account_id=$1),
        'runs',(SELECT jsonb_agg(jsonb_build_array(id,state,state_version) ORDER BY id) FROM runs WHERE account_id=$1),
        'receipts',(SELECT jsonb_agg(jsonb_build_array(run_id,id,state_version,ordinal,policy_id,sample_requested,sample_evaluation_id) ORDER BY run_id,ordinal) FROM run_policy_receipts WHERE account_id=$1),
        'flags',(SELECT jsonb_agg(jsonb_build_array(run_id,policy_id,flag,created_at) ORDER BY run_id,policy_id,flag) FROM run_flags WHERE account_id=$1),
        'evaluations',(SELECT jsonb_agg(jsonb_build_array(id,policy_id,space_id,type,target_kind,target_id,run_id,sample_requested,sample_input_id,blob_key) ORDER BY id) FROM policy_evaluations WHERE account_id=$1),
        'inputs',(SELECT jsonb_agg(jsonb_build_array(id,state,space_snapshot_id,capture_kind,upload_job_id,created_at,expires_at,published_at,staging_sealed IS NOT NULL) ORDER BY id) FROM policy_input_objects WHERE account_id=$1),
        'captures',(SELECT jsonb_agg(jsonb_build_array(space_snapshot_id,capture_kind,current_input_id,pending_input_id,reserved_at) ORDER BY space_snapshot_id,capture_kind) FROM policy_input_capture_heads WHERE account_id=$1),
        'budgets',(SELECT jsonb_agg(jsonb_build_array(hour,bucket,used) ORDER BY hour,bucket) FROM policy_sample_budgets WHERE account_id=$1))`, w.acct).Scan(&out)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func assertPolicyEvidenceUnchanged(t *testing.T, w holdWorld, before json.RawMessage) {
	t.Helper()
	if after := policyEvidenceSnapshot(t, w); !bytes.Equal(before, after) {
		t.Fatalf("transaction changed owned evidence: before=%s after=%s", before, after)
	}
}

func policyJobKinds(t *testing.T, w holdWorld) map[string]int {
	t.Helper()
	rows, err := w.q.Query(t.Context(), `SELECT kind,count(*) FROM jobs WHERE account_id=$1 GROUP BY kind`, w.acct)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var kind string
		var count int
		if err := rows.Scan(&kind, &count); err != nil {
			t.Fatal(err)
		}
		out[kind] = count
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func assertPolicyJobsAdded(t *testing.T, w holdWorld, before map[string]int, events int) {
	t.Helper()
	want := maps.Clone(before)
	want[outbox.FanoutKind.Name] += events
	want[policyInputUploadKind]++
	if got := policyJobKinds(t, w); !maps.Equal(got, want) {
		t.Fatalf("owned job kinds: got %v want %v", got, want)
	}
}

func assertEvaluationCapture(t *testing.T, w holdWorld, req policyapi.EvaluationRequest, receipt policyapi.Receipt, space kernel.ULID) {
	t.Helper()
	var evaluations, inputs, heads, budgets int
	if err := w.q.QueryRow(t.Context(), `SELECT
        (SELECT count(*) FROM policy_evaluations WHERE account_id=$1),
        (SELECT count(*) FROM policy_input_objects WHERE account_id=$1),
        (SELECT count(*) FROM policy_input_capture_heads WHERE account_id=$1),
        (SELECT count(*) FROM policy_sample_budgets WHERE account_id=$1)`, w.acct).Scan(&evaluations, &inputs, &heads, &budgets); err != nil {
		t.Fatal(err)
	}
	if evaluations != 1 || inputs != 1 || heads != 1 || budgets != 0 {
		t.Fatalf("unsampled capture cardinality: evaluations=%d inputs=%d heads=%d budgets=%d", evaluations, inputs, heads, budgets)
	}
	var policy, ownedSpace, target kernel.ULID
	var kind, targetKind string
	var run, sample *kernel.ULID
	var requested bool
	var blob *string
	err := w.q.QueryRow(t.Context(), `SELECT policy_id,space_id,type,target_kind,target_id,run_id,sample_requested,sample_input_id,blob_key
        FROM policy_evaluations WHERE account_id=$1 AND id=$2`, w.acct, receipt.ID).
		Scan(&policy, &ownedSpace, &kind, &targetKind, &target, &run, &requested, &sample, &blob)
	if err != nil {
		t.Fatal(err)
	}
	if policy != receipt.Policy || ownedSpace != space || kind != string(req.Type) || targetKind != string(req.Target.Kind) || target != req.Target.ID ||
		!reflect.DeepEqual(run, req.Run) || requested || sample != nil || blob != nil || receipt.Sample || receipt.SampleEvaluation != nil {
		t.Fatal("evaluation lost its exact owner/target or retained an unrequested sample")
	}
	var input kernel.ULID
	var inputState, jobState, jobKind, jobQueue, unique string
	var staged bool
	var published *time.Time
	var payload json.RawMessage
	err = w.q.QueryRow(t.Context(), `SELECT i.id,i.state,i.staging_sealed IS NOT NULL,i.published_at,j.state,j.kind,j.queue,j.unique_key,j.payload
        FROM policy_input_capture_heads h JOIN policy_input_objects i ON i.account_id=h.account_id AND i.id=h.pending_input_id
        AND i.space_snapshot_id=h.space_snapshot_id AND i.capture_kind=h.capture_kind
        JOIN jobs j ON j.account_id=i.account_id AND j.id=i.upload_job_id
        WHERE h.account_id=$1 AND h.space_snapshot_id=$2 AND h.capture_kind=$3 AND h.current_input_id IS NULL`, w.acct, space, string(req.Type)).
		Scan(&input, &inputState, &staged, &published, &jobState, &jobKind, &jobQueue, &unique, &payload)
	if err != nil {
		t.Fatal(err)
	}
	var ref map[string]kernel.ULID
	if err := json.Unmarshal(payload, &ref); err != nil {
		t.Fatal(err)
	}
	if inputState != "pending" || !staged || published != nil || jobState != "pending" || jobKind != policyInputUploadKind || jobQueue != string(queue.Default) ||
		unique != input.String() || len(ref) != 1 || ref["id"] != input {
		t.Fatal("capture head, sealed staging and ID-only upload job do not share one owned input")
	}
}

func assertPolicyEventFanout(t *testing.T, w holdWorld, id kernel.ULID, kind, subjectKind, subject string, sequence int64) {
	t.Helper()
	var payload json.RawMessage
	var jobQueue, state string
	err := w.q.QueryRow(t.Context(), `SELECT j.payload,j.queue,j.state FROM events e JOIN jobs j ON j.account_id=e.account_id
        AND j.kind=$7 AND j.unique_key=$8 WHERE e.account_id=$1 AND e.id=$2 AND e.kind=$3
        AND e.subject_kind=$4 AND e.subject_id=$5 AND e.sequence=$6`, w.acct, id, kind, subjectKind, subject, sequence, outbox.FanoutKind.Name, id.String()).
		Scan(&payload, &jobQueue, &state)
	if err != nil {
		t.Fatal(err)
	}
	var ref map[string]kernel.ULID
	if err := json.Unmarshal(payload, &ref); err != nil {
		t.Fatal(err)
	}
	if jobQueue != string(queue.Events) || state != "pending" || len(ref) != 2 || ref["account"] != w.acct.ULID || ref["id"] != id {
		t.Fatal("event fanout does not carry its exact account and event identity")
	}
}
