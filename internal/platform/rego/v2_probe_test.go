package rego

import (
	"bytes"
	"context"
	"crypto/fips140"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/open-policy-agent/opa/v1/ast"
	oparego "github.com/open-policy-agent/opa/v1/rego"

	"github.com/weaveplatform/infraweave/internal/iac/sanitize"
)

// v2Capabilities is a research-only variant. It changes released language
// features alone, preserving the production builtin/network/FIPS restrictions.
func v2Capabilities(fips bool) *ast.Capabilities {
	caps := capabilitiesFor(fips)
	caps.Features = append(caps.Features, ast.FeatureRegoV2, ast.FeatureRegoV2Import)
	caps.FutureKeywords = ast.CapabilitiesForThisVersion().FutureKeywords
	return caps
}

// prototype evaluates the real linked library with a v1 preamble and a selected
// user dialect. It exposes no production engine value, store or HTTP endpoint.
func prototype(ctx context.Context, kind Type, version ast.RegoVersion, body, input string, fips bool) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	caps := v2Capabilities(fips)
	user, err := ast.ParseModuleWithOpts("prototype.rego", body, ast.ParserOptions{RegoVersion: version, Capabilities: caps})
	if err != nil {
		return Result{}, err
	}
	pre, err := ast.ParseModuleWithOpts("preamble.rego", preamble(kind), ast.ParserOptions{RegoVersion: ast.RegoV1, Capabilities: caps})
	if err != nil {
		return Result{}, err
	}
	query, err := oparego.New(oparego.Query("data.infraweave.result"), oparego.ParsedModule(pre), oparego.ParsedModule(user), oparego.SetRegoVersion(version), oparego.Capabilities(caps), oparego.EnablePrintStatements(true), oparego.Function1(&oparego.Function{Name: "sanitized", Decl: sanitizedDecl()}, sanitized)).
		PrepareForEval(ctx)
	if err != nil {
		return Result{}, err
	}
	var in any
	if err := json.Unmarshal([]byte(input), &in); err != nil {
		return Result{}, err
	}
	var printed printBuffer
	results, err := query.Eval(ctx, oparego.EvalInput(in), oparego.EvalPrintHook(&printed))
	if err != nil {
		return Result{}, err
	}
	if len(results) != 1 || len(results[0].Expressions) != 1 {
		return Result{}, ErrResult
	}
	raw, err := json.Marshal(results[0].Expressions[0].Value)
	return Result{Decision: raw, Print: printed.text.String(), PrintTruncated: printed.truncated}, err
}

