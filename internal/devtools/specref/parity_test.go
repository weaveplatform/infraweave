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

// parityReference is a reference with five tags and an untagged operation.
const parityReference = `openapi: 3.0.3
paths:
  /stacks:
    parameters: [{name: search, in: query}]
    get:
      operationId: stacks
      tags: [Stacks]
      x-graphql: {field: stacks, type: Query}
    post: {operationId: stackCreate, tags: [Stacks], x-graphql: {field: stackCreate, type: Mutation}}
  /stacks/{id}:
    get: {operationId: stack, tags: [Stacks], x-graphql: {field: stack, type: Query}}
    delete: {operationId: stackDelete, tags: [Stacks]}
  /stack/{id}/lock:
    post: {operationId: stackLock, tags: [Stacks]}
  /stacks/{id}/outputs:
    get: {operationId: stackOutputs, tags: [Stacks], x-graphql: {field: outputs, type: Stack}}
  /contexts:
    get: {operationId: contexts, tags: [Contexts]}
  /account/spacelift-thing:
    get: {operationId: spaceliftThing, tags: [Account]}
  /policies/{id}/simulate:
    post: {operationId: policySimulate, tags: [Policies]}
  /sessions:
    delete: {operationId: sessionDeleteAll}
`

// parityServed covers every outcome against parityReference with
// parityRegisters: stacks, stackCreate and stack served, with the
// conventions' parameters and a renamed path parameter; stackDelete and
// stackLock served with a recorded divergence; spaceliftThing renamed;
// stackOutputs, contexts and sessionDeleteAll missing; policySimulate
// superseded; and two infraweave-only operations.
const parityServed = `openapi: 3.0.3
components:
  parameters:
    Cursor: {name: cursor, in: query}
    Limit: {name: limit, in: query}
paths:
  /stacks:
    parameters: [{name: search, in: query}]
    summary: path-level members are not operations
    get:
      operationId: stacks
      tags: [Stacks]
      parameters: [{$ref: '#/components/parameters/Cursor'}, {$ref: '#/components/parameters/Limit'}]
      x-infraweave-divergence: [cursor-pagination, rate-limit]
    post:
      operationId: stackCreate
      tags: [Stacks]
      parameters: [{name: Idempotency-Key, in: header}]
      x-infraweave-divergence: [idempotency-key, rate-limit]
  /stacks/{stackId}:
    get: {operationId: stack, tags: [Stacks]}
    delete: {operationId: stackDelete, tags: [Stacks], x-infraweave-divergence: [soft-delete]}
  /stacks/{stackId}/lock:
    post: {operationId: stackLock, tags: [Stacks], x-infraweave-divergence: [path, rate-limit]}
  /account/thing:
    get: {operationId: thing, tags: [Account]}
  /organizations:
    get: {operationId: organizations, tags: [Organization]}
  /livez:
    get: {operationId: livez}
`

const (
	parityRenames = `{"records": [
  {"kind": "identifier", "reference": "spaceliftThing", "infraweave": "thing"},
  {"kind": "identifier", "reference": "/spacelift-thing", "infraweave": "/thing"},
  {"kind": "identifier", "reference": "/spacelift", "infraweave": "/infraweave"},
  {"kind": "identifier", "reference": "stacks", "infraweave": "x", "schema": "Stack"},
  {"kind": "identifier", "reference": "spacelift_*", "infraweave": "infraweave_*"},
  {"kind": "concept", "reference": "root", "infraweave": "top"}]}`
	paritySupersessions = `{"records": [
  {"id": "SUP-98", "treatment": "superseded", "operations": ["flows:stacks"]},
  {"id": "SUP-99", "treatment": "superseded", "operations": ["deploy:policySimulate"]},
  {"id": "DEP-01", "treatment": "served"}]}`
)

