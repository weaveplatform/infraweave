package sanitize_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac/plan"
	"github.com/weaveplatform/infraweave/internal/iac/sanitize"
)

const document = `{
 "format_version":"1.2", "terraform_version":"1.15.0",
 "variables":{"secret":{"value":"secret"},"public":{"value":"public"}},
 "resource_changes":[{"address":"thing.a","type":"thing","name":"a","provider_name":"example",
  "change":{"actions":["update"],"before":{"token":"secret"},"before_sensitive":{"token":true},
   "after":{"name":"public","token":"secret","nested":[{"token":"secret","name":"public"}],"n":9007199254740993,"b":true,"nil":null},
   "after_sensitive":{"token":true,"nested":[{"token":true}]}}}],
 "resource_drift":[{"address":"thing.a","change":{"before":"secret","after":"secret","before_sensitive":true,"after_sensitive":true}}],
 "output_changes":{"token":{"before":"secret","after":"secret","before_sensitive":true,"after_sensitive":true}},
 "planned_values":{"outputs":{"token":{"value":"secret","sensitive":true}},
  "root_module":{"resources":[{"address":"thing.a","values":{"token":"secret","name":"public"},"sensitive_values":{"token":true}}],
   "child_modules":[{"address":"module.child","resources":[{"address":"module.child.thing.a","values":{"token":"secret"},"sensitive_values":true}]}]}},
 "prior_state":{"values":{"outputs":{"token":{"value":"secret","sensitive":true}},
  "root_module":{"resources":[{"address":"thing.a","values":{"token":"secret"},"sensitive_values":{"token":true}}]}}},
 "configuration":{"root_module":{"variables":{"secret":{"sensitive":true},"public":{}},
  "resources":[{"expressions":{"password":{"constant_value":"literal"},"nested":{"constant_value":{"token":["literal",7,false,null]}}}}],
  "module_calls":{"child":{"module":{"resources":[{"expressions":{"secret":{"constant_value":"literal"}}}]}}}}}
}`

