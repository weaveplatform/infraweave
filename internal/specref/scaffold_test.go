package specref

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// conventionComponents are what api/src/deploy/components-common.yaml
// defines for decision 0003, which every scaffold references.
var conventionComponents = []string{
	"parameters/IdempotencyKey", "parameters/Cursor", "parameters/Limit",
	"headers/RateLimitLimit", "headers/RateLimitRemaining", "headers/RateLimitReset",
	"responses/TooManyRequests", "responses/ApiDisabled", "responses/IdempotencyConflict",
	"responses/IdempotencyMismatch", "responses/InvalidCursor",
}

func common(extra ...string) Defined {
	d := Defined{Paths: map[string]bool{}, Components: map[string]bool{}}
	for _, c := range append(slices.Clone(conventionComponents), extra...) {
		d.Components[c] = true
	}
	return d
}

func goldenReference(t *testing.T) []byte {
	t.Helper()
	ref, err := os.ReadFile(filepath.Join("testdata", "golden", FileSpec))
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

type fragment struct {
	Paths      map[string]ymap           `yaml:"paths"`
	Components map[string]map[string]any `yaml:"components"`
}

func scaffoldFixture(t *testing.T, tag string, existing Defined) (*Scaffolded, fragment) {
	t.Helper()
	s, err := Scaffold(ScaffoldOptions{Reference: goldenReference(t), Tag: tag, Existing: existing})
	if err != nil {
		t.Fatal(err)
	}
	var f fragment
	if err := yaml.Unmarshal(s.YAML, &f); err != nil {
		t.Fatal(err)
	}
	return s, f
}

func TestUnitScaffoldSelectsTag(t *testing.T) {
	s, f := scaffoldFixture(
		t,
		"Stacks",
		common("schemas/Problem", "responses/Problem", "responses/NotFound"),
	)
	for p, item := range f.Paths {
		if !hasTag(item, "Stacks") {
			t.Errorf("%s has no Stacks operation", p)
		}
	}
	if op(f, "/stacks", "get") == nil || op(f, "/stacks", "post") == nil {
		t.Errorf("/stacks missing: %v", sortedKeys(f.Paths))
	}
	if f.Paths["/contexts"] != nil {
		t.Error("a Contexts path was scaffolded under Stacks")
	}
	if s.Operations == 0 || s.Components != countComponents(f) {
		t.Errorf(
			"counts: %d operations, %d components (fragment has %d)",
			s.Operations,
			s.Components,
			countComponents(f),
		)
	}
	if !strings.HasPrefix(
		string(s.YAML),
		"# Scaffolded by `specref scaffold --tag Stacks` from the Spacelift reference (schema-",
	) {
		t.Errorf("header: %.120s", s.YAML)
	}
	if s.TagDescription == "" {
		t.Error("tag description missing")
	}
	// The fixture's POST /stacks/{stackId}/runs is untagged but shares a
	// path with the tagged GET, so it comes along and is reported.
	if !slices.Contains(s.Foreign, "POST /stacks/{stackId}/runs (untagged)") {
		t.Errorf("foreign = %v", s.Foreign)
	}
}

// op returns one operation of the fragment, or nil.
func op(f fragment, path, method string) ymap {
	o, _ := f.Paths[path][method].(ymap)
	return o
}

func countComponents(f fragment) int {
	n := 0
	for _, items := range f.Components {
		n += len(items)
	}
	return n
}

func TestUnitScaffoldClosure(t *testing.T) {
	_, f := scaffoldFixture(t, "Stacks", common("responses/Problem", "responses/NotFound"))
	schemas := f.Components["schemas"]
	// Stack reaches Owner, a union; its discriminator mapping and oneOf reach
	// both members.
	for _, name := range []string{"Stack", "Owner", "User", "Team", "StackRef", "StackPage"} {
		if schemas[name] == nil {
			t.Errorf("schemas/%s missing from the closure", name)
		}
	}
	// Problem is reached only through responses/Problem, defined elsewhere.
	if schemas["Problem"] != nil {
		t.Error("a schema reached only through an existing response was copied")
	}
	if f.Components["responses"] != nil {
		t.Errorf("responses defined elsewhere were copied: %v", f.Components["responses"])
	}
	if f.Components["parameters"] != nil || f.Components["headers"] != nil {
		t.Error("convention components were copied instead of referenced")
	}
	if schemas["Context"] != nil {
		t.Error("an unreachable schema was copied")
	}
}

func TestUnitScaffoldReferencesExisting(t *testing.T) {
	existing := common(
		"schemas/Problem",
		"responses/Problem",
		"responses/NotFound",
		"schemas/Stack",
	)
	s, f := scaffoldFixture(t, "Stacks", existing)
	if f.Components["schemas"]["Stack"] != nil {
		t.Error("schemas/Stack is defined elsewhere but was copied")
	}
	if !strings.Contains(string(s.YAML), "$ref: '#/components/schemas/Stack'") {
		t.Error("references to the existing Stack were dropped")
	}
	// Owner is reached only through Stack, which another fragment owns.
	if f.Components["schemas"]["Owner"] != nil {
		t.Error("a component reached only through an existing one was copied")
	}

	existing.Paths["/stacks"] = true
	s, f = scaffoldFixture(t, "Stacks", existing)
	if f.Paths["/stacks"] != nil || !slices.Contains(s.Shared, "/stacks") {
		t.Errorf("a path another fragment defines was scaffolded: shared=%v", s.Shared)
	}
}

func TestUnitScaffoldConventions(t *testing.T) {
	_, f := scaffoldFixture(
		t,
		"Stacks",
		common("schemas/Problem", "responses/Problem", "responses/NotFound"),
	)
	list := op(f, "/stacks", "get")
	if !hasParamRefs(list, "Cursor", "Limit") {
		t.Errorf("list lacks cursor and limit: %v", list["parameters"])
	}
	schema := list["responses"].(ymap)["200"].(ymap)["content"].(ymap)["application/json"].(ymap)["schema"].(ymap)
	if schema["$ref"] != "#/components/schemas/StackPage" {
		t.Errorf("list response = %v", schema)
	}
	page := f.Components["schemas"]["StackPage"].(ymap)
	if page["properties"].(ymap)["items"].(ymap)["items"].(ymap)["$ref"] != "#/components/schemas/Stack" {
		t.Errorf("StackPage items = %v", page["properties"])
	}
	if divergence(list) != "cursor-pagination,rate-limit" ||
		list["x-infraweave-cost"] != CostStandard {
		t.Errorf("list: divergence %s, cost %v", divergence(list), list["x-infraweave-cost"])
	}

	create := op(f, "/stacks", "post")
	if !hasParamRefs(create, "IdempotencyKey") ||
		divergence(create) != "idempotency-key,rate-limit" {
		t.Errorf("create: %v %s", create["parameters"], divergence(create))
	}
	for code, want := range map[string]string{
		"409": "IdempotencyConflict", "422": "IdempotencyMismatch", "429": "TooManyRequests", "503": "ApiDisabled",
	} {
		if r := create["responses"].(ymap)[code].(ymap); r["$ref"] != "#/components/responses/"+want {
			t.Errorf("create %s = %v", code, r)
		}
	}
	created := create["responses"].(ymap)["201"].(ymap)
	if created["headers"].(ymap)["X-RateLimit-Remaining"] == nil {
		t.Error("success response lacks rate-limit headers")
	}

	item := op(f, "/stacks/{stackId}", "get")
	if hasParamRefs(item, "Cursor") || divergence(item) != "rate-limit" {
		t.Errorf("a single-item GET was paginated: %s", divergence(item))
	}
}

func hasParamRefs(op ymap, names ...string) bool {
	params, _ := op["parameters"].([]any)
	for _, n := range names {
		if !slices.ContainsFunc(params, func(p any) bool {
			m, ok := p.(ymap)
			return ok && m["$ref"] == "#/components/parameters/"+n
		}) {
			return false
		}
	}
	return true
}

func divergence(op ymap) string {
	return strings.Join(tags(ymap{"tags": op["x-infraweave-divergence"]}), ",")
}

// mini is a reference small enough to state each convention edge inline.
const mini = `openapi: 3.0.3
info: {title: t, version: schema-mini}
paths:
  /widgets/search:
    post:
      operationId: searchWidgets
      tags: [W]
      x-rest-style: search
      responses:
        "200": {description: ok}
  /widgets/batch:
    post:
      operationId: widgetsBatch
      tags: [W]
      responses:
        "200": {description: ok}
  /widget-names:
    get:
      operationId: widgetNames
      tags: [W]
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {type: array, items: {type: string}}
  /widgets/{id}:
    delete:
      operationId: widgetDelete
      tags: [W]
`

func TestUnitScaffoldCostAndPageNames(t *testing.T) {
	s, err := Scaffold(ScaffoldOptions{Reference: []byte(mini), Tag: "W", Existing: common()})
	if err != nil {
		t.Fatal(err)
	}
	var f fragment
	if err := yaml.Unmarshal(s.YAML, &f); err != nil {
		t.Fatal(err)
	}
	search := op(f, "/widgets/search", "post")
	if search["x-infraweave-cost"] != CostExpensive || hasParamRefs(search, "IdempotencyKey") {
		t.Errorf("search: cost %v, params %v", search["x-infraweave-cost"], search["parameters"])
	}
	batch := op(f, "/widgets/batch", "post")
	if batch["x-infraweave-cost"] != CostExpensive || !hasParamRefs(batch, "IdempotencyKey") {
		t.Errorf("batch: cost %v, params %v", batch["x-infraweave-cost"], batch["parameters"])
	}
	if f.Components["schemas"]["WidgetNamesPage"] == nil {
		t.Errorf("a list of plain values is named after its operation: %v", f.Components["schemas"])
	}
	del := op(f, "/widgets/{id}", "delete")
	if del["responses"].(ymap)["429"] == nil {
		t.Error("an operation without responses gains none")
	}
}

func TestUnitScaffoldDeterministic(t *testing.T) {
	a, _ := scaffoldFixture(t, "Stacks", common("responses/Problem", "responses/NotFound"))
	b, _ := scaffoldFixture(t, "Stacks", common("responses/Problem", "responses/NotFound"))
	if !bytes.Equal(a.YAML, b.YAML) {
		t.Error("two scaffolds of the same tag differ")
	}
}

func TestUnitScaffoldErrors(t *testing.T) {
	clash := strings.Replace(
		mini,
		"operationId: widgetNames\n",
		"operationId: widgetNames\n      parameters:\n"+
			"        - {name: limit, in: query, schema: {type: integer}}\n",
		1,
	)
	for _, tc := range []struct {
		name, ref, tag string
		existing       Defined
		want           error
	}{
		{"unknown tag", mini, "Gadgets", common(), ErrUnknownTag},
		{"missing convention component", mini, "W", Defined{}, ErrMissingComponent},
		{"parameter clash", clash, "W", common(), ErrParamClash},
		{"page clash", mini + "components:\n  schemas:\n    WidgetNamesPage: {type: object}\n", "W", common(), ErrComponentClash},
		{"not YAML", "paths: [", "W", common(), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Scaffold(
				ScaffoldOptions{Reference: []byte(tc.ref), Tag: tc.tag, Existing: tc.existing},
			)
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Errorf("got %v, want %v", err, tc.want)
			}
		})
	}
	idem := strings.Replace(
		mini,
		"operationId: widgetsBatch\n",
		"operationId: widgetsBatch\n      parameters:\n"+
			"        - {name: Idempotency-Key, in: header, schema: {type: string}}\n",
		1,
	)
	if _, err := Scaffold(
		ScaffoldOptions{Reference: []byte(idem), Tag: "W", Existing: common()},
	); !errors.Is(
		err,
		ErrParamClash,
	) {
		t.Errorf("idempotency clash: %v", err)
	}
}

