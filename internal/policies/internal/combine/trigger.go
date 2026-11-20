package combine

import (
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

// Trigger keeps a priority conflict nonfatal while recording its notification.
func Trigger(receipts []policyapi.Receipt) policyapi.Evaluation {
	out := evaluation(policyapi.Trigger, receipts)
	if !out.HasPolicies {
		return out
	}
	out.Trigger = &policyapi.TriggerDecision{Targets: []string{}}
	conflict, fatal := false, false
	for i := range out.Receipts {
		r := &out.Receipts[i]
		var d inputs.TriggerDecision
		if r.Failure == nil &&
			(r.Type != policyapi.Trigger || decode(r.Decision, &d) != nil || anyNull(d.Trigger, d.Priority) || !validPriority(value(d.Priority)) || !common(r, d.Flag, d.ResetFlag, d.Sample, false)) {
			invalid(r, policyapi.Trigger)
		}
		if r.Failure != nil {
			failed(&out, r, policyapi.Ignore)
			fatal = true
			continue
		}
		priority := value(d.Priority)
		conflict = conflict || disagree(out.Trigger.Priority, priority)
		if priority != "" {
			out.Trigger.Priority = priority
		}
		targets := value(d.Trigger)
		r.Outcome = policyapi.Ignore
		if len(targets) > 0 {
			r.Outcome = policyapi.TriggerRuns
		}
		out.Trigger.Targets = append(out.Trigger.Targets, targets...)
	}
	out.Trigger.Targets = unique(out.Trigger.Targets)
	if conflict {
		out.Trigger.Priority = "normal"
		out.Failures = append(out.Failures, policyapi.Failure{Code: "PRIORITY_CONFLICT", Message: "Trigger policies disagree on priority; using normal."})
	}
	if out.Trigger.Priority == "" {
		out.Trigger.Priority = "normal"
	}
	if fatal {
		out.Trigger.Targets = []string{}
	}
	out.Outcome = policyapi.Ignore
	if len(out.Trigger.Targets) > 0 {
		out.Outcome = policyapi.TriggerRuns
	}
	return out
}