func decode(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := d.Decode(&doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func at(t *testing.T, value any, path string) any {
	t.Helper()
	for token := range strings.SplitSeq(path, "/") {
		switch v := value.(type) {
		case map[string]any:
			var ok bool
			value, ok = v[token]
			if !ok {
				t.Fatalf("missing %s in %s", token, path)
			}
		case []any:
			i, err := strconv.Atoi(token)
			if err != nil || i < 0 || i >= len(v) {
				t.Fatalf("invalid index %s in %s", token, path)
			}
			value = v[i]
		default:
			t.Fatalf("non-container in %s", path)
		}
	}
	return value
}

func TestUnitValue(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   any
		want any
	}{
		{name: "captured hello", in: "hello", want: "938b9824"},
		{name: "captured second vector", in: "infraweave-capture-test", want: "9597bf47"},
		{name: "empty", in: "", want: "7852b855"},
		{name: "unicode", in: "é", want: "2ba69c4c"},
		{name: "number", in: json.Number("9007199254740993"), want: json.Number("9007199254740993")},
		{name: "null", in: nil, want: nil},
		{name: "boolean", in: true, want: true},
		{name: "object", in: map[string]any{"value": "plain"}, want: map[string]any{"value": "plain"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitize.Value(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Value = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUnitPlanModes(t *testing.T) {
	for _, mode := range []sanitize.Mode{sanitize.Default, sanitize.Smart, sanitize.None} {
		t.Run(string(mode), func(t *testing.T) {
			var out bytes.Buffer
			if err := sanitize.Plan(&out, strings.NewReader(document), mode); err != nil {
				t.Fatal(err)
			}
			if mode == sanitize.None && out.String() != document {
				t.Fatal("NONE changed the input bytes")
			}
			doc := decode(t, out.Bytes())
			for _, path := range []string{
				"variables/secret/value", "resource_changes/0/change/before/token",
				"resource_changes/0/change/after/token", "resource_changes/0/change/after/nested/0/token",
				"resource_drift/0/change/before", "resource_drift/0/change/after",
				"output_changes/token/before", "output_changes/token/after",
				"planned_values/outputs/token/value", "planned_values/root_module/resources/0/values/token",
				"planned_values/root_module/child_modules/0/resources/0/values/token",
				"prior_state/values/outputs/token/value", "prior_state/values/root_module/resources/0/values/token",
			} {
				want := any("secret")
				if mode != sanitize.None {
					want = sanitize.Value(want)
				}
				if got := at(t, doc, path); got != want {
					t.Errorf("%s = %v, want %v", path, got, want)
				}
			}
			for _, path := range []string{
				"variables/public/value", "resource_changes/0/change/after/name",
				"resource_changes/0/change/after/nested/0/name", "planned_values/root_module/resources/0/values/name",
			} {
				want := any("public")
				if mode == sanitize.Default {
					want = sanitize.Value(want)
				}
				if got := at(t, doc, path); got != want {
					t.Errorf("%s = %v, want %v", path, got, want)
				}
			}
			for _, path := range []string{
				"configuration/root_module/resources/0/expressions/password/constant_value",
				"configuration/root_module/resources/0/expressions/nested/constant_value/token/0",
				"configuration/root_module/module_calls/child/module/resources/0/expressions/secret/constant_value",
			} {
				want := any("literal")
				if mode != sanitize.None {
					want = sanitize.Value(want)
				}
				if got := at(t, doc, path); got != want {
					t.Errorf("%s = %v, want %v", path, got, want)
				}
			}
			original := decode(t, []byte(document))
			for _, path := range []string{
				"format_version", "terraform_version", "resource_changes/0/address", "resource_changes/0/type",
				"resource_changes/0/name", "resource_changes/0/provider_name", "resource_changes/0/change/actions",
				"resource_changes/0/change/after/n", "resource_changes/0/change/after/b", "resource_changes/0/change/after/nil",
				"planned_values/root_module/child_modules/0/address",
			} {
				if got, want := at(t, doc, path), at(t, original, path); !reflect.DeepEqual(got, want) {
					t.Errorf("metadata/type changed at %s: %v != %v", path, got, want)
				}
			}
		})
	}
}

func TestUnitRecordedPlans(t *testing.T) {
	root := filepath.Join("..", "..", "..", "test", "fixtures", "vendor-documents")
	files := []string{}
	for _, pattern := range []string{"terraform/plan-*.json", "opentofu/plan-*.json", "terragrunt/json-out/*/tfplan.json"} {
		found, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		if err != nil || len(found) == 0 {
			t.Fatalf("fixture pattern %s: %v", pattern, err)
		}
		files = append(files, found...)
	}
	if len(files) != 16 || sanitize.MaxSize != plan.MaxSize {
		t.Fatalf("fixture count/bound drift: %d plans, %d != %d", len(files), sanitize.MaxSize, plan.MaxSize)
	}
	for _, file := range files {
		for _, mode := range []sanitize.Mode{sanitize.Default, sanitize.Smart, sanitize.None} {
			t.Run(filepath.ToSlash(file)+"/"+string(mode), func(t *testing.T) {
				data, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				var out bytes.Buffer
				if err := sanitize.Plan(&out, bytes.NewReader(data), mode); err != nil {
					t.Fatal(err)
				}
				if mode == sanitize.None {
					if !bytes.Equal(out.Bytes(), data) {
						t.Fatal("NONE changed the recorded plan")
					}
					return
				}
				for _, secret := range []string{"dummy-api-key", "dummy-password", "dummy-secret", "dummy-write-only", "dummy-marked", "dummy-nested", "dummy-tg-api-key"} {
					if bytes.Contains(out.Bytes(), []byte(secret)) {
						t.Errorf("recorded secret %s survived", secret)
					}
				}
				before, err := plan.Parse(bytes.NewReader(data))
				if err != nil {
					t.Fatal(err)
				}
				after, err := plan.Parse(bytes.NewReader(out.Bytes()))
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("resource metadata/delta changed: %v", err)
				}
			})
		}
	}
}

func TestUnitTerragruntUnits(t *testing.T) {
	input := `{"units":[{"path":"","plan":` + document + `},{"path":"app","plan":` + document + `}]}`
	for _, mode := range []sanitize.Mode{sanitize.Default, sanitize.Smart, sanitize.None} {
		t.Run(string(mode), func(t *testing.T) {
			var out bytes.Buffer
			if err := sanitize.Plan(&out, strings.NewReader(input), mode); err != nil {
				t.Fatal(err)
			}
			doc := decode(t, out.Bytes())
			if at(t, doc, "units/0/path") != "" || at(t, doc, "units/1/path") != "app" {
				t.Fatal("unit paths changed")
			}
			want := any("secret")
			if mode != sanitize.None {
				want = sanitize.Value(want)
			}
			for _, path := range []string{"units/0/plan/variables/secret/value", "units/1/plan/variables/secret/value"} {
				if at(t, doc, path) != want {
					t.Fatalf("unit secret survived at %s", path)
				}
			}
		})
	}
}

func TestUnitSmartCopies(t *testing.T) {
	first := "secret"
	second := sanitize.Value(first).(string)
	input := map[string]any{
		"format_version":    "1.0",
		"terraform_version": first,
		"variables": map[string]any{
			"one": map[string]any{"value": first},
			"two": map[string]any{"value": second},
		},
		"configuration": map[string]any{"root_module": map[string]any{"variables": map[string]any{
			"one": map[string]any{"sensitive": true},
			"two": map[string]any{"sensitive": true, "default": map[string]any{"constant_value": second}},
		}}},
		"output_changes": map[string]any{"copy": map[string]any{"before": first, "after": second}},
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := sanitize.Plan(&out, bytes.NewReader(data), sanitize.Smart); err != nil {
		t.Fatal(err)
	}
	doc := decode(t, out.Bytes())
	for path, want := range map[string]any{
		"terraform_version":          first,
		"variables/one/value":        second,
		"variables/two/value":        sanitize.Value(second),
		"output_changes/copy/before": second,
		"output_changes/copy/after":  sanitize.Value(second),
		"configuration/root_module/variables/two/default/constant_value": sanitize.Value(second),
	} {
		if got := at(t, doc, path); got != want {
			t.Errorf("%s = %v, want %v", path, got, want)
		}
	}
}

func TestUnitSmartAcrossUnits(t *testing.T) {
	input := `{"units":[{"path":"secret","plan":{"format_version":"1.0","checks":[{"message":"secret"}],"output_changes":{"copy":{"after":"secret"}}}},{"path":"app","plan":` + document + `}]}`
	var out bytes.Buffer
	if err := sanitize.Plan(&out, strings.NewReader(input), sanitize.Smart); err != nil {
		t.Fatal(err)
	}
	doc := decode(t, out.Bytes())
	if got := at(t, doc, "units/0/plan/output_changes/copy/after"); got != sanitize.Value("secret") {
		t.Fatalf("unmarked value earlier than its marked unit survived: %v", got)
	}
	if at(t, doc, "units/0/path") != "secret" || len(at(t, doc, "units/0/plan/checks").([]any)) != 0 {
		t.Fatal("unit metadata changed or sanitized checks were retained")
	}
	// Resource attributes can be named variables; only a module's variables
	// map holds declarations with defaults and sensitivity metadata.
	input = `{"format_version":"1.0","configuration":{"root_module":{"resources":[{"expressions":{"variables":{"constant_value":"literal"}}}]}}}`
	out.Reset()
	if err := sanitize.Plan(&out, strings.NewReader(input), sanitize.Smart); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out.Bytes(), []byte("literal")) {
		t.Fatal("a constant on the variables attribute survived")
	}
}

type failedIO struct{}

var errIO = errors.New("test I/O failure")

func (failedIO) Read([]byte) (int, error)  { return 0, errIO }
func (failedIO) Write([]byte) (int, error) { return 0, errIO }

func TestUnitRefusals(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `{}`, `{"format_version":"2.0"}`, `{"format_version":"1.0"} {}`,
		`{"units":null}`, `{"units":1}`, `{"units":[null]}`, `{"units":[{"path":0,"plan":{}}]}`, `{"units":[{"path":"x","plan":null}]}`,
		`{"format_version":"1.0","resource_changes":{}}`, `{"format_version":"1.0","resource_changes":[{}]}`,
		`{"format_version":"1.0","output_changes":{"x":null}}`, `{"format_version":"1.0","planned_values":false}`,
		`{"format_version":"1.0","prior_state":[]}`, `{"format_version":"1.0","prior_state":{"values":1}}`,
		`{"format_version":"1.0","planned_values":{"outputs":1}}`, `{"format_version":"1.0","planned_values":{"outputs":{"x":null}}}`,
		`{"format_version":"1.0","planned_values":{"root_module":[]}}`, `{"format_version":"1.0","configuration":1}`,
		`{"format_version":"1.0","configuration":{"root_module":1}}`, `{"format_version":"1.0","variables":{"x":null}}`,
		`{"format_version":"1.0","output_changes":[]}`, `{"format_version":"1.0","variables":false}`,
		`{"format_version":"1.0","configuration":{"root_module":{"variables":[]}}}`,
		`{"format_version":"1.0","configuration":{"root_module":{"variables":{"x":3}}}}`,
		`{"format_version":"1.0","configuration":{"root_module":{"variables":{"x":{"sensitive":2,"default":"secret"}}}}}`,
		`{"format_version":"1.0","configuration":{"root_module":{"variables":{"x":{"sensitive":{},"default":{}}}}}}`,
		`{"format_version":"1.0","variables":{"x":{"value":{}}},"configuration":{"root_module":{"variables":{"x":{"sensitive":{}}}}}}`,
		`{"format_version":"1.0","planned_values":{"outputs":{"x":{"value":{},"sensitive":{}}}}}`,
		`{"format_version":"1.0","planned_values":{"outputs":{"x":{"value":"secret","sensitive":null}}}}`,
		`{"format_version":"1.0","planned_values":{"root_module":{"resources":[{"values":[],"sensitive_values":{"token":true}}]}}}`,
		`{"format_version":"1.0","planned_values":{"root_module":{"child_modules":false}}}`,
		`{"format_version":"1.0","resource_changes":[{"change":{"after_sensitive":[2],"after":["secret"]}}]}`,
		`{"units":[{"path":"x","plan":{"format_version":"1.0","checks":false}}]}`,
		`{"format_version":"1.0","output_changes":{"x":{"after":"secret","after_sensitive":2}}}`,
		`{"format_version":"1.0","output_changes":{"x":{"after":"secret","after_sensitive":{"x":true}}}}`,
		`{"format_version":"1.0","output_changes":{"x":{"after":["secret"],"after_sensitive":[]}}}`,
	} {
		t.Run(input, func(t *testing.T) {
			for _, mode := range []sanitize.Mode{sanitize.Default, sanitize.Smart, sanitize.None} {
				var out bytes.Buffer
				if err := sanitize.Plan(&out, strings.NewReader(input), mode); !errors.Is(err, sanitize.ErrMalformed) || out.Len() != 0 {
					t.Fatalf("mode %s: err %v, wrote %d bytes", mode, err, out.Len())
				}
			}
		})
	}
	var out bytes.Buffer
	if err := sanitize.Plan(&out, strings.NewReader(document), "invalid"); !errors.Is(err, sanitize.ErrMode) {
		t.Fatal(err)
	}
	if err := sanitize.Plan(&out, failedIO{}, sanitize.Default); !errors.Is(err, errIO) {
		t.Fatal(err)
	}
	for _, mode := range []sanitize.Mode{sanitize.Default, sanitize.None} {
		if err := sanitize.Plan(failedIO{}, strings.NewReader(document), mode); !errors.Is(err, errIO) {
			t.Fatal(err)
		}
	}
	large := io.LimitReader(bytes.NewReader(bytes.Repeat([]byte{' '}, sanitize.MaxSize+1)), sanitize.MaxSize+1)
	if err := sanitize.Plan(&out, large, sanitize.Default); !errors.Is(err, sanitize.ErrTooLarge) || out.Len() != 0 {
		t.Fatal(err)
	}
}

func FuzzPlan(f *testing.F) {
	f.Add(document)
	f.Add(`{"units":[]}`)
	f.Add(`{"format_version":"1.0"}`)
	f.Fuzz(func(t *testing.T, input string) {
		var out bytes.Buffer
		if err := sanitize.Plan(&out, strings.NewReader(input), sanitize.Smart); err != nil {
			if out.Len() != 0 {
				t.Fatal("invalid input wrote an artifact")
			}
			return
		}
		if !json.Valid(out.Bytes()) {
			t.Fatal("invalid output JSON")
		}
	})
}
