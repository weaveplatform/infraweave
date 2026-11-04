package specref

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// parityReference is a reference with two tags and an untagged operation.
const parityReference = `openapi: 3.0.3
paths:
  /stacks:
    get: {operationId: stacks, tags: [Stacks]}
    post: {operationId: stackCreate, tags: [Stacks]}
  /stacks/{id}:
    get: {operationId: stack, tags: [Stacks]}
    delete: {operationId: stackDelete, tags: [Stacks]}
  /stack/{id}/lock:
    post: {operationId: stackLock, tags: [Stacks]}
  /contexts:
    get: {operationId: contexts, tags: [Contexts]}
  /sessions:
    delete: {operationId: sessionDeleteAll}
`

// parityServed covers every outcome against parityReference: stacks and
// stackCreate present, stack present under a renamed path parameter,
// stackDelete renamed at its route, stackLock at another route, contexts and
// sessionDeleteAll missing, and two infraweave-only operations, one untagged.
const parityServed = `openapi: 3.0.3
paths:
  /stacks:
    parameters: []
    summary: path-level members are not operations
    get: {operationId: stacks, tags: [Stacks]}
    post: {operationId: stackCreate, tags: [Stacks]}
  /stacks/{stackId}:
    get: {operationId: stack, tags: [Stacks]}
    delete: {operationId: stackRemove, tags: [Stacks]}
  /stacks/{stackId}/lock:
    post: {operationId: stackLock, tags: [Stacks]}
  /organizations:
    get: {operationId: organizations, tags: [Organization]}
  /livez:
    get: {operationId: livez}
`

