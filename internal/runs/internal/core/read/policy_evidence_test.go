package read

import (
	"context"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policiesapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/evidence"
)

type evidenceRead struct {
	evidence.Store
	snapshots map[kernel.ULID]evidence.Snapshot
	err       error
}

func (s evidenceRead) PolicyEvidence(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID]evidence.Snapshot, error) {
	return s.snapshots, s.err
}

type identitiesRead struct {
	account kernel.AccountID
	live    map[kernel.ULID]policiesapi.PolicyIdentity
	err     error
	calls   int
}

func (s *identitiesRead) Identities(_ context.Context, account kernel.AccountID, ids []kernel.ULID) (map[kernel.ULID]policiesapi.PolicyIdentity, error) {
	s.account = account
	s.calls++
	return s.live, s.err
}

func TestUnitPolicyReceiptReadKeepsHistoryAfterDeletion(t *testing.T) {
	account := kernel.NewAccountID()
	run, policy := kernel.NewULID(), kernel.NewULID()
	identity := &identitiesRead{live: map[kernel.ULID]policiesapi.PolicyIdentity{policy: {ID: policy, Name: "Renamed"}}}
	reader := New(Deps{Policies: identity, Evidence: evidenceRead{snapshots: map[kernel.ULID]evidence.Snapshot{
		run: {Receipts: []evidence.Receipt{{ID: kernel.NewULID(), Policy: policy, PolicyName: "Historical name", PolicyVersion: 1}}, Flags: []string{"owned"}},
	}}})
	for _, deleted := range []bool{false, true} {
		if deleted {
			identity.live = map[kernel.ULID]policiesapi.PolicyIdentity{}
		}
		out, err := reader.policyEvidence(t.Context(), account, []kernel.ULID{run})
		if err != nil || len(out[run].receipts) != 1 || len(out[run].flags) != 1 {
			t.Fatalf("receipt projection: %+v %v", out, err)
		}
		receipt := out[run].receipts[0]
		if receipt.Deleted != deleted || receipt.PolicyName != "Historical name" || receipt.PolicyVersion != 1 || identity.account != account {
			t.Fatalf("live lookup changed account or historical evidence: %+v %+v", receipt, identity)
		}
	}
}

func TestUnitPolicyReceiptReadFailures(t *testing.T) {
	run, policy := kernel.NewULID(), kernel.NewULID()
	store := evidenceRead{snapshots: map[kernel.ULID]evidence.Snapshot{run: {Receipts: []evidence.Receipt{{Policy: policy}}}}}
	for _, deps := range []Deps{
		{Evidence: evidenceRead{err: kernel.ErrUnavailable}},
		{Evidence: store},
		{Evidence: store, Policies: &identitiesRead{err: kernel.ErrUnavailable}},
	} {
		if _, err := New(deps).policyEvidence(t.Context(), kernel.NewAccountID(), []kernel.ULID{run}); !errors.Is(err, kernel.ErrUnavailable) {
			t.Fatalf("unavailable evidence became a deleted policy: %v", err)
		}
	}
	identity := &identitiesRead{}
	out, err := New(Deps{Policies: identity}).policyEvidence(t.Context(), kernel.NewAccountID(), []kernel.ULID{run})
	if err != nil || out[run].receipts == nil || out[run].flags == nil || identity.calls != 0 {
		t.Fatalf("empty evidence read a policy or returned null arrays: %+v %v", out, err)
	}
}
