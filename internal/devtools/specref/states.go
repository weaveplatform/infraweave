package specref

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// States computes decision 0016's availability state for each record of
// docs/data/capabilities.json from the parity report and the last
// acceptance results. A scenario names the operations it exercises with
// @op:<operationId>, its kind with @case:success, @case:denial or
// @case:failure, and a Terraform provider scenario names its record with
// @provider:<record id>, such as @provider:resource:spacelift_stack.

// Scenario tag prefixes and the three kinds of the evidence rule. An @op:
// tag may name the reference operationId or the one infraweave serves.
const (
	TagOperation = "@op:"
	TagCase      = "@case:"
	TagProvider  = "@provider:"

	CaseSuccess = "success"
	CaseDenial  = "denial"
	CaseFailure = "failure"
)

// State is an availability state of decision 0021.
type State string

const (
	StateImplemented State = "implemented"
	StatePartial     State = "partial"
	StateStub        State = "stub"
	StateMissing     State = "missing"
	StateSuperseded  State = "superseded"
)

// rank orders the states by how much they claim. A superseded record claims
// nothing the acceptance results could show, so it ranks with missing.
func (s State) rank() int {
	switch s {
	case StateImplemented:
		return 3
	case StatePartial:
		return 2
	case StateStub:
		return 1
	}
	return 0
}

// Scenario is one scenario of an acceptance run: its feature's URI, its
// name, its tags with the feature's, and whether every step passed.
type Scenario struct {
	URI, Name string
	Tags      []string
	Passed    bool
}

// ReadCucumber decodes godog's cucumber JSON report. A scenario passes when
// it has steps and every one passed; a skipped, pending or undefined step
// fails it.
func ReadCucumber(data []byte) ([]Scenario, error) {
	type tag struct {
		Name string `json:"name"`
	}
	var features []struct {
		URI      string `json:"uri"`
		Tags     []tag  `json:"tags"`
		Elements []struct {
			Name  string `json:"name"`
			Type  string `json:"type"`
			Tags  []tag  `json:"tags"`
			Steps []struct {
				Result struct {
					Status string `json:"status"`
				} `json:"result"`
			} `json:"steps"`
		} `json:"elements"`
	}
	if err := json.Unmarshal(data, &features); err != nil {
		return nil, fmt.Errorf("specref: decode cucumber report: %w", err)
	}
	var out []Scenario
	for _, f := range features {
		for _, e := range f.Elements {
			if e.Type != "scenario" {
				continue
			}
			s := Scenario{URI: f.URI, Name: e.Name, Passed: len(e.Steps) > 0}
			for _, t := range slices.Concat(f.Tags, e.Tags) {
				if !slices.Contains(s.Tags, t.Name) {
					s.Tags = append(s.Tags, t.Name)
				}
			}
			for _, st := range e.Steps {
				if st.Result.Status != "passed" {
					s.Passed = false
				}
			}
			out = append(out, s)
		}
	}
	return out, nil
}

// CapabilityRecord is the part of a capabilities.json record the state check
// reads: its id, such as tag:Stacks or resource:spacelift_stack, the state
// it claims and, for a Terraform row the operation map misses, the root
// fields it calls.
type CapabilityRecord struct {
	ID    string
	State State
	Calls []string
}

// ReadCapabilities lists the records of docs/data/capabilities.json that
// belong to product and claim a state: its tag records and, for every
// product, its operation, resource and data-source records.
func ReadCapabilities(data []byte, product string) ([]CapabilityRecord, error) {
	var reg struct {
		Records []struct {
			ID        string `json:"id"`
			Product   string `json:"product"`
			Spacelift struct {
				Calls []string `json:"calls"`
			} `json:"spacelift"`
			Infraweave struct {
				State State `json:"state"`
			} `json:"infraweave"`
		} `json:"records"`
	}
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("%w: capabilities: %w", ErrRegister, err)
	}
	var out []CapabilityRecord
	for _, r := range reg.Records {
		if r.Product == product && r.Infraweave.State != "" {
			out = append(out, CapabilityRecord{
				ID:    r.ID,
				State: r.Infraweave.State,
				Calls: r.Spacelift.Calls,
			})
		}
	}
	return out, nil
}

// ReadTerraformOperations decodes a reference's terraform-operations.json.
func ReadTerraformOperations(data []byte) (*TerraformOperations, error) {
	var t TerraformOperations
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("specref: decode %s: %w", FileTerraformOps, err)
	}
	return &t, nil
}

// CapabilityState is one record's recorded and computed states, and the
// reference operations the computed one rests on.
type CapabilityState struct {
	ID         string
	Recorded   State
	Computed   State
	Operations []string
}

// ClaimsMore reports whether the record claims more than the acceptance
// results show.
func (s CapabilityState) ClaimsMore() bool {
	return s.Recorded != StateSuperseded && s.Recorded.rank() > s.Computed.rank()
}