// This authored counterexample measures the released semantic difference;
// source inspection alone does not establish how the embedded library behaves.
func TestUnitRegoReleasedV2Semantics(t *testing.T) {
	const body = "package infraweave\ndeny contains \"missing\" if not startswith(input.missing, \"x\")"
	for _, tc := range []struct {
		version ast.RegoVersion
		denied  int
	}{{ast.RegoV1, 0}, {ast.RegoV2, 1}} {
		out, err := prototype(t.Context(), Plan, tc.version, body, `{}`, fips140.Enforced())
		if err != nil {
			t.Fatal(err)
		}
		denied := decision(t, out)["deny"].([]any)
		if len(denied) != tc.denied {
			t.Fatalf("OPA %s negation produced %v", tc.version, denied)
		}
		t.Logf("released OPA %s undefined composite negation: deny=%v", tc.version, denied)
	}
	out, err := prototype(
		t.Context(),
		Plan,
		ast.RegoV1,
		"package infraweave\nimport rego.v2\ndeny contains sanitized(input.secret) if input.a and not (input.b or input.c)",
		`{"secret":"dummy-v2-probe","a":true,"b":false,"c":false}`,
		fips140.Enforced(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := decision(t, out)["deny"]; !reflect.DeepEqual(got, []any{sanitize.Value("dummy-v2-probe")}) {
		t.Fatalf("released v2 import/operators/helper: %v", got)
	}
}

// A wildcard that already existed in v0/v1 must retain its original keyword
// set and undefined-negation meaning across a library update.
func TestUnitRegoLegacyWildcardSemantics(t *testing.T) {
	for _, version := range []EngineType{V0, V1} {
		t.Run(string(version), func(t *testing.T) {
			for _, imports := range []string{"import future.keywords", "import future.keywords.if\nimport future.keywords.contains", "import rego.v1"} {
				out := evaluate(
					t,
					testEngine(t),
					Policy{Type: Plan, Engine: version, Body: "package infraweave\n" + imports + "\ndeny contains \"missing\" if not startswith(input.missing, \"x\")"},
					`{}`,
				)
				if denied := decision(t, out)["deny"].([]any); len(denied) != 0 {
					t.Fatalf("%s enabled later negation semantics: %v", imports, denied)
				}
			}
		})
	}
}

func TestUnitRegoSupportedKeywordImports(t *testing.T) {
	cases := []struct {
		imports      string
		kind         Type
		v0, v1, rule string
		want         any
	}{
		{"import future.keywords.in", Approval, `approve { "item" in input.items }`, `approve if { "item" in input.items }`, "approve", true},
		{
			"import future.keywords.every\nimport future.keywords.in",
			Approval,
			`approve { every x in input.items { x == "item" } }`,
			`approve if { every x in input.items { x == "item" } }`,
			"approve",
			true,
		},
		{"import future.keywords.if", Approval, `approve if input.allowed`, `approve if input.allowed`, "approve", true},
		{"import future.keywords.contains", Plan, `deny contains "seen"`, `deny contains "seen"`, "deny", []any{"seen"}},
	}
	for _, version := range []EngineType{V0, V1} {
		for _, tc := range cases {
			body := tc.v0
			if version == V1 {
				body = tc.v1
			}
			p := Policy{Type: tc.kind, Engine: version, Body: "package infraweave\n" + tc.imports + "\n" + body}
			out := evaluate(t, testEngine(t), p, `{"items":["item"],"allowed":true}`)
			if !reflect.DeepEqual(decision(t, out)[tc.rule], tc.want) {
				t.Fatalf("supported %s import stopped working: %s", version, tc.imports)
			}
		}
	}

	for _, engine := range []EngineType{V0, V1} {
		for _, imp := range []string{"import rego.v2", "import future.keywords.not", "import future.keywords.and", "import future.keywords.or", "import future.keywords.unknown"} {
			err := testEngine(t).Check(t.Context(), Policy{Type: Plan, Engine: engine, Body: "package infraweave\n" + imp})
			var compile *CompileError
			if !errors.As(err, &compile) || len(compile.Diagnostics) == 0 || compile.Diagnostics[0].File != "policy.rego" || compile.Diagnostics[0].Row != 2 {
				t.Fatalf("unsupported import has no predictable source diagnostic: %s: %v", imp, err)
			}
		}
		for _, expression := range []string{`input.a and input.b`, `input.a or input.b`, `not {input.a;input.b}`} {
			err := testEngine(
				t,
			).Check(t.Context(), Policy{Type: Plan, Engine: engine, Body: "package infraweave\nimport future.keywords\ndeny contains \"later syntax\" if " + expression})
			if err == nil {
				t.Fatalf("wildcard enabled v2-only syntax: %s", expression)
			}
		}
	}
}

func TestUnitRegoLanguageCapabilityCache(t *testing.T) {
	e := testEngine(t)
	p := Policy{Type: Plan, Engine: V1, Body: "package infraweave\nimport future.keywords\ndeny contains \"missing\" if not startswith(input.missing, \"x\")"}
	// Model a prepared query made with the released library's wider keyword set.
	e.capabilities = func() *ast.Capabilities { return v2Capabilities(fips140.Enforced()) }
	before := evaluate(t, e, p, `{}`)
	if len(decision(t, before)["deny"].([]any)) != 1 {
		t.Fatal("counterexample did not exercise the wider capabilities")
	}
	e.capabilities = capabilities
	after := evaluate(t, e, p, `{}`)
	if len(decision(t, after)["deny"].([]any)) != 0 || len(e.cache.entries) != 2 {
		t.Fatal("language capability change reused the old query")
	}
	for _, input := range []string{`{"missing":"no"}`, `{"missing":"x"}`} {
		out := evaluate(t, e, p, input)
		want := 0
		if strings.Contains(input, "no") {
			want = 1
		}
		if len(decision(t, out)["deny"].([]any)) != want || len(e.cache.entries) != 2 {
			t.Fatal("prepared query retained input or fragmented its identity")
		}
	}
}

func TestUnitRegoV2PrototypeBoundaries(t *testing.T) {
	for _, kind := range []Type{Access, Approval, Push, Initialization, Intent, Login, Notification, Plan, Task, Trigger} {
		supported := evaluate(t, testEngine(t), Policy{Type: kind, Engine: V1, Body: "package infraweave"}, `{}`)
		proposed, err := prototype(t.Context(), kind, ast.RegoV2, "package infraweave\nimport rego.v2", `{}`, fips140.Enforced())
		if err != nil || !bytes.Equal(supported.Decision, proposed.Decision) {
			t.Fatalf("%s mixed-version preamble changed: %s, %v", kind, proposed.Decision, err)
		}
	}
	for _, name := range []string{"http.send", "net.lookup_ip_addr", "opa.runtime", "rego.parse_module", "time.now_ns", "trace", "exec", "crypto.md5", "crypto.sha1", "crypto.hmac.md5", "crypto.hmac.sha1"} {
		arguments := `input.value`
		switch name {
		case "opa.runtime", "time.now_ns":
			arguments = ""
		case "net.lookup_ip_addr", "crypto.hmac.md5", "crypto.hmac.sha1":
			arguments = `input.value,input.other`
		}
		_, err := prototype(t.Context(), Plan, ast.RegoV2, "package infraweave\ndeny contains \"forbidden\" if "+name+"("+arguments+")", `{}`, true)
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("v2 probe restored removed builtin %s: %v", name, err)
		}
	}
	long := strings.Repeat("x", PrintBytes+10)
	input, _ := json.Marshal(map[string]string{"value": long})
	out, err := prototype(t.Context(), Plan, ast.RegoV2, "package infraweave\ndeny contains \"seen\" if {print(input.value); true}", string(input), fips140.Enforced())
	if err != nil || !out.PrintTruncated || len(out.Print) != PrintBytes {
		t.Fatalf("prototype print capture lost its bound: %d, %v", len(out.Print), err)
	}
}

type corpusFile struct {
	File       string `json:"file"`
	Source     string `json:"source"`
	SHA256     string `json:"sha256"`
	PolicyType string `json:"policyType"`
}
type corpusManifest struct {
	Repository string       `json:"repository"`
	Revision   string       `json:"revision"`
	Files      []corpusFile `json:"files"`
}

func TestUnitRegoMITCompatibilityCorpus(t *testing.T) {
	const root = "../../../test/fixtures/rego-compatibility"
	raw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest corpusManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Revision != "6ff5edb08bc806f323eff5c48426a7762ef1fee7" || len(manifest.Files) != 6 {
		t.Fatal("corpus pin or inventory changed without review")
	}
	files, err := filepath.Glob(filepath.Join(root, "*.rego"))
	if err != nil || len(files) != 5 {
		t.Fatal("corpus source inventory differs from probe cases")
	}
	for _, file := range manifest.Files {
		t.Run(file.File, func(t *testing.T) {
			if filepath.Base(file.File) != file.File || !strings.Contains(file.Source, manifest.Revision) {
				t.Fatal("fixture path or source pin invalid")
			}
			source, err := os.ReadFile(filepath.Join(root, file.File))
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(source)
			if hex.EncodeToString(sum[:]) != file.SHA256 {
				t.Fatal("MIT source differs from pinned bytes")
			}
			if file.File == "LICENSE" {
				return
			}
			body := strings.Replace(string(source), "package spacelift", "package infraweave", 1)
			cases := corpusCases(t, file.File)
			for _, tc := range cases {
				e := testEngine(t)
				baseline := evaluate(t, e, Policy{Type: Type(file.PolicyType), Engine: V0, Body: body}, tc.input)
				for rule, want := range tc.rules {
					if !reflect.DeepEqual(decision(t, baseline)[rule], want) {
						t.Fatalf("%s corpus rule %s: %v, want %v", tc.name, rule, decision(t, baseline)[rule], want)
					}
				}
				v1 := evaluate(t, e, Policy{Type: Type(file.PolicyType), Engine: V1, Body: body}, tc.input)
				v2, err := prototype(t.Context(), Type(file.PolicyType), ast.RegoV2, body, tc.input, fips140.Enforced())
				if err != nil || !bytes.Equal(baseline.Decision, v1.Decision) || !bytes.Equal(baseline.Decision, v2.Decision) {
					t.Fatalf("%s corpus dialect comparison: %s / %s / %s, %v", tc.name, baseline.Decision, v1.Decision, v2.Decision, err)
				}
				t.Logf("%s: supported v0/v1 and prototype v2 complete raw decisions match", tc.name)
			}
		})
	}
}

type corpusCase struct {
	name, input string
	rules       map[string]any
}

func corpusCases(t *testing.T, file string) []corpusCase {
	t.Helper()
	empty := []any{}
	switch file {
	case "access-team.rego":
		return []corpusCase{
			{"engineering", `{"session":{"teams":["Engineering"]}}`, map[string]any{"read": true}},
			{"other", `{"session":{"teams":["Other"]}}`, map[string]any{"read": false}},
		}
	case "approval-count.rego":
		return []corpusCase{
			{"approve", `{"run":{"state":"UNCONFIRMED"},"reviews":{"current":{"approvals":[{},{}],"rejections":[]}}}`, map[string]any{"approve": true, "reject": false}},
			{"reject", `{"run":{"state":"UNCONFIRMED"},"reviews":{"current":{"approvals":[],"rejections":[{},{}]}}}`, map[string]any{"approve": false, "reject": true}},
		}
	case "push-root.rego":
		return []corpusCase{
			{
				"in root",
				`{"stack":{"project_root":"infra/","branch":"main"},"push":{"affected_files":["infra/main.tf"],"branch":"main","tag":""}}`,
				map[string]any{"track": true, "propose": true, "ignore": false},
			},
			{
				"outside root",
				`{"stack":{"project_root":"infra/","branch":"main"},"push":{"affected_files":["docs/readme.md"],"branch":"main","tag":""}}`,
				map[string]any{"track": false, "propose": false, "ignore": true},
			},
		}
	case "trigger-dependencies.rego":
		return []corpusCase{
			{"finished", `{"run":{"state":"FINISHED","type":"TRACKED"}}`, map[string]any{"trigger": []any{"stack-one", "stack-three", "stack-two"}}},
			{"failed", `{"run":{"state":"FAILED","type":"TRACKED"}}`, map[string]any{"trigger": empty}},
		}
	case "plan-sanitized.rego":
		input, _ := json.Marshal(
			map[string]any{
				"terraform": map[string]any{
					"resource_changes": []any{
						map[string]any{"change": map[string]any{"actions": []string{"create"}, "after": map[string]any{"endpoint": sanitize.Value("forbidden.endpoint/webhook")}}},
					},
				},
			},
		)
		return []corpusCase{
			{"forbidden hash", string(input), map[string]any{"deny": []any{"must not target the forbidden endpoint: forbidden.endpoint/webhook"}}},
			{"no resources", `{"terraform":{"resource_changes":[]}}`, map[string]any{"deny": empty}},
		}
	default:
		t.Fatalf("corpus file has no meaningful probe: %s", file)
		return nil
	}
}

// Keep the keyword manifest explicit and independent of upstream additions.
func TestUnitRegoLanguageCapabilityManifest(t *testing.T) {
	for _, fips := range []bool{false, true} {
		caps := capabilitiesFor(fips)
		if !slices.Equal(caps.FutureKeywords, []string{"contains", "every", "if", "in"}) || slices.Contains(caps.Features, ast.FeatureRegoV2) ||
			slices.Contains(caps.Features, ast.FeatureRegoV2Import) {
			t.Fatal("supported engine capabilities widened")
		}
		if len(caps.AllowNet) != 0 {
			t.Fatal("language restriction changed egress boundary")
		}
	}
}
