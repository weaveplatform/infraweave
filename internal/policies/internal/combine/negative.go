package combine

import (
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

// Negative combines deny-list gates and Intent's explicit positive grants.
func Negative(kind policyapi.Type, receipts []policyapi.Receipt) policyapi.Evaluation {
	out := evaluation(kind, receipts)
	if !out.HasPolicies {
		return out
	}
	out.Negative = &policyapi.NegativeDecision{Allow: []string{}, Deny: []string{}}
	out.Outcome = policyapi.Allow
	if kind == policyapi.Intent {
		out.Outcome = policyapi.Undecided
	}
	for i := range out.Receipts {
		r := &out.Receipts[i]
		var d inputs.IntentDecision
		var valid bool
		switch kind {
		case policyapi.Intent:
			valid = decode(r.Decision, &d) == nil && !d.Allow.IsNull()
		case policyapi.Task, policyapi.Initialization:
			var negative inputs.TaskDecision
			valid = decode(r.Decision, &negative) == nil
			d.Deny, d.Flag, d.ResetFlag, d.Sample = negative.Deny, negative.Flag, negative.ResetFlag, negative.Sample
		default:
			valid = false
		}
		if r.Failure == nil && (r.Type != kind || !valid || d.Deny.IsNull() || !common(r, d.Flag, d.ResetFlag, d.Sample, false)) {
			invalid(r, kind)
		}
		if r.Failure != nil {
			failed(&out, r, policyapi.Deny)
			out.Outcome = policyapi.Deny
			continue
		}
		allow, _ := d.Allow.Get()
		deny, _ := d.Deny.Get()
		r.Outcome = policyapi.Allow
		if kind == policyapi.Intent && len(allow) == 0 {
			r.Outcome = policyapi.Undecided
		}
		if len(deny) > 0 {
			r.Outcome = policyapi.Deny
			out.Outcome = policyapi.Deny
		} else if len(allow) > 0 && out.Outcome != policyapi.Deny {
			out.Outcome = policyapi.Allow
		}
		out.Negative.Allow = append(out.Negative.Allow, allow...)
		out.Negative.Deny = append(out.Negative.Deny, deny...)
	}
	out.Negative.Allow = unique(out.Negative.Allow)
	out.Negative.Deny = unique(out.Negative.Deny)
	return out
}
