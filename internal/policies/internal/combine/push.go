package combine

import (
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

// Push combines directives without creating runs, updating heads or checks.
func Push(receipts []policyapi.Receipt) policyapi.Evaluation {
	out := evaluation(policyapi.Push, receipts)
	if !out.HasPolicies {
		return out
	}
	out.Push = &policyapi.PushDecision{Cancel: []string{}, Messages: []string{}, ExternalDependencies: []string{}}
	p := out.Push
	for i := range out.Receipts {
		r := &out.Receipts[i]
		d, priority := pushProgram(r, p)
		if r.Failure != nil {
			failed(&out, r, policyapi.Ignore)
			continue
		}
		if priority != "" {
			p.Priority = priority
		}
		if lock := value(d.Lock); lock != "" {
			p.Lock = lock
		}
		if unlock := value(d.Unlock); unlock != "" {
			p.Unlock = unlock
		}
		if version := value(d.ModuleVersion); version != "" {
			p.ModuleVersion = version
		}
		p.Track = p.Track || value(d.Track)
		p.Propose = p.Propose || value(d.Propose)
		p.Ignore = p.Ignore || value(d.Ignore)
		p.IgnoreTrack = p.IgnoreTrack || value(d.IgnoreTrack)
		p.NoTrigger = p.NoTrigger || value(d.Notrigger)
		p.Notify = p.Notify || value(d.Notify)
		p.Fail = p.Fail || value(d.Fail)
		p.AllowFork = p.AllowFork || value(d.AllowFork)
		p.Cancel = append(p.Cancel, value(d.Cancel)...)
		p.Messages = append(p.Messages, value(d.Message)...)
		p.ExternalDependencies = append(p.ExternalDependencies, value(d.ExternalDependency)...)
		r.Outcome = policyapi.Ignore
		if !value(d.Ignore) && (value(d.Track) && !value(d.IgnoreTrack) && !value(d.Notrigger) || value(d.Propose)) {
			r.Outcome = policyapi.PushRun
		}
	}
	p.Cancel, p.Messages, p.ExternalDependencies = unique(p.Cancel), unique(p.Messages), unique(p.ExternalDependencies)
	if p.Priority == "" {
		p.Priority = "normal"
	}
	if p.IgnoreTrack {
		p.Track = false
	}
	if len(out.Failures) > 0 {
		*p = policyapi.PushDecision{
			Ignore:               true,
			Fail:                 true,
			FailedCheck:          true,
			Notify:               true,
			Priority:             "normal",
			Messages:             p.Messages,
			Cancel:               []string{},
			ExternalDependencies: []string{},
		}
	}
	if p.Ignore {
		p.Track, p.Propose = false, false
	}
	out.Outcome = policyapi.Ignore
	if p.Track && !p.NoTrigger || p.Propose {
		out.Outcome = policyapi.PushRun
	}
	return out
}

func pushProgram(r *policyapi.Receipt, p *policyapi.PushDecision) (inputs.PushDecision, string) {
	var d inputs.PushDecision
	if r.Failure == nil &&
		(r.Type != policyapi.Push || decode(r.Decision, &d) != nil || anyNull(d.AllowFork, d.Cancel, d.ExternalDependency, d.Fail, d.Ignore, d.IgnoreTrack, d.Lock, d.Message, d.ModuleVersion, d.Notify, d.Notrigger, d.Prioritize, d.Priority, d.Propose, d.Track, d.Unlock) || !common(r, d.Flag, d.ResetFlag, d.Sample, false)) {
		invalid(r, policyapi.Push)
	}
	priority := value(d.Priority)
	if r.Failure == nil && (!validPriority(priority) || priority != "" && value(d.Prioritize)) {
		invalid(r, policyapi.Push)
	}
	if value(d.Prioritize) {
		priority = "high"
	}
	if r.Failure == nil && (disagree(p.Priority, priority) || disagree(p.Lock, value(d.Lock))) {
		r.Failure = &policyapi.Failure{Code: "CONFLICT", Message: "Push policies disagree on lock or priority."}
	}
	return d, priority
}

func validPriority(p string) bool { return p == "" || p == "high" || p == "normal" || p == "low" }
func disagree(a, b string) bool   { return a != "" && b != "" && a != b }

func value[T any](field inputs.Field[T]) T { v, _ := field.Get(); return v }
func anyNull(fields ...interface{ IsNull() bool }) bool {
	for _, f := range fields {
		if f.IsNull() {
			return true
		}
	}
	return false
}
