package api_test

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/weaveplatform/infraweave/api"
	"github.com/weaveplatform/infraweave/internal/devtools/specref"
)

// parityProduct is one product's parity inputs: its reference, embedded,
// and its baseline, which make parity-baseline rewrites.
type parityProduct struct {
	product   specref.Product
	reference []byte
}

var (
	deployParity = parityProduct{specref.Deploy, api.SpaceliftReference}
	flowsParity  = parityProduct{specref.Flows, api.FlowsReference}
)

func (p parityProduct) baseline(t *testing.T) *specref.ParityBaseline {
	t.Helper()
	data, err := os.ReadFile(filepath.Base(p.product.Baseline))
	if err != nil {
		t.Fatal(err)
	}
	b, err := specref.ReadParityBaseline(data)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// registers reads decision 0001's rename table and the supersession
// register for the product.
func (p parityProduct) registers(t *testing.T) *specref.ParityRegisters {
	t.Helper()
	renames, err := os.ReadFile(filepath.Join("..", "docs", "data", "renames.json"))
	if err != nil {
		t.Fatal(err)
	}
	sups, err := os.ReadFile(filepath.Join("..", "docs", "data", "supersessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := specref.ReadParityRegisters(renames, sups, p.product.Name)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func (p parityProduct) compare(t *testing.T, served []byte) *specref.ParityReport {
	t.Helper()
	return p.compareWith(t, served, p.registers(t))
}

func (p parityProduct) compareWith(
	t *testing.T,
	served []byte,
	reg *specref.ParityRegisters,
) *specref.ParityReport {
	t.Helper()
	r, err := p.product.Parity(p.reference, served, reg)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// checkParity fails on a breach of decision 0016's rules (an unrecorded
// divergence, a served superseded operation), when the API loses a
// reference operation that the baseline does not accept as missing, and
// when the baseline lists an operation the comparison no longer needs, so
// coverage of the reference can only go up and the baseline shrinks with
// it.
func checkParity(t *testing.T, p parityProduct) {
	t.Helper()
	r := p.compare(t, api.Served)
	for _, s := range r.Failures {
		t.Errorf("parity failure: %s", s)
	}
	d := r.Diff(p.baseline(t))
	for _, s := range d.Regressions {
		t.Errorf("parity regression: %s", s)
	}
	for _, s := range d.Improvements {
		t.Errorf("parity improvement, apply with make parity-baseline: %s", s)
	}
	c := r.Counts()
	t.Logf("%d reference operations: %d served, %d served with divergence, %d superseded, "+
		"%d missing; %d infraweave-only",
		c.Reference, c.By[specref.OutcomeServed], c.By[specref.OutcomeDivergent],
		c.By[specref.OutcomeSuperseded], c.By[specref.OutcomeMissing], c.Only)
}

// TestUnitAPIParity checks the API against the Deploy reference.
func TestUnitAPIParity(t *testing.T) { checkParity(t, deployParity) }

// TestUnitFlowsParity checks the API against the Flows reference. Nothing
// Flows is served before P18c, so the baseline accepts every operation as
// missing; the test-scenario operations stay accepted until PD-16 is
// decided.
func TestUnitFlowsParity(t *testing.T) {
	checkParity(t, flowsParity)
	b := flowsParity.baseline(t)
	for _, id := range b.Missing["Flows Test Scenarios"] {
		if !strings.Contains(b.Reasons[id], "PD-16") {
			t.Errorf("%s is accepted as missing without naming PD-16: %q", id, b.Reasons[id])
		}
	}
}

type document = map[string]any

func decodeServed(t *testing.T) (document, document) {
	t.Helper()
	var doc document
	if err := yaml.Unmarshal(api.Served, &doc); err != nil {
		t.Fatal(err)
	}
	paths, _ := doc["paths"].(document)
	return doc, paths
}

func encode(t *testing.T, doc document) []byte {
	t.Helper()
	out, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// stacksPath is the first path of the API that holds a Stacks
// operation, with the operationIds under it.
func stacksPath(t *testing.T, paths document) (string, []string) {
	t.Helper()
	for _, p := range slices.Sorted(maps.Keys(paths)) {
		item := paths[p].(document)
		for _, v := range item {
			op, ok := v.(document)
			if tags, _ := op["tags"].([]any); ok && slices.Contains(tags, any("Stacks")) {
				return p, operationIDs(item)
			}
		}
	}
	t.Fatal("the API has no Stacks path")
	return "", nil
}

// operationIDs lists the operationIds of a path item's operations.
func operationIDs(item document) []string {
	var out []string
	for _, v := range item {
		if op, ok := v.(document); ok {
			if id, ok := op["operationId"].(string); ok {
				out = append(out, id)
			}
		}
	}
	return out
}

func mentions(lines []string, id string) bool {
	return slices.ContainsFunc(
		lines,
		func(s string) bool { return strings.Contains(s, ": "+id+" ") },
	)
}

// TestUnitAPIParityChecks proves the parity test against the committed
// documents: dropping a served operation, serving a reference operation the
// baseline lists as missing, moving one and serving a superseded one each
// change its verdict.
func TestUnitAPIParityChecks(t *testing.T) {
	baseline := deployParity.baseline(t)
	t.Run("removed operation is a regression", func(t *testing.T) {
		doc, paths := decodeServed(t)
		p, opIDs := stacksPath(t, paths)
		delete(paths, p)
		d := deployParity.compare(t, encode(t, doc)).Diff(baseline)
		for _, id := range opIDs {
			if !mentions(d.Regressions, id) {
				t.Errorf("removing %s does not name %s: %q", p, id, d.Regressions)
			}
		}
	})
	t.Run("served operation is an improvement until the baseline shrinks", func(t *testing.T) {
		var ref document
		if err := yaml.Unmarshal(api.SpaceliftReference, &ref); err != nil {
			t.Fatal(err)
		}
		doc, paths := decodeServed(t)
		listed := map[string]bool{}
		for _, opIDs := range baseline.Missing {
			for _, id := range opIDs {
				listed[id] = true
			}
		}
		// The first reference path the API lacks whose operations the
		// baseline all lists as missing.
		refPaths := ref["paths"].(document)
		var added string
		var addedIDs []string
		for _, p := range slices.Sorted(maps.Keys(refPaths)) {
			opIDs := operationIDs(refPaths[p].(document))
			unlisted := slices.ContainsFunc(opIDs, func(id string) bool { return !listed[id] })
			if paths[p] == nil && len(opIDs) > 0 && !unlisted {
				paths[p], added, addedIDs = refPaths[p], p, opIDs
				break
			}
		}
		if added == "" {
			t.Fatal("the baseline lists no reference path as missing")
		}
		r := deployParity.compare(t, encode(t, doc))
		d := r.Diff(baseline)
		if len(d.Regressions) != 0 {
			t.Errorf("serving %s: %s", added, d.Text())
		}
		for _, id := range addedIDs {
			if !mentions(d.Improvements, id) {
				t.Errorf("serving %s does not name %s: %q", added, id, d.Improvements)
			}
		}
		if d := r.Diff(r.Baseline(baseline)); !d.Clean() {
			t.Errorf("the rewritten baseline still differs: %s", d.Text())
		}
	})
	t.Run("move fails until x-infraweave-divergence records it", func(t *testing.T) {
		doc, paths := decodeServed(t)
		p, opIDs := stacksPath(t, paths)
		paths["/moved"+p] = paths[p]
		delete(paths, p)
		r := deployParity.compare(t, encode(t, doc))
		for _, id := range opIDs {
			if !mentions(r.Failures, id) {
				t.Errorf("moving %s does not name %s: %q", p, id, r.Failures)
			}
		}
		for _, v := range paths["/moved"+p].(document) {
			if op, ok := v.(document); ok {
				recorded, _ := op["x-infraweave-divergence"].([]any)
				op["x-infraweave-divergence"] = append(recorded, specref.DivergencePath)
			}
		}
		r = deployParity.compare(t, encode(t, doc))
		if len(r.Failures) != 0 {
			t.Errorf("a recorded move fails: %q", r.Failures)
		}
		for _, id := range opIDs {
			if op, _ := r.Operation(id); op.Outcome != specref.OutcomeDivergent {
				t.Errorf("%s is %s, want served with divergence", id, op.Outcome)
			}
		}
		if d := r.Diff(baseline); !d.Clean() {
			t.Errorf("a recorded move changes the baseline: %s", d.Text())
		}
	})
	t.Run("served superseded operation fails", func(t *testing.T) {
		_, paths := decodeServed(t)
		_, opIDs := stacksPath(t, paths)
		reg := deployParity.registers(t)
		reg.Superseded[opIDs[0]] = "SUP-99"
		r := deployParity.compareWith(t, api.Served, reg)
		if !slices.ContainsFunc(r.Failures, func(s string) bool {
			return strings.Contains(s, ": "+opIDs[0]+" ") &&
				strings.Contains(s, "is superseded by SUP-99")
		}) {
			t.Errorf("failures = %q", r.Failures)
		}
	})
}

// TestUnitFlowsParityChecks proves the Flows parity test against the
// committed documents: an operation the baseline stops accepting is a
// regression, and serving one it accepts is an improvement until the
// baseline shrinks.
func TestUnitFlowsParityChecks(t *testing.T) {
	t.Run("unlisted missing operation is a regression", func(t *testing.T) {
		b := flowsParity.baseline(t)
		b.Missing["Flows Projects"] = slices.DeleteFunc(
			b.Missing["Flows Projects"], func(id string) bool { return id == "listProjects" })
		d := flowsParity.compare(t, api.Served).Diff(b)
		if !mentions(d.Regressions, "listProjects") {
			t.Errorf("regressions = %q", d.Regressions)
		}
	})
	t.Run("served operation is an improvement", func(t *testing.T) {
		var ref document
		if err := yaml.Unmarshal(api.FlowsReference, &ref); err != nil {
			t.Fatal(err)
		}
		doc, paths := decodeServed(t)
		paths["/flows/projects"] = ref["paths"].(document)["/flows/projects"]
		d := flowsParity.compare(t, encode(t, doc)).Diff(flowsParity.baseline(t))
		want := "Flows Projects: listProjects is listed as missing, and is served now"
		if len(d.Regressions) != 0 || !slices.Contains(d.Improvements, want) {
			t.Errorf("diff = %s", d.Text())
		}
	})
}