func TestUnitScaffoldFile(t *testing.T) {
	refDir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(refDir, FileSpec),
		goldenReference(t),
		filePerm,
	); err != nil {
		t.Fatal(err)
	}
	svc := t.TempDir()
	writeCommon(t, svc, "responses/Problem", "responses/NotFound")
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(svc, name), []byte(body), filePerm); err != nil {
			t.Fatal(err)
		}
	}
	write("info.yaml", "openapi: 3.0.3\n")
	write("README.md", "ignored")
	out := filepath.Join(svc, "paths-stacks.yaml")

	s, err := ScaffoldFile(refDir, "Stacks", out, false)
	if err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(first, s.YAML) {
		t.Fatalf("fragment not written: %v", err)
	}
	if _, err := ScaffoldFile(refDir, "Stacks", out, false); !errors.Is(err, ErrFragmentExists) {
		t.Errorf("overwrite without force: %v", err)
	}
	// With force, the fragment being replaced doesn't count as existing, so
	// the result is byte-identical rather than empty.
	if _, err := ScaffoldFile(refDir, "Stacks", out, true); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(first, second) {
		t.Errorf("re-scaffold with force differs: %v", err)
	}

	if _, err := ScaffoldFile(
		t.TempDir(),
		"Stacks",
		filepath.Join(t.TempDir(), "p.yaml"),
		false,
	); err == nil {
		t.Error("missing reference: want error")
	}
	if _, err := ScaffoldFile(
		refDir,
		"Gadgets",
		filepath.Join(svc, "paths-g.yaml"),
		false,
	); !errors.Is(
		err,
		ErrUnknownTag,
	) {
		t.Errorf("unknown tag: %v", err)
	}
	if _, err := ScaffoldFile(
		refDir,
		"Stacks",
		filepath.Join(svc, "missing", "p.yaml"),
		false,
	); err == nil {
		t.Error("missing service directory: want error")
	}
	write("paths-bad.yaml", "paths: [\n")
	if _, err := ScaffoldFile(
		refDir,
		"Stacks",
		out,
		true,
	); err == nil ||
		!strings.Contains(err.Error(), "paths-bad.yaml") {
		t.Errorf("unparseable fragment: %v", err)
	}
}

