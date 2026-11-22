package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
)

// This composes the real access, stacks, policy, outbox and queue domains. The
// evaluator is a server port; workflow handlers adopt it in their owning tickets.
func TestAccPolicyEvaluatorUsesRealOwnerPortsAndCallerTransaction(t *testing.T) {
	w := newHoldWorld(t)
	if _, err := w.d.Access.API().
		SpaceUpdate(w.admin, api.SpaceUpdateRequestObject{SpaceId: "kept", Body: &api.SpaceInput{Name: "Kept", ParentSpace: "hold", InheritEntities: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.d.Stacks.API().StackCreate(w.admin, api.StackCreateRequestObject{Body: &api.StackCreateRequest{Input: api.StackInput{
		Name: "Evaluation target", Repository: "infra", Branch: "main", Provider: new(api.VCSProviderGIT), Space: new("kept"), Labels: &[]string{"prod"},
	}}}); err != nil {
		t.Fatal(err)
	}
	stack, err := w.d.Stacks.Stacks().BySlug(t.Context(), w.acct, "evaluation-target")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := w.d.Stacks.Stacks().Settings(t.Context(), w.acct, stack.ID)
	if err != nil {
		t.Fatal(err)
	}
	created, err := w.d.Policies.API().PolicyCreatev2(w.admin, api.PolicyCreatev2RequestObject{Body: &api.PolicyCreateInput{
		Name: "Inherited task gate", Type: api.PolicyTypeTASK, Space: new("hold"), Labels: &[]string{"autoattach:prod"},
		Body: "package infraweave\ndeny contains \"task blocked\" if true\nflag contains \"gate-seen\" if true\n",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := created.(api.PolicyCreatev2201JSONResponse); !ok {
		t.Fatalf("policy create: %T", created)
	}
	input, err := json.Marshal(map[string]any{
		"request": map[string]any{"command": "echo test", "timestamp_ns": int64(1795140000000000001)},
		"session": map[string]any{"machine": false, "teams": []string{}},
		"stack": map[string]any{
			"administrative": false,
			"autodeploy":     false,
			"branch":         settings.Branch,
			"id":             stack.Slug,
			"name":           stack.Name,
			"namespace":      settings.Namespace,
			"project_root":   "",
			"repository":     settings.Repository,
			"state":          "NONE",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := policyapi.EvaluationRequest{Account: w.acct, Type: policyapi.Task, Target: policyapi.Target{Kind: policyapi.StackTarget, ID: stack.ID}, Input: input}
	count := func(table string) int {
		t.Helper()
		var n int
		if table != "events" && table != "jobs" {
			t.Fatal("unknown count table")
		}
		if err := w.q.QueryRow(t.Context(), "SELECT count(*) FROM "+table+" WHERE account_id=$1", w.acct).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	beforeEvents, beforeJobs := count("events"), count("jobs")
	beforeEvidence, beforeKinds := policyEvidenceSnapshot(t, w), policyJobKinds(t, w)
	abort := errors.New("caller rollback")
	err = tx.Do(t.Context(), w.q, tx.Options{}, func(ctx context.Context) error {
		got, err := w.d.Policies.Evaluator().Evaluate(ctx, req)
		if err != nil {
			return err
		}
		if got.Outcome != policyapi.Deny || len(got.Receipts) != 1 || len(got.Receipts[0].Flags) != 1 {
			return fmt.Errorf("real inherited gate did not deny: %+v", got)
		}
		return abort
	})
	if !errors.Is(err, abort) || count("events") != beforeEvents || count("jobs") != beforeJobs {
		t.Fatalf("rollback leaked evaluator event/job: %v", err)
	}
	assertPolicyEvidenceUnchanged(t, w, beforeEvidence)
	got, err := w.d.Policies.Evaluator().Evaluate(t.Context(), req)
	if err != nil || got.Outcome != policyapi.Deny || len(got.Receipts) != 1 || count("events") != beforeEvents+1 {
		t.Fatalf("real composed evaluator: %+v %v", got, err)
	}
	assertPolicyJobsAdded(t, w, beforeKinds, 1)
	assertEvaluationCapture(t, w, req, got.Receipts[0], w.kept)
	assertPolicyEventFanout(t, w, got.Receipts[0].ID, policyapi.EvaluatedKind, "policy_evaluation", got.Receipts[0].ID.String(), 1)
	committed := policyEvidenceSnapshot(t, w)
	wrong := req
	wrong.Account = kernel.NewAccountID()
	if _, err := w.d.Policies.Evaluator().Evaluate(t.Context(), wrong); err == nil {
		t.Fatal("real stack port accepted a foreign account")
	}
	assertPolicyEvidenceUnchanged(t, w, committed)
}
