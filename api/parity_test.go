package api_test

import (
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/weaveplatform/infraweave/api"
	"github.com/weaveplatform/infraweave/internal/devtools/specref"
)

// parityBaseline accepts the Deploy reference operations the API does not
// serve unchanged; make parity-baseline rewrites it.
const parityBaseline = "parity-baseline.deploy.json"

func readParityBaseline(t *testing.T) *specref.ParityBaseline {
	t.Helper()
	data, err := os.ReadFile(parityBaseline)
	if err != nil {
		t.Fatal(err)
	}
	b, err := specref.ReadParityBaseline(data)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func compareServed(t *testing.T, served []byte) *specref.ParityReport {
	t.Helper()
	r, err := specref.CompareParity(api.SpaceliftReference, served)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestUnitAPIParity fails when the API loses a reference operation
// that the baseline does not accept as missing or moved, when a move has no
// reason, and when the baseline lists an operation the comparison no longer
// needs, so coverage of the reference can only go up and the baseline
// shrinks with it.
func TestUnitAPIParity(t *testing.T) {
	r := compareServed(t, api.Served)
	d := r.Diff(readParityBaseline(t))
	for _, s := range d.Regressions {
		t.Errorf("parity regression: %s", s)
	}
	for _, s := range d.Improvements {
		t.Errorf("parity improvement, apply with make parity-baseline: %s", s)
	}
	c := r.Counts()
	t.Logf("%d reference operations: %d present, %d moved, %d missing; %d infraweave-only",
		c.Reference, c.Present, c.Moved, c.Missing, c.Only)
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
// baseline lists as missing and moving one each change its verdict.
func TestUnitAPIParityChecks(t *testing.T) {
	baseline := readParityBaseline(t)
	t.Run("removed operation is a regression", func(t *testing.T) {
		doc, paths := decodeServed(t)
		p, opIDs := stacksPath(t, paths)
		delete(paths, p)
		d := compareServed(t, encode(t, doc)).Diff(baseline)
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
		r := compareServed(t, encode(t, doc))
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
	t.Run("move passes once the baseline records its reason", func(t *testing.T) {
		doc, paths := decodeServed(t)
		p, opIDs := stacksPath(t, paths)
		paths["/moved"+p] = paths[p]
		delete(paths, p)
		r := compareServed(t, encode(t, doc))
		d := r.Diff(baseline)
		for _, id := range opIDs {
			if !mentions(d.Regressions, id) {
				t.Errorf("moving %s does not name %s: %q", p, id, d.Regressions)
			}
		}
		moved := r.Baseline(baseline)
		if len(moved.Moved) != len(opIDs) {
			t.Fatalf("baseline records %d moves, want %d", len(moved.Moved), len(opIDs))
		}
		if d := r.Diff(moved); d.Clean() {
			t.Error("a move without a reason passes")
		}
		for i := range moved.Moved {
			moved.Moved[i].Reason = "The fixture serves the path under /moved."
		}
		if d := r.Diff(moved); !d.Clean() {
			t.Errorf("a move with a reason fails: %s", d.Text())
		}
	})
}