func compareFixture(t *testing.T) *ParityReport {
	t.Helper()
	r, err := CompareParity([]byte(parityReference), []byte(parityServed))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func ids(ops []ParityOperation) []string {
	out := make([]string, 0, len(ops))
	for _, o := range ops {
		out = append(out, o.OperationID)
	}
	return out
}

func tagParity(t *testing.T, r *ParityReport, tag string) TagParity {
	t.Helper()
	for _, tp := range r.Tags {
		if tp.Tag == tag {
			return tp
		}
	}
	t.Fatalf("no tag %q in the report", tag)
	return TagParity{}
}

func TestUnitParityCompare(t *testing.T) {
	r := compareFixture(t)
	var tags []string
	for _, tp := range r.Tags {
		tags = append(tags, tp.Tag)
	}
	if want := []string{"Contexts", "Organization", "Stacks", Untagged}; !slices.Equal(tags, want) {
		t.Errorf("tags %q, want %q", tags, want)
	}
	stacks := tagParity(t, r, "Stacks")
	if got, want := ids(
		stacks.Present,
	), []string{
		"stack",
		"stackCreate",
		"stacks",
	}; !slices.Equal(
		got,
		want,
	) {
		t.Errorf("present %q, want %q", got, want)
	}
	wantMoved := []MovedOperation{
		{
			Reference: ParityOperation{"stackDelete", "DELETE", "/stacks/{id}", "Stacks", false},
			Served:    ParityOperation{"stackRemove", "DELETE", "/stacks/{stackId}", "Stacks", false},
		},
		{
			Reference: ParityOperation{"stackLock", "POST", "/stack/{id}/lock", "Stacks", false},
			Served:    ParityOperation{"stackLock", "POST", "/stacks/{stackId}/lock", "Stacks", false},
		},
	}
	if diff := cmp.Diff(wantMoved, stacks.Moved); diff != "" {
		t.Errorf("moved (-want +got):\n%s", diff)
	}
	if len(stacks.Missing) != 0 || len(stacks.Only) != 0 || stacks.Reference() != 5 {
		t.Errorf("Stacks: %+v", stacks)
	}
	if got := ids(tagParity(t, r, "Contexts").Missing); !slices.Equal(got, []string{"contexts"}) {
		t.Errorf("Contexts missing %q", got)
	}
	untagged := tagParity(t, r, Untagged)
	if !slices.Equal(ids(untagged.Missing), []string{"sessionDeleteAll"}) ||
		!slices.Equal(ids(untagged.Only), []string{"livez"}) {
		t.Errorf("untagged: %+v", untagged)
	}
	org := tagParity(t, r, "Organization")
	if org.Reference() != 0 || !slices.Equal(ids(org.Only), []string{"organizations"}) {
		t.Errorf("Organization: %+v", org)
	}
	want := ParityCounts{Reference: 7, Present: 3, Moved: 2, Missing: 2, Only: 2}
	if got := r.Counts(); got != want {
		t.Errorf("counts %+v, want %+v", got, want)
	}
}

// TestUnitParityMatchPriority checks that an operationId match wins over a
// route match: a served operation that keeps one reference operationId at
// another reference operation's route is that operationId moved, and leaves
// the route's own operation missing.
func TestUnitParityMatchPriority(t *testing.T) {
	served := `paths:
  /stacks/{id}:
    get: {operationId: stacks, tags: [Stacks]}
`
	r, err := CompareParity([]byte(parityReference), []byte(served))
	if err != nil {
		t.Fatal(err)
	}
	stacks := tagParity(t, r, "Stacks")
	if len(stacks.Moved) != 1 || stacks.Moved[0].Reference.OperationID != "stacks" {
		t.Errorf("moved %+v, want stacks", stacks.Moved)
	}
	if !slices.Contains(ids(stacks.Missing), "stack") {
		t.Errorf("missing %q lacks stack", ids(stacks.Missing))
	}
}

func TestUnitParityText(t *testing.T) {
	golden(t, "parity.txt", []byte(compareFixture(t).Text()))
}

func TestUnitParityBaselineRoundTrip(t *testing.T) {
	r := compareFixture(t)
	b := r.Baseline(nil)
	golden(t, "parity-baseline.json", b.JSON())
	back, err := ReadParityBaseline(b.JSON())
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(b, back); diff != "" {
		t.Errorf("round trip (-wrote +read):\n%s", diff)
	}
	// Written afresh, every move lacks a reason, so the check refuses it.
	d := r.Diff(b)
	if len(d.Improvements) != 0 || len(d.Regressions) != 2 {
		t.Fatalf("diff %+v", d)
	}
	for _, s := range d.Regressions {
		if !strings.HasSuffix(s, "the baseline gives no reason for the move") {
			t.Errorf("regression %q", s)
		}
	}
	b.Moved[0].Reason = "Routes use the plural collection & its ID."
	b.Moved[1].Reason = "   "
	d = r.Diff(b)
	if len(d.Regressions) != 1 || !strings.Contains(d.Regressions[0], "stackLock") {
		t.Errorf("a blank reason passes: %+v", d)
	}
	b.Moved[1].Reason = "Locks sit under the plural collection."
	if d := r.Diff(b); !d.Clean() {
		t.Errorf("a baseline with reasons fails: %s", d.Text())
	}
	if !strings.Contains(string(b.JSON()), "collection & its") {
		t.Error("the baseline escapes a reason's ampersand")
	}
	// Rewriting keeps each reason whose move is unchanged.
	again := r.Baseline(b)
	if diff := cmp.Diff(b, again); diff != "" {
		t.Errorf("rewrite lost a reason (-had +rewrote):\n%s", diff)
	}
}

// accepted is the baseline that accepts compareFixture exactly.
func accepted(t *testing.T) *ParityBaseline {
	t.Helper()
	b := compareFixture(t).Baseline(nil)
	for i := range b.Moved {
		b.Moved[i].Reason = "deliberate"
	}
	return b
}

func TestUnitParityDiff(t *testing.T) {
	r := compareFixture(t)
	if d := r.Diff(
		accepted(t),
	); !d.Clean() ||
		d.Text() != "The baseline accepts the report exactly.\n" {
		t.Fatalf("accepted baseline: %s", d.Text())
	}
	cases := []struct {
		name         string
		edit         func(b *ParityBaseline)
		regressions  []string
		improvements []string
	}{
		{
			name: "missing operation not listed",
			edit: func(b *ParityBaseline) { delete(b.Missing, "Contexts") },
			regressions: []string{
				"Contexts: contexts (GET /contexts) is missing, and the baseline does not list it",
			},
		},
		{
			name: "move not recorded",
			edit: func(b *ParityBaseline) { b.Moved = b.Moved[1:] },
			regressions: []string{
				"Stacks: stackDelete (DELETE /stacks/{id}) is served as stackRemove (DELETE /stacks/{stackId}), " +
					"and the baseline does not record the move",
			},
		},
		{
			name: "move recorded to another route",
			edit: func(b *ParityBaseline) { b.Moved[1].ServedRoute = "POST /locks" },
			regressions: []string{
				"Stacks: stackLock (POST /stack/{id}/lock) is served as stackLock (POST /stacks/{stackId}/lock), " +
					"and the baseline records it as stackLock (POST /stack/{id}/lock) served as stackLock (POST /locks)",
			},
		},
		{
			name: "present operation listed as missing",
			edit: func(b *ParityBaseline) { b.Missing["Stacks"] = []string{"stacks"} },
			improvements: []string{
				"Stacks: stacks is listed as missing, and is present now",
			},
		},
		{
			name: "moved operation listed as missing",
			edit: func(b *ParityBaseline) { b.Missing["Stacks"] = []string{"stackLock"} },
			improvements: []string{
				"Stacks: stackLock is listed as missing, and is moved now",
			},
		},
		{
			name: "missing operation under another tag",
			edit: func(b *ParityBaseline) {
				delete(b.Missing, "Contexts")
				b.Missing["Spaces"] = []string{"contexts"}
			},
			improvements: []string{
				"Spaces: contexts is listed as missing here, and the reference files it under Contexts",
			},
		},
		{
			name: "missing entry the reference lacks",
			edit: func(b *ParityBaseline) { b.Missing["Gone"] = []string{"gone"} },
			improvements: []string{
				"Gone: gone is listed as missing, and the reference no longer has it",
			},
		},
		{
			name: "move entry the reference lacks",
			edit: func(b *ParityBaseline) {
				b.Moved = append(b.Moved, BaselineMove{OperationID: "gone", Reason: "x"})
			},
			improvements: []string{"gone is listed as moved, and the reference no longer has it"},
		},
		{
			name: "move entry for a present operation",
			edit: func(b *ParityBaseline) {
				b.Moved = append(b.Moved, BaselineMove{OperationID: "stacks", Reason: "x"})
			},
			improvements: []string{"Stacks: stacks is listed as moved, and is present now"},
		},
		{
			name: "move entry for a missing operation",
			edit: func(b *ParityBaseline) {
				delete(b.Missing, "Contexts")
				b.Moved = append(b.Moved, BaselineMove{OperationID: "contexts", Reason: "x"})
			},
			regressions: []string{
				"Contexts: contexts (GET /contexts) is missing, and the baseline does not list it",
			},
			improvements: []string{"Contexts: contexts is listed as moved, and is missing now"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := accepted(t)
			tc.edit(b)
			d := r.Diff(b)
			if diff := cmp.Diff(tc.regressions, d.Regressions); diff != "" {
				t.Errorf("regressions (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.improvements, d.Improvements); diff != "" {
				t.Errorf("improvements (-want +got):\n%s", diff)
			}
			text := d.Text()
			for _, s := range tc.regressions {
				if !strings.Contains(text, "regression: "+s+"\n") {
					t.Errorf("text lacks regression %q:\n%s", s, text)
				}
			}
			for _, s := range tc.improvements {
				if !strings.Contains(
					text,
					"improvement (apply with make parity-baseline): "+s+"\n",
				) {
					t.Errorf("text lacks improvement %q:\n%s", s, text)
				}
			}
		})
	}
}

func TestUnitParityErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		reference, served string
		want              error
	}{
		"reference not YAML": {"paths: [", parityServed, nil},
		"served not YAML":    {parityReference, "paths: [", nil},
		"no operationId":     {parityReference, "paths: {/x: {get: {tags: [A]}}}", ErrNoOperationID},
		"operationId twice": {
			"paths: {/x: {get: {operationId: a}}, /y: {get: {operationId: a}}}",
			parityServed,
			ErrDuplicateOp,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := CompareParity([]byte(tc.reference), []byte(tc.served))
			if err == nil {
				t.Fatal("compared")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("error %v, want %v", err, tc.want)
			}
		})
	}
	for name, data := range map[string]string{
		"not JSON":              "{",
		"unknown member":        `{"missing": {}, "moved": [], "extra": 1}`,
		"listed twice":          `{"missing": {"A": ["a"], "B": ["a"]}, "moved": []}`,
		"missing and moved":     `{"missing": {"A": ["a"]}, "moved": [{"operationId": "a"}]}`,
		"wrong type of missing": `{"missing": []}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ReadParityBaseline([]byte(data)); !errors.Is(err, ErrBaseline) {
				t.Errorf("error %v, want ErrBaseline", err)
			}
		})
	}
}

func parityFiles(t *testing.T) ParityFiles {
	t.Helper()
	dir := t.TempDir()
	f := ParityFiles{
		Reference: filepath.Join(dir, "reference.yaml"),
		Served:    filepath.Join(dir, "served.yaml"),
		Baseline:  filepath.Join(dir, "baseline.json"),
	}
	for p, data := range map[string]string{f.Reference: parityReference, f.Served: parityServed} {
		if err := os.WriteFile(p, []byte(data), filePerm); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func TestUnitParityFiles(t *testing.T) {
	f := parityFiles(t)
	res, err := ParityCheck(f)
	if err != nil {
		t.Fatal(err)
	}
	// With no baseline file yet, every gap is a regression.
	if len(res.Diff.Regressions) != 4 || len(res.Baseline.Missing) != 0 {
		t.Errorf("without a baseline: %+v", res.Diff)
	}
	b, err := WriteParityBaseline(f)
	if err != nil {
		t.Fatal(err)
	}
	b.Moved[0].Reason = "deliberate"
	b.Moved[1].Reason = "deliberate"
	if err := os.WriteFile(f.Baseline, b.JSON(), filePerm); err != nil {
		t.Fatal(err)
	}
	if res, err = ParityCheck(f); err != nil || !res.Diff.Clean() {
		t.Fatalf("after writing the baseline: %v %+v", err, res)
	}
	// Rewriting keeps the reasons on disk.
	if b, err = WriteParityBaseline(f); err != nil || b.Moved[0].Reason != "deliberate" {
		t.Errorf("rewrite: %v %+v", err, b)
	}
}

func TestUnitParityFilesErrors(t *testing.T) {
	for name, edit := range map[string]func(f *ParityFiles){
		"no reference":       func(f *ParityFiles) { f.Reference += ".absent" },
		"no served document": func(f *ParityFiles) { f.Served += ".absent" },
		"invalid served":     func(f *ParityFiles) { f.Served = f.Baseline; writeFile(t, f.Baseline, "paths: [") },
		"baseline a directory": func(f *ParityFiles) {
			f.Baseline = filepath.Dir(f.Baseline)
		},
		"invalid baseline": func(f *ParityFiles) { writeFile(t, f.Baseline, "{") },
	} {
		t.Run(name, func(t *testing.T) {
			f := parityFiles(t)
			edit(&f)
			if _, err := ParityCheck(f); err == nil {
				t.Error("ParityCheck passed")
			}
			if _, err := WriteParityBaseline(f); err == nil {
				t.Error("WriteParityBaseline passed")
			}
		})
	}
	t.Run("unwritable baseline", func(t *testing.T) {
		f := parityFiles(t)
		f.Baseline = filepath.Join(f.Baseline, "nested.json")
		if _, err := WriteParityBaseline(f); err == nil {
			t.Error("wrote under a missing directory")
		}
	})
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), filePerm); err != nil {
		t.Fatal(err)
	}
}

// TestUnitParityDesigned proves an x-upstream: none operation is
// infraweave-only even at a reference route or under a reference
// operationId, leaving the reference operation missing.
func TestUnitParityDesigned(t *testing.T) {
	served := `openapi: 3.0.3
paths:
  /contexts:
    get: {operationId: contextsDesigned, tags: [Contexts], x-upstream: none}
  /sessions:
    delete: {operationId: sessionDeleteAll, x-upstream: none}
`
	r, err := CompareParity([]byte(parityReference), []byte(served))
	if err != nil {
		t.Fatal(err)
	}
	contexts := tagParity(t, r, "Contexts")
	if d := cmp.Diff([]string{"contexts"}, ids(contexts.Missing)); d != "" {
		t.Errorf("missing (-want +got):\n%s", d)
	}
	if len(contexts.Only) != 1 || !contexts.Only[0].Designed || len(contexts.Moved) != 0 {
		t.Errorf("contexts = %+v", contexts)
	}
	untagged := tagParity(t, r, Untagged)
	if d := cmp.Diff([]string{"sessionDeleteAll"}, ids(untagged.Missing)); d != "" ||
		len(untagged.Only) != 1 || len(untagged.Present) != 0 {
		t.Errorf("a designed operation keeping a reference operationId: %+v", untagged)
	}
}

// TestUnitProductParity proves each product compares only the served
// operations under its own tags, so neither reports the other's as
// infraweave-only.
func TestUnitProductParity(t *testing.T) {
	reference := `openapi: 3.0.3
paths:
  /flows/projects:
    get: {operationId: listProjects, tags: [Flows Projects]}
`
	served := `openapi: 3.0.3
paths:
  /flows/projects:
    get: {operationId: listProjects, tags: [Flows Projects]}
  /flows/members:
    get: {operationId: listMembers, tags: [Flows Members], x-upstream: none}
  /flows-integration:
    get: {operationId: flowsIntegration, tags: [Flows]}
  /stacks:
    get: {operationId: stacks, tags: [Stacks]}
  /livez:
    get: {operationId: livez}
`
	for _, tc := range []struct {
		p             Product
		present, only []string
	}{
		{Flows, []string{"listProjects"}, []string{"listMembers"}},
		{Deploy, nil, []string{"flowsIntegration", "livez", "stacks"}},
		{Product{}, []string{"listProjects"}, []string{"flowsIntegration", "listMembers", "livez", "stacks"}},
	} {
		r, err := tc.p.Parity([]byte(reference), []byte(served))
		if err != nil {
			t.Fatal(err)
		}
		var present, only []string
		for _, tp := range r.Tags {
			present = append(present, ids(tp.Present)...)
			only = append(only, ids(tp.Only)...)
		}
		slices.Sort(only)
		if d := cmp.Diff(tc.present, present); d != "" {
			t.Errorf("%q present (-want +got):\n%s", tc.p.Name, d)
		}
		if d := cmp.Diff(tc.only, only); d != "" {
			t.Errorf("%q infraweave-only (-want +got):\n%s", tc.p.Name, d)
		}
	}
}

// TestUnitParityReasons proves a missing operation's reason survives a
// rewrite while it stays missing, goes when it is served, and is an
// improvement when the baseline gives a reason for an operation it does not
// list as missing.
func TestUnitParityReasons(t *testing.T) {
	r := compareFixture(t)
	prev := r.Baseline(nil)
	if prev.Reasons != nil || strings.Contains(string(prev.JSON()), "reasons") {
		t.Errorf("a baseline without reasons writes none: %s", prev.JSON())
	}
	prev.Reasons = map[string]string{"contexts": "Waits on PD-99.", "stacks": "Stale."}
	if d := r.Diff(prev); !slices.Equal(d.Improvements,
		[]string{"stacks has a reason, and the baseline does not list it as missing"}) {
		t.Errorf("improvements = %q", d.Improvements)
	}
	next := r.Baseline(prev)
	if d := cmp.Diff(map[string]string{"contexts": "Waits on PD-99."}, next.Reasons); d != "" {
		t.Errorf("reasons (-want +got):\n%s", d)
	}
	back, err := ReadParityBaseline(next.JSON())
	if err != nil || back.Reasons["contexts"] != "Waits on PD-99." {
		t.Errorf("round trip: %+v %v", back, err)
	}
	if d := r.Diff(next); len(d.Improvements) != 0 {
		t.Errorf("the rewritten baseline still has improvements: %s", d.Text())
	}
}