// evidence is what the passing scenarios show for one operation.
type evidence struct {
	cases map[string]bool
}

func (e evidence) complete() bool {
	return e.cases[CaseSuccess] && e.cases[CaseDenial] && e.cases[CaseFailure]
}

// States computes the state of every record from the report, the scenarios
// and, for Terraform records, the root fields tf records each calls; tf may
// be nil for a product without a provider in the register.
func (r *ParityReport) States(
	records []CapabilityRecord,
	scenarios []Scenario,
	tf *TerraformOperations,
) []CapabilityState {
	ops := map[string]OperationParity{}
	byTag := map[string][]string{}
	byField := map[string]string{}
	for _, t := range r.Tags {
		for _, op := range t.Operations {
			id := op.Reference.OperationID
			ops[id] = op
			byTag[t.Tag] = append(byTag[t.Tag], id)
			if f := op.Reference.RootField; f != "" {
				byField[f] = id
			}
		}
	}
	proven := map[string]evidence{}
	providers := map[string]bool{}
	for _, s := range scenarios {
		if !s.Passed {
			continue
		}
		var cases []string
		for _, t := range s.Tags {
			if c, ok := strings.CutPrefix(t, TagCase); ok {
				cases = append(cases, c)
			}
			if id, ok := strings.CutPrefix(t, TagProvider); ok {
				providers[id] = true
			}
		}
		for _, t := range s.Tags {
			if id, ok := strings.CutPrefix(t, TagOperation); ok {
				e := proven[id]
				if e.cases == nil {
					e.cases = map[string]bool{}
				}
				for _, c := range cases {
					e.cases[c] = true
				}
				proven[id] = e
			}
		}
	}
	opsOf := func(fields []string) []string {
		var out []string
		for _, f := range fields {
			if id, ok := byField[f]; ok && !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
		slices.Sort(out)
		return out
	}
	out := make([]CapabilityState, 0, len(records))
	for _, rec := range records {
		kind, name, _ := strings.Cut(rec.ID, ":")
		var ids []string
		terraform := false
		switch kind {
		case "tag":
			ids = byTag[name]
		case "operation":
			if _, ok := ops[name]; ok {
				ids = []string{name}
			}
		case "resource", "data-source":
			terraform = true
			calls := rec.Calls
			switch {
			case calls != nil:
			case tf != nil && kind == "resource":
				calls = tf.Resources[name]
			case tf != nil:
				calls = tf.DataSources[name]
			}
			ids = opsOf(calls)
		}
		st := CapabilityState{ID: rec.ID, Recorded: rec.State, Operations: ids}
		st.Computed = computeState(ids, ops, proven)
		if terraform && st.Computed == StateImplemented && !providers[rec.ID] {
			st.Computed = StatePartial
		}
		out = append(out, st)
	}
	return out
}

// computeState applies decision 0016's table to one record's operations.
// Superseded operations are left out; a record whose every operation is
// superseded is superseded, and one with no operations is missing.
func computeState(ids []string, ops map[string]OperationParity, proven map[string]evidence) State {
	var live []string
	for _, id := range ids {
		if ops[id].Outcome != OutcomeSuperseded {
			live = append(live, id)
		}
	}
	if len(live) == 0 {
		if len(ids) > 0 {
			return StateSuperseded
		}
		return StateMissing
	}
	implemented, routed := 0, 0
	for _, id := range live {
		op := ops[id]
		if op.Served == nil {
			continue
		}
		routed++
		// A scenario names the operation as infraweave serves it, which a
		// rename makes differ from the reference.
		e := evidence{cases: map[string]bool{}}
		for _, name := range []string{id, op.Served.OperationID} {
			for c := range proven[name].cases {
				e.cases[c] = true
			}
		}
		if e.complete() {
			implemented++
		}
	}
	switch {
	case implemented == len(live):
		return StateImplemented
	case implemented > 0:
		return StatePartial
	case routed == len(live):
		return StateStub
	}
	return StateMissing
}

// StateClaims lists, sorted, each record that claims more than its computed
// state.
func StateClaims(states []CapabilityState) []string {
	var out []string
	for _, s := range states {
		if s.ClaimsMore() {
			out = append(out, fmt.Sprintf(
				"%s claims %s, and the acceptance results show %s over %d operations",
				s.ID, s.Recorded, s.Computed, len(s.Operations)))
		}
	}
	slices.Sort(out)
	return out
}

// StatesText renders each record's states, one per line.
func StatesText(states []CapabilityState) string {
	var b strings.Builder
	for _, s := range states {
		mark := ""
		if s.ClaimsMore() {
			mark = "  claims more"
		}
		fmt.Fprintf(&b, "%s: recorded %s, computed %s%s\n", s.ID, s.Recorded, s.Computed, mark)
	}
	return b.String()
}
