package pg_test

import (
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
)

func TestAccProjectionsCountEveryRetainedSampleIndependentlyOfPreview(t *testing.T) {
	w := newSampleWorld(t)
	p, err := w.f.svc.Create(
		t.Context(),
		w.f.principal,
		core.Input{Name: "Full retained count", Type: rego.Plan, Body: "package infraweave\nsample := true\n", Labels: []string{"autoattach:*"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	req := fixtureEvaluationRequest(w.f, policyapi.Plan, evaluationInput(t, "plan-tracked-warn.json"))
	for i := range 501 {
		if i == 500 {
			// Model already populated prior UTC quota windows without changing the
			// production budget or waiting for a window. All evaluations and uploads
			// still use the real owner, queue, OPA, sealing and filesystem paths.
			if _, err := w.f.db.Exec(t.Context(), `UPDATE policy_sample_budgets SET hour=hour-interval '1 day' WHERE account_id=$1`, p.Account); err != nil {
				t.Fatal(err)
			}
		}
		got, err := w.f.svc.Evaluate(t.Context(), req)
		if err != nil || len(got.Receipts) != 1 || got.Receipts[0].SampleEvaluation == nil {
			t.Fatalf("real sampled evaluation %d: %+v/%v", i, got, err)
		}
		w.uploadAll(t)
	}
	ids := []kernel.ULID{p.ID}
	projection, err := w.f.svc.ProjectionEvidence(t.Context(), w.f.principal, ids, nil, true)
	if err != nil || projection.SampleCounts[p.ID] != 501 || len(projection.Records[p.ID]) != 100 {
		t.Fatalf("count vs preview: %+v/%v", projection, err)
	}
	page, err := w.f.svc.SearchEvaluations(t.Context(), w.f.principal, p.ID.String(), search.Input{First: new(int32(100))})
	if err != nil || len(page.Edges) != 100 || !page.HasNext {
		t.Fatalf("shared metadata first page: %+v/%v", page, err)
	}
	for i, record := range projection.Records[p.ID] {
		if record != page.Edges[i].Node {
			t.Fatalf("equal-time preview/search order differs at %d: %+v/%+v", i, record, page.Edges[i].Node)
		}
	}
	// Physically remove the sealed payloads while leaving publication metadata
	// intact. Counts and safe previews must not open any private object.
	if err := os.RemoveAll(w.root); err != nil {
		t.Fatal(err)
	}
	after, err := w.f.svc.ProjectionEvidence(t.Context(), w.f.principal, ids, nil, true)
	if err != nil || !reflect.DeepEqual(projection, after) {
		t.Fatalf("metadata projection opened private data: %+v/%v", after, err)
	}
}

func TestAccProjectionsKeepMetadataSeparateFromRetentionLifecycleAndCurrentScope(t *testing.T) {
	w := newSampleWorld(t)
	receipt := w.evaluate(t).Receipts[0]
	ids := []kernel.ULID{receipt.Policy}
	check := func(want int64, records int) {
		t.Helper()
		got, err := w.f.svc.ProjectionEvidence(t.Context(), w.f.principal, ids, nil, true)
		if err != nil || got.SampleCounts[receipt.Policy] != want || len(got.Records[receipt.Policy]) != records {
			t.Fatalf("projection count/history want %d/%d: %+v/%v", want, records, got, err)
		}
	}
	check(0, 1) // staging cannot imply a retained sample
	w.uploadAll(t)
	check(1, 1)
	input, err := w.f.store.InspectInput(t.Context(), w.f.principal.AccountID, receipt.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, statement string
		args            []any
	}{
		{"elapsed input", `UPDATE policy_input_objects SET expires_at=$1 WHERE account_id=$2 AND id=$3`, []any{w.f.now, w.f.principal.AccountID, receipt.ID}},
		{"failed input", `UPDATE policy_input_objects SET state='failed' WHERE account_id=$1 AND id=$2`, []any{w.f.principal.AccountID, receipt.ID}},
		{"retired blob", `UPDATE blob_objects SET state='deleting' WHERE store='policy-inputs' AND key=$1`, []any{input.Key}},
		{"elapsed blob", `UPDATE blob_objects SET expires_at=$1 WHERE store='policy-inputs' AND key=$2`, []any{w.f.now, input.Key}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := w.f.db.Exec(t.Context(), test.statement, test.args...); err != nil {
				t.Fatal(err)
			}
			check(0, 1)
			if _, err := w.f.db.Exec(
				t.Context(),
				`UPDATE policy_input_objects SET state='live',expires_at=$1 WHERE account_id=$2 AND id=$3`,
				w.f.now.Add(7*24*time.Hour),
				w.f.principal.AccountID,
				receipt.ID,
			); err != nil {
				t.Fatal(err)
			}
			if _, err := w.f.db.Exec(
				t.Context(),
				`UPDATE blob_objects SET state='live',expires_at=$1 WHERE store='policy-inputs' AND key=$2`,
				w.f.now.Add(7*24*time.Hour),
				input.Key,
			); err != nil {
				t.Fatal(err)
			}
			check(1, 1)
		})
	}
	if _, err := w.f.db.Exec(t.Context(), `DELETE FROM blob_objects WHERE store='policy-inputs' AND key=$1`, input.Key); err != nil {
		t.Fatal(err)
	}
	check(0, 1)
	foreign := w.f.principal
	foreign.AccountID = kernel.NewAccountID()
	got, err := w.f.svc.ProjectionEvidence(t.Context(), foreign, ids, nil, true)
	if err != nil || len(got.SampleCounts) != 0 || len(got.Records) != 0 {
		t.Fatalf("foreign projection: %+v/%v", got, err)
	}
	root := w.f.principal.AccountID.ULID
	team := w.f.spaces.nodes[1].ID
	if _, err := w.f.db.Exec(t.Context(), `UPDATE policies SET space_id=$1 WHERE account_id=$2 AND id=$3`, team, w.f.principal.AccountID, receipt.Policy); err != nil {
		t.Fatal(err)
	}
	w.f.authz.readable = []kernel.ULID{root}
	check(0, 0)
	w.f.authz.readable = []kernel.ULID{team}
	check(0, 1)
	if _, err := w.f.db.Exec(t.Context(), `DELETE FROM policies WHERE account_id=$1 AND id=$2`, w.f.principal.AccountID, receipt.Policy); err != nil {
		t.Fatal(err)
	}
	check(0, 0)
}

func TestAccProjectionAttachmentHistoryBindsActualStackAndIncludesUnsampledEvaluations(t *testing.T) {
	w := newSampleWorld(t)
	p, err := w.f.svc.Create(
		t.Context(),
		w.f.principal,
		core.Input{Name: "Unsampled history", Type: rego.Plan, Body: "package infraweave\nsample := false\n", Labels: []string{"autoattach:*"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	req := fixtureEvaluationRequest(w.f, policyapi.Plan, evaluationInput(t, "plan-tracked-warn.json"))
	first, err := w.f.svc.Evaluate(t.Context(), req)
	if err != nil || len(first.Receipts) != 1 {
		t.Fatalf("first stack: %+v/%v", first, err)
	}
	original := w.f.stacks.stack.ID
	other := kernel.NewULID()
	w.f.stacks.stack.ID = other
	req.Target.ID = other
	second, err := w.f.svc.Evaluate(t.Context(), req)
	if err != nil || len(second.Receipts) != 1 {
		t.Fatalf("other stack: %+v/%v", second, err)
	}
	for _, receipt := range []policyapi.Receipt{first.Receipts[0], second.Receipts[0]} {
		stack := req.Target.ID
		if receipt.ID == first.Receipts[0].ID {
			stack = original
		}
		got, err := w.f.svc.ProjectionEvidence(t.Context(), w.f.principal, []kernel.ULID{p.ID}, &stack, false)
		if err != nil || len(got.Records[p.ID]) != 1 || got.Records[p.ID][0].ID != receipt.ID || len(got.SampleCounts) != 0 {
			t.Fatalf("stack-specific history: %+v/%v", got, err)
		}
	}
	missing := kernel.NewULID()
	got, err := w.f.svc.ProjectionEvidence(t.Context(), w.f.principal, []kernel.ULID{p.ID}, &missing, false)
	if err != nil || len(got.Records) != 0 {
		t.Fatalf("unrelated stack history: %+v/%v", got, err)
	}
	w.f.now = w.f.now.Add(7 * 24 * time.Hour)
	got, err = w.f.svc.ProjectionEvidence(t.Context(), w.f.principal, []kernel.ULID{p.ID}, nil, true)
	if err != nil || len(got.Records) != 0 || len(got.SampleCounts) != 0 {
		t.Fatalf("old history should leave bounded preview: %+v/%v", got, err)
	}
}

func TestAccProjectionFailuresDoNotBecomeZeroCounts(t *testing.T) {
	w := newSampleWorld(t)
	r := w.evaluate(t).Receipts[0]
	w.uploadAll(t)
	if _, err := w.f.db.Exec(t.Context(), `ALTER TABLE blob_objects RENAME TO unavailable_blob_metadata`); err != nil {
		t.Fatal(err)
	}
	_, err := w.f.svc.ProjectionEvidence(t.Context(), w.f.principal, []kernel.ULID{r.Policy}, nil, true)
	if cause, ok := errors.AsType[*pgconn.PgError](err); !ok || cause.Code != "42P01" {
		t.Fatalf("unavailable blob owner became empty count: %v", err)
	}
	// Metadata-only attachment history does not depend on blob availability.
	if got, err := w.f.svc.ProjectionEvidence(t.Context(), w.f.principal, []kernel.ULID{r.Policy}, nil, false); err != nil || len(got.Records[r.Policy]) != 1 {
		t.Fatalf("safe history depended on blob owner: %+v/%v", got, err)
	}
}
