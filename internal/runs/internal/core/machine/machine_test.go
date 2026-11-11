package machine_test

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
)

// register is docs/data/run-transitions.json.
const register = "../../../../../docs/data/run-transitions.json"

// record is one of the register's edges; runTypes is "all" or a list.
type record struct {
	ID       string          `json:"id"`
	From     *string         `json:"from"`
	To       string          `json:"to"`
	RunTypes json.RawMessage `json:"runTypes"`
}

// created lists the run types the register's "all" stands for.
var created = []kernel.RunType{
	kernel.RunTypeProposed, kernel.RunTypeTracked, kernel.RunTypeTask,
	kernel.RunTypeTesting, kernel.RunTypeDestroy,
}

func readRegister(t *testing.T) []record {
	t.Helper()
	b, err := os.ReadFile(register)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Records []record `json:"records"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Records
}

func (r record) from() kernel.RunState {
	if r.From == nil {
		return machine.Created
	}
	return kernel.RunState(*r.From)
}

func (r record) types(t *testing.T) []kernel.RunType {
	t.Helper()
	var all string
	if json.Unmarshal(r.RunTypes, &all) == nil && all == "all" {
		return created
	}
	var list []kernel.RunType
	if err := json.Unmarshal(r.RunTypes, &list); err != nil {
		t.Fatalf("%s: runTypes %s: %v", r.ID, r.RunTypes, err)
	}
	return list
}

// TestUnitTransitionRegister checks that the machine's edges are the
// register's, in its order, with the same states and run types, so an
// edge missing from either fails.
func TestUnitTransitionRegister(t *testing.T) {
	recs := readRegister(t)
	edges := machine.Edges()
	ids := func(n int, id func(int) string) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = id(i)
		}
		return out
	}
	want := ids(len(recs), func(i int) string { return recs[i].ID })
	got := ids(len(edges), func(i int) string { return edges[i].ID })
	if !slices.Equal(got, want) {
		t.Fatalf("edges %v, register %v", got, want)
	}
	for i, r := range recs {
		e := edges[i]
		types := slices.Clone(r.types(t))
		slices.Sort(types)
		gotTypes := slices.Clone(e.Types)
		slices.Sort(gotTypes)
		if e.From != r.from() || e.To != kernel.RunState(r.To) || !slices.Equal(gotTypes, types) {
			t.Errorf("%s = %s → %s for %v, register %s → %s for %v",
				r.ID, e.From, e.To, e.Types, r.from(), r.To, types)
		}
	}
}

// drivenBy is each driver's edges, transcribed apart from the machine:
// liveness fails a run out of each state a worker holds it in, and a stop
// is the API's action that the worker reports.
//
// ref: decision 0008 § "Who drives each transition"
var drivenBy = map[machine.Driver][]string{
	machine.DriverAPI:         {"T3", "T4", "T8", "T9", "T16", "T19", "T26", "T27", "T28"},
	machine.DriverCreation:    {"T1"},
	machine.DriverScheduler:   {"T2", "T4", "T5", "T6"},
	machine.DriverApproval:    {"T5", "T23", "T24", "T29"},
	machine.DriverWorkerClaim: {"T7", "T30", "T33"},
	machine.DriverWorkerReport: {
		"T10", "T11", "T12", "T13", "T14", "T15", "T16", "T18", "T19", "T32", "T35", "T36",
		"T37", "T38", "T39", "T40", "T41", "T42", "T43", "T44",
	},
	machine.DriverDecision: {"T17", "T18", "T20", "T21", "T22"},
	machine.DriverTimers:   {"T4", "T9", "T25", "T27", "T31", "T34"},
	machine.DriverLiveness: {"T11", "T15", "T18", "T36", "T38", "T41", "T43", "T44"},
}

// TestUnitTransitionTable checks every (type, from, to, driver): Allowed
// allows exactly the register's edges for their run types and drivenBy's
// drivers, PENDING included as a state that has none, and Next
// lists exactly the edges out of a state for a type.
func TestUnitTransitionTable(t *testing.T) {
	type key struct {
		typ      kernel.RunType
		from, to kernel.RunState
		driver   machine.Driver
	}
	want := map[key]string{}
	recs := readRegister(t)
	for _, r := range recs {
		for d, ids := range drivenBy {
			if !slices.Contains(ids, r.ID) {
				continue
			}
			for _, typ := range r.types(t) {
				want[key{typ, r.from(), kernel.RunState(r.To), d}] = r.ID
			}
		}
	}
	for d, ids := range drivenBy {
		for _, id := range ids {
			if !slices.ContainsFunc(recs, func(r record) bool { return r.ID == id }) {
				t.Errorf("%s drives %s, which the register lacks", d, id)
			}
		}
	}

	pending := kernel.RunState("PENDING")
	states := append(kernel.RunStates(), pending)
	allowed := 0
	for _, typ := range kernel.RunTypes() {
		for _, from := range append([]kernel.RunState{machine.Created}, states...) {
			var next []string
			for _, to := range states {
				for _, d := range machine.Drivers() {
					e, ok := machine.Allowed(typ, from, to, d)
					id, wantOK := want[key{typ, from, to, d}]
					if ok != wantOK || e.ID != id {
						t.Errorf("Allowed(%s, %q, %s, %s) = %s, %t; want %q, %t",
							typ, from, to, d, e.ID, ok, id, wantOK)
					}
					if ok {
						allowed++
						if !slices.Contains(next, e.ID) {
							next = append(next, e.ID)
						}
					}
				}
			}
			var got []string
			for _, e := range machine.Next(typ, from) {
				got = append(got, e.ID)
			}
			slices.SortFunc(next, byNumber)
			if !slices.Equal(got, next) {
				t.Errorf("Next(%s, %q) = %v, want %v", typ, from, got, next)
			}
		}
	}
	if allowed != len(want) {
		t.Errorf("allowed %d (type, from, to, driver), want %d", allowed, len(want))
	}
}

// byNumber orders edge IDs by their number, the register's order.
func byNumber(a, b string) int {
	var x, y int
	_, _ = fmt.Sscanf(a, "T%d", &x)
	_, _ = fmt.Sscanf(b, "T%d", &y)
	return x - y
}

// TestUnitTransitionNever checks that no edge enters UNKNOWN or PENDING or
// leaves UNKNOWN, that a PARSE run takes none, and that every state an
// edge names is a RunState.
func TestUnitTransitionNever(t *testing.T) {
	for _, e := range machine.Edges() {
		if e.To == kernel.RunStateUnknown || e.To == "PENDING" ||
			e.From == kernel.RunStateUnknown ||
			e.From == "PENDING" {
			t.Errorf("%s: %s → %s", e.ID, e.From, e.To)
		}
		if !e.To.Valid() || (e.From != machine.Created && !e.From.Valid()) {
			t.Errorf("%s names a state that is no RunState: %s → %s", e.ID, e.From, e.To)
		}
		if slices.Contains(e.Types, kernel.RunTypeParse) ||
			slices.Contains(e.Types, kernel.RunTypeUnknown) {
			t.Errorf("%s is taken by %v", e.ID, e.Types)
		}
		if len(e.Drivers) == 0 {
			t.Errorf("%s has no driver", e.ID)
		}
	}
	for _, s := range append(kernel.RunStates(), machine.Created) {
		if n := machine.Next(kernel.RunTypeParse, s); len(n) != 0 {
			t.Errorf("Next(PARSE, %q) = %+v", s, n)
		}
	}
}

// TestUnitTransitionGuards checks that a queued run becomes ready only
// past the stack lock, its approvals, its upstream runs, its external
// dependencies and a disabled stack, that a confirm needs its approvals,
// and that an edge handed out is a copy.
func TestUnitTransitionGuards(t *testing.T) {
	e, ok := machine.Allowed(kernel.RunTypeTracked, kernel.RunStateQueued, kernel.RunStateReady,
		machine.DriverScheduler)
	want := []machine.Guard{
		machine.GuardStackLockFree, machine.GuardApproved, machine.GuardUpstreamFinished,
		machine.GuardExternalSettled, machine.GuardStackEnabled,
	}
	if !ok || !slices.Equal(e.Guards, want) {
		t.Errorf("T2 = %+v, %t", e, ok)
	}
	e, ok = machine.Allowed(kernel.RunTypeTracked, kernel.RunStateUnconfirmed,
		kernel.RunStateConfirmed, machine.DriverAPI)
	if !ok || !slices.Equal(e.Guards, []machine.Guard{machine.GuardApproved}) {
		t.Errorf("T26 = %+v, %t", e, ok)
	}

	e.Guards[0], e.Types[0], e.Drivers[0] = "changed", "changed", "changed"
	edges := machine.Edges()
	edges[0].Types[0] = "changed"
	again, _ := machine.Allowed(kernel.RunTypeTracked, kernel.RunStateUnconfirmed,
		kernel.RunStateConfirmed, machine.DriverAPI)
	if again.Guards[0] != machine.GuardApproved || machine.Edges()[0].Types[0] == "changed" {
		t.Error("changing an edge handed out changed the machine")
	}
}
