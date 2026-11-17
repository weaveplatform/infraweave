package http

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/weaveplatform/infraweave/internal/runs/internal/core/runtimeconfig"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

func TestUnitResolvedConfigurationProjection(t *testing.T) {
	if resolvedOf(nil) != nil {
		t.Fatal("an unrecorded run has a runtime configuration")
	}
	for _, vendor := range []string{"terraform", "opentofu", "terragrunt"} {
		t.Run(vendor, func(t *testing.T) {
			root, image := "infra", "runner:fixed"
			spec := workersapi.Spec{
				Stack: workersapi.SpecStack{
					Vendor:      vendor,
					ProjectRoot: &root,
					VendorConfig: json.RawMessage(
						`{"concise":true,"enableCPUProfiling":true,"externalStateAccessEnabled":true,"useSmartSanitization":true,"useStateManagement":true,"prefixResourceNamesWithModuleName":true}`,
					),
				},
				RunnerImage: &image,
				Tool:        &workersapi.SpecTool{Name: "tofu", Version: "1.13.9", Download: true},
				Hooks:       map[string][]string{"before_apply": {"echo fixed"}},
			}
			if vendor == "terragrunt" {
				spec.Terragrunt = &workersapi.SpecTerragrunt{Version: "1.2.2", RunAll: true}
			}
			record, err := runtimeconfig.New(spec, true)
			if err != nil {
				t.Fatal(err)
			}
			wantJSON, err := json.Marshal(record.Configuration)
			if err != nil {
				t.Fatal(err)
			}
			gotJSON, err := json.Marshal(resolvedOf(record))
			if err != nil {
				t.Fatal(err)
			}
			var got, want any
			if json.Unmarshal(gotJSON, &got) != nil || json.Unmarshal(wantJSON, &want) != nil ||
				!reflect.DeepEqual(got, want) || bytes.Contains(gotJSON, []byte(`"execution"`)) ||
				bytes.Contains(gotJSON, []byte(`"vendorConfig"`)) {
				t.Fatalf("served configuration: %s; want %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestUnitResolvedConfigurationRedactsVariables(t *testing.T) {
	record := &runtimeconfig.Record{Configuration: runtimeconfig.Configuration{
		Environment: []runtimeconfig.Variable{{Key: "TOKEN", Value: "must-not-echo"}},
	}}
	got := resolvedOf(record)
	data, err := json.Marshal(got)
	if err != nil || len(got.Environment) != 1 || got.Environment[0].Key != "TOKEN" ||
		got.Environment[0].Value != "" || bytes.Contains(data, []byte("must-not-echo")) {
		t.Fatalf("variable projection: %s, %v", data, err)
	}
}
