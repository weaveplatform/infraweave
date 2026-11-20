package combine

import (
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

// Plan combines independently evaluated PLAN programs.
func Plan(receipts []policyapi.Receipt) policyapi.Evaluation {
	out := evaluation(policyapi.Plan, receipts)
	if !out.HasPolicies {
		return out
	}
	out.Outcome = policyapi.Allow
	out.Plan = &policyapi.PlanDecision{Deny: []string{}, Warn: []string{}, Info: []string{}}
	for i := range out.Receipts {
		r := &out.Receipts[i]
		var d inputs.PlanDecision
		if r.Failure == nil &&
			(r.Type != policyapi.Plan || decode(r.Decision, &d) != nil || d.Deny.IsNull() || d.Warn.IsNull() || d.Info.IsNull() || !common(r, d.Flag, d.ResetFlag, d.Sample, false)) {
			invalid(r, policyapi.Plan)
		}
		if r.Failure != nil {
			failed(&out, r, policyapi.Deny)
			out.Outcome = policyapi.Deny
			continue
		}
		deny, _ := d.Deny.Get()
		warn, _ := d.Warn.Get()
		info, _ := d.Info.Get()
		r.Outcome = policyapi.Allow
		if len(deny) > 0 {
			r.Outcome = policyapi.Deny
			out.Outcome = policyapi.Deny
		}
		out.Plan.Deny = append(out.Plan.Deny, deny...)
		out.Plan.Warn = append(out.Plan.Warn, warn...)
		out.Plan.Info = append(out.Plan.Info, info...)
	}
	out.Plan.Deny = unique(out.Plan.Deny)
	out.Plan.Warn = unique(out.Plan.Warn)
	out.Plan.Info = unique(out.Plan.Info)
	return out
}
