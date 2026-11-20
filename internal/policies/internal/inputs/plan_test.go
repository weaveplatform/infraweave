package inputs

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Every recorded full TF/Tofu plan must keep every change key and type through
// the narrowed policy block, while unrelated sections stay in the artifact.
func TestUnitProjectRecordedPlans(t *testing.T) {
	files, err := filepath.Glob("../../../iac/plan/testdata/*/*.json")
	if err != nil || len(files) != 6 {
		t.Fatalf("recorded plan inventory: %d, %v", len(files), err)
	}
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			vendor := "terraform"
			if filepath.Base(filepath.Dir(file)) == "tofu" {
				vendor = "opentofu"
			}
			var in PlanInput
			if err := ApplyPlan(&in, vendor, bytes.NewReader(raw)); err != nil {
				t.Fatal(err)
			}
			block, _ := in.Terraform.Get()
			encoded, err := json.Marshal(block)
			if err != nil {
				t.Fatal(err)
			}
			compareProjected(t, raw, encoded)
			if vendor == "opentofu" {
				tofu, ok := in.Opentofu.Get()
				if !ok {
					t.Fatal("missing opentofu block")
				}
				if !reflect.DeepEqual(tofu.ResourceChanges, block.ResourceChanges) || !reflect.DeepEqual(tofu.OpentofuVersion, block.TerraformVersion) {
					t.Fatal("OpenTofu alias differs")
				}
			}
			schema := compile(t, contractCompiler(t), "TerraformPlan")
			if err := schema.Validate(jsonValue(t, encoded)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func compareProjected(t *testing.T, raw, encoded []byte) {
	t.Helper()
	var source, projected map[string]json.RawMessage
	if json.Unmarshal(raw, &source) != nil || json.Unmarshal(encoded, &projected) != nil {
		t.Fatal("invalid projection")
	}
	for _, key := range []string{"resource_changes", "checks", "terraform_version"} {
		got, gok := projected[key]
		want, wok := source[key]
		if gok != wok || gok && !reflect.DeepEqual(jsonValue(t, got), jsonValue(t, want)) {
			t.Fatalf("projection changed %s", key)
		}
	}
	if len(projected) > 3 {
		t.Fatal("unpublished top-level section entered policy input")
	}
}

func TestUnitProjectPlanMetadata(t *testing.T) {
	const raw = `{"format_version":"1.2","terraform_version":"1.17.2","resource_changes":[{"address":"terraform_data.x[0]","previous_address":"terraform_data.old[0]","action_reason":"replace_because_cannot_update","index":0,"index_unknown":false,"deposed":"deadbeef","module_address":"module.a","mode":"managed","type":"terraform_data","name":"x","provider_name":"terraform.io/builtin/terraform","change":{"actions":["delete","create"],"before":null,"after":{"input":"hash"},"after_unknown":{"output":true},"before_sensitive":false,"after_sensitive":{"input":true},"replace_paths":[["input",0]],"importing":{"id":"hash","unknown":false,"identity":{"id":"hash"}},"generated_config":"hash","before_identity":null,"after_identity":{"id":"hash"}}}],"checks":[]}`
	var in PlanInput
	if err := ApplyPlan(&in, "terraform", strings.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	block, _ := in.Terraform.Get()
	encoded, err := json.Marshal(block)
	if err != nil {
		t.Fatal(err)
	}
	compareProjected(t, []byte(raw), encoded)
	if err := compile(t, contractCompiler(t), "TerraformPlan").Validate(jsonValue(t, encoded)); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`"key"`, `0`, `-1`} {
		var step PathStep
		if err := json.Unmarshal([]byte(value), &step); err != nil {
			t.Fatal(err)
		}
		out, err := json.Marshal(step)
		if err != nil || string(out) != value {
			t.Fatalf("path round trip %q: %s, %v", value, out, err)
		}
	}
	for _, value := range []string{`null`, `false`, `1.2`, `{}`, `[]`, ``} {
		var step PathStep
		if step.UnmarshalJSON([]byte(value)) == nil {
			t.Fatalf("bad path step accepted: %q", value)
		}
	}
}

type brokenPlanReader struct{}

func (brokenPlanReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestUnitProjectPlanBoundaries(t *testing.T) {
	for _, raw := range []string{
		`null`, `{}`, `{"format_version":"2.0"}`, `{"format_version":"1.2","resource_changes":[{"unknown_vendor_member":true}]}`,
		`{"format_version":"1.2","resource_changes":[{"change":{"unknown_delta_member":1}}]}`,
	} {
		var in PlanInput
		if ApplyPlan(&in, "terraform", strings.NewReader(raw)) == nil {
			t.Fatalf("bad or incomplete document accepted: %s", raw)
		}
	}
	var in PlanInput
	if err := ApplyPlan(&in, "other", strings.NewReader(`{}`)); !errors.Is(err, ErrPlanDocument) {
		t.Fatal(err)
	}
	if err := ApplyPlan(&in, "terraform", brokenPlanReader{}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
	if err := ApplyPlan(&in, "terraform", io.LimitReader(zeroPlanReader{}, maxPlanDocument+1)); !errors.Is(err, ErrPlanDocument) {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"format_version":"1.2"}`, `{"format_version":"1.2","resource_changes":null,"checks":[]}`, `{"format_version":"1.2","resource_changes":[],"checks":[]}`} {
		if err := ApplyPlan(&in, "terraform", strings.NewReader(raw)); err != nil {
			t.Fatal(err)
		}
		block, _ := in.Terraform.Get()
		encoded, _ := json.Marshal(block)
		compareProjected(t, []byte(raw), encoded)
	}
}

type zeroPlanReader struct{}

func (zeroPlanReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestUnitProjectTerragruntPlans(t *testing.T) {
	const unit = `{"format_version":"1.2","terraform_version":"1.17.2","checks":[],"resource_changes":null}`
	raw := `{"units":[{"path":".","plan":` + unit + `},{"path":"child","plan":` + unit + `}]}`
	var in PlanInput
	if err := ApplyPlan(&in, "terragrunt", strings.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	units, ok := in.Terragrunt.Get()
	if !ok || len(units) != 2 {
		t.Fatal("missing units")
	}
	for _, name := range []string{".", "child"} {
		encoded, err := json.Marshal(units[name])
		if err != nil {
			t.Fatal(err)
		}
		compareProjected(t, []byte(unit), encoded)
		if err := compile(t, contractCompiler(t), "TerragruntProject").Validate(jsonValue(t, encoded)); err != nil {
			t.Fatal(err)
		}
	}
	if err := ApplyPlan(&in, "terraform", strings.NewReader(unit)); err != nil || in.Terragrunt.Present() {
		t.Fatalf("stale vendor survived: %v", err)
	}
	for _, raw := range []string{`{}`, `{"units":null}`, `{"units":[]}`, `{"units":[{"path":"a","plan":{}}]}`, `{"units":[{"path":"a","plan":` + unit + `},{"path":"a","plan":` + unit + `}]}`} {
		if ApplyPlan(&in, "terragrunt", strings.NewReader(raw)) == nil {
			t.Fatalf("bad units accepted: %s", raw)
		}
	}
	for _, path := range []string{"", "/abs", "../escape", "a/../b", ".."} {
		pathJSON, _ := json.Marshal(path)
		raw := `{"units":[{"path":` + string(pathJSON) + `,"plan":` + unit + `}]}`
		if ApplyPlan(&in, "terragrunt", strings.NewReader(raw)) == nil {
			t.Fatalf("bad unit path accepted: %q", path)
		}
	}
}
