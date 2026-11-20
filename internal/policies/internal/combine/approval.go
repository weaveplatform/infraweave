package combine

import (
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

// Approval resolves per-policy gates before applying rejection precedence.
func Approval(receipts []policyapi.Receipt) policyapi.Evaluation {
	out := evaluation(policyapi.Approval, receipts)
	if !out.HasPolicies {
		return out
	}
	out.Approval = &policyapi.ApprovalDecision{ApproveNotes: []string{}, RejectNotes: []string{}}
	allApprove, anyReject := true, false
	for i := range out.Receipts {
		r := &out.Receipts[i]
		var d inputs.ApprovalDecision
		if r.Failure == nil && (r.Type != policyapi.Approval || decode(r.Decision, &d) != nil || d.Approve.IsNull() || d.Reject.IsNull() || d.ApproveWithNote.IsNull() || d.RejectWithNote.IsNull() || !common(r, d.Flag, d.ResetFlag, d.Sample, false)) {
			invalid(r, policyapi.Approval)
		}
		if r.Failure != nil {
			failed(&out, r, policyapi.Undecided)
			allApprove = false
			continue
		}
		approve, _ := d.Approve.Get()
		reject, _ := d.Reject.Get()
		approveNotes, _ := d.ApproveWithNote.Get()
		rejectNotes, _ := d.RejectWithNote.Get()
		approve = approve || len(approveNotes) > 0
		reject = reject || len(rejectNotes) > 0
		r.Outcome = policyapi.Undecided
		// A conflicting individual program is undecided. Resolved rejections
		// then defeat approvals from every other program (report 09 §3.3).
		if approve && !reject {
			r.Outcome = policyapi.Approve
		}
		if reject && !approve {
			r.Outcome = policyapi.Reject
			anyReject = true
		}
		allApprove = allApprove && r.Outcome == policyapi.Approve
		out.Approval.ApproveNotes = append(out.Approval.ApproveNotes, approveNotes...)
		out.Approval.RejectNotes = append(out.Approval.RejectNotes, rejectNotes...)
	}
	out.Outcome = policyapi.Undecided
	if anyReject {
		out.Outcome = policyapi.Reject
		out.Approval.Reject = true
	} else if allApprove {
		out.Outcome = policyapi.Approve
		out.Approval.Approve = true
	}
	out.Approval.ApproveNotes = unique(out.Approval.ApproveNotes)
	out.Approval.RejectNotes = unique(out.Approval.RejectNotes)
	return out
}
