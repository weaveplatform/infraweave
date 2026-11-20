package combine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
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
	slices.SortFunc(out.Receipts, func(a, b policyapi.Receipt) int { return bytes.Compare(a.Policy[:], b.Policy[:]) })
	return out
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
