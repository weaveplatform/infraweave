package http

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policiesapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/evidence"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
)

func TestUnitPolicyReceiptWireProjection(t *testing.T) {
	policy := kernel.NewULID()
	receipt := read.PolicyReceipt{Receipt: evidence.Receipt{
		ID: kernel.NewULID(), Policy: policy, PolicyName: "Historical", PolicyVersion: 7,
		Type: policiesapi.Approval, Engine: "REGO_V1", Outcome: policiesapi.Undecided,
		SampleRequested: true, CreatedAt: time.Unix(1_800_000_000, 0),
		Failure: &policiesapi.Failure{Code: "EVALUATION", Message: "Policy evaluation failed."},
	}}
	for _, deleted := range []bool{false, true} {
		receipt.Deleted = deleted
		out := receiptsOf([]read.PolicyReceipt{receipt})
		if len(out) != 1 || out[0].PolicyDeleted != deleted || (out[0].PolicySlug == nil) != deleted || out[0].Sampled || out[0].SampleExpired || out[0].Flags == nil {
			t.Fatalf("receipt metadata or sample availability changed: %+v", out)
		}
		value := out[0]
		if value.PolicyVersion == nil || *value.PolicyVersion != 7 || value.PolicyName != "Historical" || value.CreatedAt != 1_800_000_000 || value.Error == nil ||
			value.Error.Code != "EVALUATION" {
			t.Fatalf("receipt lost committed metadata: %+v", value)
		}
		if value.PolicySlug != nil && *value.PolicySlug != policy.String() {
			t.Fatalf("live policy reference changed: %s", *value.PolicySlug)
		}
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{`"input":`, `"print":`, `"body":`, `"decision":`} {
			if strings.Contains(string(body), forbidden) {
				t.Fatalf("run receipt exposes private evaluation material: %s", body)
			}
		}
	}
	if out := receiptsOf(nil); out == nil || len(out) != 0 {
		t.Fatalf("empty receipt projection: %+v", out)
	}
}
