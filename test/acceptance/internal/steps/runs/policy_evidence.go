package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

type policyEvidenceSteps struct{ w *harness.World }

func (s *policyEvidenceSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the run "(\w+)" has a historical receipt fixture for policy "(\w+)"$`, s.fixture)
	sc.Step(`^the run "(\w+)"'s latest update carries only public policy evidence$`, s.event)
	sc.Step(`^the answer carries the historical receipt for policy "(\w+)", deleted (true|false)$`, s.receipt)
}

func (s *policyEvidenceSteps) receipt(policy, deleted string) error {
	var run api.Run
	if err := json.Unmarshal(s.w.Last().Body, &run); err != nil {
		return fmt.Errorf("%w: decode public run: %w", harness.ErrStep, err)
	}
	if len(run.PolicyReceipts) != 1 {
		return fmt.Errorf("%w: expected one historical receipt", harness.ErrStep)
	}
	r := run.PolicyReceipts[0]
	isDeleted := deleted == "true"
	if r.Id != s.w.IDs["receipt"] || r.CreatedAt <= 0 || r.PolicyName != "Snapshot guard" || r.PolicyType != api.PolicyTypeAPPROVAL ||
		r.EngineType != api.REGOV1 || r.Outcome != "APPROVE" || !reflect.DeepEqual(r.Flags, []string{"reviewed"}) ||
		r.Sampled || r.SampleExpired || r.PolicyDeleted != isDeleted || r.PolicyVersion == nil || *r.PolicyVersion != 1 || r.Error != nil {
		return fmt.Errorf("%w: historical receipt metadata changed", harness.ErrStep)
	}
	if isDeleted && r.PolicySlug != nil || !isDeleted && (r.PolicySlug == nil || *r.PolicySlug != s.w.IDs[policy]) {
		return fmt.Errorf("%w: live/deleted policy identity projection changed", harness.ErrStep)
	}
	return nil
}

// The fixture isolates read/event projection from the future approval/report
// integrations. The real evaluator and atomic writer are tested against PG in
// runs' storage suite; this does not pretend a public policy gate executed.
func (s *policyEvidenceSteps) fixture(ctx context.Context, run, policy string) error {
	runID, err := kernel.ParseULID(s.w.IDs[run])
	if err != nil {
		return fmt.Errorf("%w: run fixture ID: %w", harness.ErrStep, err)
	}
	policyID, err := kernel.ParseULID(s.w.IDs[policy])
	if err != nil {
		return fmt.Errorf("%w: policy fixture ID: %w", harness.ErrStep, err)
	}
	receipt := kernel.NewULID()
	s.w.IDs["receipt"] = receipt.String()
	_, err = s.w.Query(ctx, fmt.Sprintf(`BEGIN;
		INSERT INTO run_policy_receipts(account_id,run_id,id,state_version,ordinal,policy_id,policy_name,policy_version,
			policy_type,engine_type,outcome,decision,flags,reset_flags,sample_requested,created_at)
		SELECT r.account_id,r.id,%s,r.state_version,1,p.id,p.name,p.version,p.type,p.engine_type,'APPROVE',
			'{"private_input":"policy-evidence-private-marker","print":"policy-evidence-private-marker"}'::jsonb,
			ARRAY['reviewed'],ARRAY[]::text[],true,now()
		FROM runs r JOIN policies p ON p.account_id=r.account_id AND p.id=%s
		WHERE r.account_id=%s AND r.id=%s;
		INSERT INTO run_flags(account_id,run_id,policy_id,flag,created_at)
		SELECT account_id,run_id,policy_id,'reviewed',created_at FROM run_policy_receipts WHERE id=%s;
		COMMIT;`, harness.Literal(receipt.UUID()), harness.Literal(policyID.UUID()),
		harness.Literal(harness.UUIDOf(harness.Home.Account)), harness.Literal(runID.UUID()), harness.Literal(receipt.UUID())))
	if err != nil {
		return fmt.Errorf("%w: seed historical receipt: %w", harness.ErrStep, err)
	}
	return nil
}

func (s *policyEvidenceSteps) event(ctx context.Context, run string) error {
	raw, err := s.w.Query(ctx, `SELECT document::text FROM events WHERE account_id=`+
		harness.Literal(harness.UUIDOf(harness.Home.Account))+` AND kind='run_updated' AND subject_id=`+
		harness.Literal(s.w.IDs[run])+` ORDER BY sequence DESC LIMIT 1`)
	if err != nil {
		return fmt.Errorf("%w: read committed run event: %w", harness.ErrStep, err)
	}
	//nolint:tagliatelle // run_updated uses the recorded snake_case event contract.
	var document struct {
		Run struct {
			Flags []string `json:"flags"`
		} `json:"run"`
		Receipts []struct {
			Type    string   `json:"type"`
			Name    string   `json:"name"`
			Outcome string   `json:"outcome"`
			Flags   []string `json:"flags"`
		} `json:"policy_receipts"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &document); err != nil {
		return fmt.Errorf("%w: decode run event: %w", harness.ErrStep, err)
	}
	if !reflect.DeepEqual(document.Run.Flags, []string{"reviewed"}) || len(document.Receipts) != 1 ||
		document.Receipts[0].Name != "Snapshot guard" || document.Receipts[0].Outcome != "APPROVE" ||
		!reflect.DeepEqual(document.Receipts[0].Flags, []string{"reviewed"}) || strings.Contains(raw, "policy-evidence-private-marker") || strings.Contains(raw, `"decision"`) {
		return fmt.Errorf("%w: committed public evidence is incorrect", harness.ErrStep)
	}
	return nil
}