func parityRegisters(t *testing.T) *ParityRegisters {
	t.Helper()
	reg, err := ReadParityRegisters([]byte(parityRenames), []byte(paritySupersessions), "deploy")
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func compare(t *testing.T, served string) *ParityReport {
	t.Helper()
	r, err := compareParity([]byte(parityReference), []byte(served), nil, parityRegisters(t))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func compareFixture(t *testing.T) *ParityReport { t.Helper(); return compare(t, parityServed) }

func ids(ops []ParityOperation) []string {
	out := make([]string, 0, len(ops))
	for _, o := range ops {
		out = append(out, o.OperationID)
	}
	return out
}

// outcomes maps each reference operationId of r to its outcome.
func outcomes(r *ParityReport) map[string]Outcome {
	out := map[string]Outcome{}
	for _, t := range r.Tags {
		for _, op := range t.Operations {
			out[op.Reference.OperationID] = op.Outcome
		}
	}
	return out
}

func TestUnitParityCompare(t *testing.T) {
	r := compareFixture(t)
	want := map[string]Outcome{
		"stacks":           OutcomeServed,
		"stackCreate":      OutcomeServed,
		"stack":            OutcomeServed,
		"stackDelete":      OutcomeDivergent,
		"stackLock":        OutcomeDivergent,
		"spaceliftThing":   OutcomeDivergent,
		"stackOutputs":     OutcomeMissing,
		"contexts":         OutcomeMissing,
		"sessionDeleteAll": OutcomeMissing,
		"policySimulate":   OutcomeSuperseded,
	}
	if d := cmp.Diff(want, outcomes(r)); d != "" {
		t.Errorf("outcomes (-want +got):\n%s", d)
	}
	if len(r.Failures) != 0 {
		t.Errorf("failures: %q", r.Failures)
	}
	tags := make([]string, 0, len(r.Tags))
	only := make([]string, 0, len(r.Tags))
	for _, tp := range r.Tags {
		tags = append(tags, tp.Tag)
		only = append(only, ids(tp.Only)...)
	}
	wantTags := []string{"Account", "Contexts", OrganizationTag, "Policies", "Stacks", Untagged}
	if !slices.Equal(tags, wantTags) {
		t.Errorf("tags %q, want %q", tags, wantTags)
	}
	if !slices.Equal(only, []string{"organizations", "livez"}) {
		t.Errorf("infraweave-only %q", only)
	}
	thing, _ := r.Operation("spaceliftThing")
	if d := cmp.Diff([]string{DivergenceOperationID, DivergencePath}, thing.Differences); d != "" {
		t.Errorf("rename differences (-want +got):\n%s", d)
	}
	sup, _ := r.Operation("policySimulate")
	if sup.Supersession != "SUP-99" || sup.Served != nil {
		t.Errorf("policySimulate = %+v", sup)
	}
	if _, ok := r.Operation("absent"); ok {
		t.Error("found an operation the reference lacks")
	}
	c := r.Counts()
	wantCounts := ParityCounts{Reference: 10, Only: 2, By: map[Outcome]int{
		OutcomeServed: 3, OutcomeDivergent: 3, OutcomeSuperseded: 1, OutcomeMissing: 3,
	}}
	if d := cmp.Diff(wantCounts, c); d != "" {
		t.Errorf("counts (-want +got):\n%s", d)
	}
}

// TestUnitParityFailures proves each of decision 0016's failure rules over
// an edit of the served fixture.
func TestUnitParityFailures(t *testing.T) {
	for _, tc := range []struct {
		name, old, new string
		outcome        map[string]Outcome
		want           []string
	}{
		{
			name: "unrecorded path",
			old:  "x-infraweave-divergence: [path, rate-limit]",
			new:  "x-infraweave-divergence: [rate-limit]",
			want: []string{"Stacks: stackLock (POST /stack/{id}/lock) is served as stackLock " +
				"(POST /stacks/{stackId}/lock), and differs in path without recording it in x-infraweave-divergence"},
		},
		{
			name: "unrecorded method",
			old:  "delete: {operationId: stackDelete",
			new:  "post: {operationId: stackDelete",
			want: []string{"Stacks: stackDelete (DELETE /stacks/{id}) is served as stackDelete " +
				"(POST /stacks/{stackId}), and differs in method without recording it in x-infraweave-divergence"},
		},
		{
			name: "dropped parameter",
			old:  "    parameters: [{name: search, in: query}]\n    summary",
			new:  "    summary",
			want: []string{
				"Stacks: stackCreate (POST /stacks) is served as stackCreate (POST /stacks), " +
					"and differs in parameter:search without recording it in x-infraweave-divergence",
				"Stacks: stacks (GET /stacks) is served as stacks (GET /stacks), " +
					"and differs in parameter:search without recording it in x-infraweave-divergence",
			},
		},
		{
			name: "parameter made required",
			old:  "parameters: [{$ref: '#/components/parameters/Cursor'}",
			new:  "parameters: [{name: search, in: query, required: true}, {$ref: '#/components/parameters/Cursor'}",
			want: []string{"Stacks: stacks (GET /stacks) is served as stacks (GET /stacks), " +
				"and differs in parameter:search without recording it in x-infraweave-divergence"},
		},
		{
			name: "convention parameter without its convention",
			old:  "x-infraweave-divergence: [cursor-pagination, rate-limit]",
			new:  "x-infraweave-divergence: [rate-limit]",
			want: []string{"Stacks: stacks (GET /stacks) is served as stacks (GET /stacks), " +
				"and differs in parameter:cursor, parameter:limit without recording it in x-infraweave-divergence"},
		},
		{
			name: "recorded parameter",
			old: "parameters: [{$ref: '#/components/parameters/Cursor'}, {$ref: '#/components/parameters/Limit'}]\n" +
				"      x-infraweave-divergence: [cursor-pagination, rate-limit]",
			new: "parameters: [{name: search, in: query, required: true}, {$ref: '#/components/parameters/Cursor'}, " +
				"{$ref: '#/components/parameters/Limit'}]\n" +
				"      x-infraweave-divergence: [cursor-pagination, rate-limit, parameter:search]",
			outcome: map[string]Outcome{"stacks": OutcomeDivergent},
		},
		{
			name: "stale record",
			old:  "get: {operationId: stack, tags: [Stacks]}",
			new:  "get: {operationId: stack, tags: [Stacks], x-infraweave-divergence: [path, parameter:x]}",
			want: []string{"Stacks: stack (GET /stacks/{id}) is served as stack (GET /stacks/{stackId}), " +
				"and its x-infraweave-divergence records path, parameter:x, which it does not differ in"},
		},
		{
			name: "renamed beyond the rename table",
			old:  "/account/thing:",
			new:  "/accounts/thing:",
			want: []string{"Account: spaceliftThing (GET /account/spacelift-thing) is served as thing " +
				"(GET /accounts/thing), and differs in path without recording it in x-infraweave-divergence"},
		},
		{
			name:    "renamed operation served under its reference name",
			old:     "operationId: thing,",
			new:     "operationId: spaceliftThing,",
			outcome: map[string]Outcome{"spaceliftThing": OutcomeDivergent},
		},
		{
			name: "served superseded operation",
			old:  "  /livez:\n",
			new:  "  /policies/{policyId}/simulate:\n    post: {operationId: policySimulate, tags: [Policies]}\n  /livez:\n",
			want: []string{"Policies: policySimulate (POST /policies/{id}/simulate) is superseded by SUP-99, " +
				"and is served as policySimulate (POST /policies/{policyId}/simulate)"},
			outcome: map[string]Outcome{"policySimulate": OutcomeSuperseded},
		},
		{
			name:    "served operation removed",
			old:     "    get: {operationId: stack, tags: [Stacks]}\n",
			new:     "",
			outcome: map[string]Outcome{"stack": OutcomeMissing},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(parityServed, tc.old) {
				t.Fatalf("the fixture lacks %q", tc.old)
			}
			r := compare(t, strings.Replace(parityServed, tc.old, tc.new, 1))
			if d := cmp.Diff(tc.want, r.Failures); d != "" {
				t.Errorf("failures (-want +got):\n%s", d)
			}
			got := outcomes(r)
			for id, o := range tc.outcome {
				if got[id] != o {
					t.Errorf("%s is %s, want %s", id, got[id], o)
				}
			}
			if len(tc.want) > 0 && !strings.Contains(r.Text(), "failure: "+tc.want[0]+"\n") {
				t.Errorf("text lacks the failure:\n%s", r.Text())
			}
		})
	}
}

func TestUnitParityRenamePath(t *testing.T) {
	reg := parityRegisters(t)
	for in, want := range map[string]string{
		"/account/spacelift-thing":       "/account/thing",
		"/spacelift/x":                   "/infraweave/x",
		"/spacelift":                     "/infraweave",
		"/spacelift-intelligence":        "/spacelift-intelligence",
		"/a/spacelift-thing/b/spacelift": "/a/thing/b/infraweave",
		"/stacks":                        "/stacks",
	} {
		if got := reg.renamePath(in); got != want {
			t.Errorf("renamePath(%q) = %q, want %q", in, got, want)
		}
	}
	if reg.renameID("stacks") != "stacks" {
		t.Error("a rename scoped to a schema renamed an operationId")
	}
}

func TestUnitParityText(t *testing.T) {
	r := compareFixture(t)
	golden(t, "parity.txt", []byte(r.Text()+r.Listing()))
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
	if d := r.Diff(b); !d.Clean() || d.Text() != "The baseline accepts the report exactly.\n" {
		t.Errorf("the written baseline: %s", d.Text())
	}
	b.Reasons = map[string]string{"contexts": "Waits on <PD-99> & more."}
	if !strings.Contains(string(b.JSON()), "<PD-99> & more") {
		t.Error("the baseline escapes a reason")
	}
}

func TestUnitParityDiff(t *testing.T) {
	r := compareFixture(t)
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
			name:         "served operation listed as missing",
			edit:         func(b *ParityBaseline) { b.Missing["Stacks"] = append(b.Missing["Stacks"], "stacks") },
			improvements: []string{"Stacks: stacks is listed as missing, and is served now"},
		},
		{
			name: "divergent operation listed as missing",
			edit: func(b *ParityBaseline) { b.Missing["Stacks"] = append(b.Missing["Stacks"], "stackLock") },
			improvements: []string{
				"Stacks: stackLock is listed as missing, and is served with divergence now",
			},
		},
		{
			name: "superseded operation listed as missing",
			edit: func(b *ParityBaseline) { b.Missing["Policies"] = []string{"policySimulate"} },
			improvements: []string{
				"Policies: policySimulate is listed as missing, and is superseded now",
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
			name: "reason without a missing entry",
			edit: func(b *ParityBaseline) { b.Reasons = map[string]string{"stacks": "Stale."} },
			improvements: []string{
				"stacks has a reason, and the baseline does not list it as missing",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := r.Baseline(nil)
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

// TestUnitParityReasons proves a missing operation's reason survives a
// rewrite while it stays missing and goes when it is served.
func TestUnitParityReasons(t *testing.T) {
	r := compareFixture(t)
	prev := r.Baseline(nil)
	if prev.Reasons != nil || strings.Contains(string(prev.JSON()), "reasons") {
		t.Errorf("a baseline without reasons writes none: %s", prev.JSON())
	}
	prev.Reasons = map[string]string{"contexts": "Waits on PD-99.", "stacks": "Stale."}
	next := r.Baseline(prev)
	if d := cmp.Diff(map[string]string{"contexts": "Waits on PD-99."}, next.Reasons); d != "" {
		t.Errorf("reasons (-want +got):\n%s", d)
	}
	back, err := ReadParityBaseline(next.JSON())
	if err != nil || back.Reasons["contexts"] != "Waits on PD-99." {
		t.Errorf("round trip: %+v %v", back, err)
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
		"unknown member":        `{"missing": {}, "moved": []}`,
		"listed twice":          `{"missing": {"A": ["a"], "B": ["a"]}}`,
		"wrong type of missing": `{"missing": []}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ReadParityBaseline([]byte(data)); !errors.Is(err, ErrBaseline) {
				t.Errorf("error %v, want ErrBaseline", err)
			}
		})
	}
	for name, tc := range map[string][2]string{
		"renames not JSON":       {"{", "{}"},
		"supersessions not JSON": {"{}", "{"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ReadParityRegisters(
				[]byte(tc[0]),
				[]byte(tc[1]),
				"deploy",
			); !errors.Is(
				err,
				ErrRegister,
			) {
				t.Errorf("error %v, want ErrRegister", err)
			}
		})
	}
}

func parityFiles(t *testing.T) ParityFiles {
	t.Helper()
	dir := t.TempDir()
	f := ParityFiles{
		Reference:     filepath.Join(dir, "reference.yaml"),
		Served:        filepath.Join(dir, "served.yaml"),
		Baseline:      filepath.Join(dir, "baseline.json"),
		Renames:       filepath.Join(dir, "renames.json"),
		Supersessions: filepath.Join(dir, "supersessions.json"),
		Product:       Deploy,
	}
	for p, data := range map[string]string{
		f.Reference:     parityReference,
		f.Served:        parityServed,
		f.Renames:       parityRenames,
		f.Supersessions: paritySupersessions,
	} {
		writeFile(t, p, data)
	}
	return f
}

func TestUnitParityFiles(t *testing.T) {
	f := parityFiles(t)
	res, err := ParityCheck(f)
	if err != nil {
		t.Fatal(err)
	}
	// With no baseline file yet, every missing operation is a regression.
	if len(res.Diff.Regressions) != 3 || len(res.Baseline.Missing) != 0 || res.Passed() {
		t.Errorf("without a baseline: %+v", res.Diff)
	}
	if _, err := WriteParityBaseline(f); err != nil {
		t.Fatal(err)
	}
	if res, err = ParityCheck(f); err != nil || !res.Passed() {
		t.Fatalf("after writing the baseline: %v %+v", err, res)
	}
	// Without the registers, the rename is missing and the supersession
	// served nowhere is missing too.
	f.Renames, f.Supersessions = "", ""
	if res, err = ParityCheck(f); err != nil || res.Passed() ||
		outcomes(res.Report)["spaceliftThing"] != OutcomeMissing ||
		outcomes(res.Report)["policySimulate"] != OutcomeMissing {
		t.Errorf("without registers: %v %+v", err, res)
	}
}

func TestUnitParityFilesErrors(t *testing.T) {
	for name, edit := range map[string]func(f *ParityFiles){
		"no reference":       func(f *ParityFiles) { f.Reference += ".absent" },
		"no served document": func(f *ParityFiles) { f.Served += ".absent" },
		"no renames":         func(f *ParityFiles) { f.Renames += ".absent" },
		"no supersessions":   func(f *ParityFiles) { f.Supersessions += ".absent" },
		"invalid renames":    func(f *ParityFiles) { writeFile(t, f.Renames, "{") },
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
	r := compare(t, served)
	got := outcomes(r)
	if got["contexts"] != OutcomeMissing || got["sessionDeleteAll"] != OutcomeMissing {
		t.Errorf("outcomes %v", got)
	}
	var only []ParityOperation
	for _, tp := range r.Tags {
		only = append(only, tp.Only...)
	}
	if len(only) != 2 || !only[0].Designed || !only[1].Designed {
		t.Errorf("infraweave-only %+v", only)
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
		p            Product
		served, only []string
	}{
		{Flows, []string{"listProjects"}, []string{"listMembers"}},
		{Deploy, nil, []string{"flowsIntegration", "livez", "stacks"}},
		{Product{}, []string{"listProjects"}, []string{"flowsIntegration", "listMembers", "livez", "stacks"}},
	} {
		r, err := tc.p.Parity([]byte(reference), []byte(served), nil)
		if err != nil {
			t.Fatal(err)
		}
		var got, only []string
		for _, tp := range r.Tags {
			for _, op := range tp.Operations {
				if op.Outcome == OutcomeServed {
					got = append(got, op.Reference.OperationID)
				}
			}
			only = append(only, ids(tp.Only)...)
		}
		slices.Sort(only)
		if d := cmp.Diff(tc.served, got); d != "" {
			t.Errorf("%q served (-want +got):\n%s", tc.p.Name, d)
		}
		if d := cmp.Diff(tc.only, only); d != "" {
			t.Errorf("%q infraweave-only (-want +got):\n%s", tc.p.Name, d)
		}
	}
}