func TestUnitScaffoldFileUnwritable(t *testing.T) {
	refDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(refDir, FileSpec), []byte(mini), filePerm); err != nil {
		t.Fatal(err)
	}
	svc := t.TempDir()
	// A directory where the fragment should go: Stat succeeds, so force is
	// needed, and the write then fails.
	out := filepath.Join(svc, "paths-w.yaml")
	if err := os.Mkdir(out, 0o750); err != nil {
		t.Fatal(err)
	}
	writeCommon(t, svc)
	if _, err := ScaffoldFile(refDir, "W", out, true); err == nil {
		t.Error("unwritable fragment: want error")
	}
}

// writeCommon writes a components-common.yaml defining the convention
// components plus extra ("kind/name"), as ReadDefined will see them.
func writeCommon(t *testing.T, dir string, extra ...string) {
	t.Helper()
	byKind := map[string][]string{}
	for _, c := range append(slices.Clone(conventionComponents), extra...) {
		kind, name, _ := strings.Cut(c, "/")
		byKind[kind] = append(byKind[kind], name)
	}
	var b strings.Builder
	b.WriteString("components:\n")
	for _, kind := range sortedKeys(byKind) {
		b.WriteString("  " + kind + ":\n")
		for _, n := range byKind[kind] {
			b.WriteString("    " + n + ": {description: x}\n")
		}
	}
	if err := os.WriteFile(
		filepath.Join(dir, "components-common.yaml"),
		[]byte(b.String()),
		filePerm,
	); err != nil {
		t.Fatal(err)
	}
}
