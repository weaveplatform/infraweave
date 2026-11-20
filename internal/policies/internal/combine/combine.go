package combine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

func evaluation(kind policyapi.Type, receipts []policyapi.Receipt) policyapi.Evaluation {
	out := policyapi.Evaluation{Type: kind, HasPolicies: len(receipts) > 0, Outcome: policyapi.NoPolicies, Receipts: []policyapi.Receipt{}, Failures: []policyapi.Failure{}}
	for _, r := range receipts {
		r.Decision = bytes.Clone(r.Decision)
		r.Flags = unique(r.Flags)
		r.ResetFlags = unique(r.ResetFlags)
		if r.Failure != nil {
			failure := *r.Failure
			r.Failure = &failure
		}
		out.Receipts = append(out.Receipts, r)
	}
	slices.SortStableFunc(out.Receipts, func(a, b policyapi.Receipt) int { return bytes.Compare(a.Policy[:], b.Policy[:]) })
	return out
}

func common(r *policyapi.Receipt, flags, reset inputs.Field[[]string], sample inputs.Field[bool], defaultSample bool) bool {
	if flags.IsNull() || reset.IsNull() || sample.IsNull() {
		return false
	}
	set, _ := flags.Get()
	clear, _ := reset.Get()
	r.Flags, r.ResetFlags = unique(set), unique(clear)
	value, present := sample.Get()
	r.Sample = value || !present && defaultSample
	return true
}

func invalid(r *policyapi.Receipt, kind policyapi.Type) {
	r.Failure = &policyapi.Failure{Code: "RESULT", Message: "Policy returned an invalid " + string(kind) + " decision."}
}

func failed(out *policyapi.Evaluation, r *policyapi.Receipt, fallback policyapi.Outcome) {
	r.Outcome = fallback
	r.Flags, r.ResetFlags = []string{}, []string{}
	r.Sample = out.Type == policyapi.Login
	out.Failures = append(out.Failures, *r.Failure)
}

func unique(values []string) []string {
	out := slices.Clone(values)
	if out == nil {
		return []string{}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

var errDecision = errors.New("invalid policy decision document")

func decode(raw json.RawMessage, into any) error {
	data := bytes.TrimSpace(raw)
	if len(data) == 0 || data[0] != '{' {
		return errDecision
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(into); err != nil {
		return fmt.Errorf("decode policy decision: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return errDecision
	}
	return nil
}
