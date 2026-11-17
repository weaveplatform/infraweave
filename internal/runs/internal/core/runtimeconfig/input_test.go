package runtimeconfig_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/runs/internal/core/runtimeconfig"
)

func TestUnitRuntimeInput(t *testing.T) {
	fields := map[string]any{
		"environment": []runtimeconfig.Variable{{Key: "LEVEL", Value: "not-in-a-read-456"}},
		"projectRoot": "infra", "runnerImage": "runner:custom", "gitSparseCheckoutPaths": []string{"infra"},
	}
	for _, name := range []string{
		"beforeInit", "afterInit", "beforePlan", "afterPlan", "beforeApply", "afterApply",
		"beforePerform", "afterPerform", "beforeDestroy", "afterDestroy", "afterRun",
	} {
		fields[name] = []string{"echo " + name}
	}
	fields["afterApply"] = []string{}
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	in, err := runtimeconfig.Parse(raw, "terraform")
	if err != nil || len(in.Hooks) != 11 || len(in.Hooks["after_apply"]) != 0 || in.ProjectRoot == nil ||
		*in.ProjectRoot != "infra" || in.RunnerImage == nil || *in.RunnerImage != "runner:custom" ||
		in.GitSparseCheckoutPaths == nil || !reflect.DeepEqual(*in.GitSparseCheckoutPaths, []string{"infra"}) {
		t.Fatalf("fields input = %+v, %v", in, err)
	}
	stored := in.Stored()
	public := in.Public()
	doc, err := json.Marshal(public)
	if err != nil || bytes.Contains(stored, []byte("not-in-a-read-456")) ||
		bytes.Contains(doc, []byte("not-in-a-read-456")) || strings.Contains(public.YAML, "not-in-a-read-456") ||
		in.Environment[0].Value != "not-in-a-read-456" {
		t.Fatalf("variable redaction changed input or leaked values: %s / %s, %v", stored, doc, err)
	}
	loaded, err := runtimeconfig.Read(stored)
	if err != nil || len(loaded.Environment) != 1 || loaded.Environment[0] != (runtimeconfig.Variable{Key: "LEVEL"}) {
		t.Fatalf("stored input = %+v, %v", loaded, err)
	}
	parsed, err := runtimeconfig.Parse(runtimeYAML(t, public.YAML), "terraform")
	parsed.YAMLInput = false
	if err != nil || !reflect.DeepEqual(parsed, loaded) {
		t.Fatalf("rendered YAML round trip = %+v, want %+v, %v", parsed, loaded, err)
	}
	if empty, err := runtimeconfig.Read(nil); err != nil || len(empty.Hooks) != 0 {
		t.Fatalf("absent stored input = %+v, %v", empty, err)
	}
	if _, err := runtimeconfig.Read(json.RawMessage(`{`)); err == nil {
		t.Error("invalid stored configuration was read")
	}
	if in, err := runtimeconfig.Parse(
		json.RawMessage(`{"beforePlan":null,"environment":null,"projectRoot":null,"runnerImage":null,"gitSparseCheckoutPaths":null,"yaml":null}`),
		"terraform",
	); err != nil || len(in.Hooks) != 0 || in.Environment != nil || in.ProjectRoot != nil || in.RunnerImage != nil ||
		in.GitSparseCheckoutPaths != nil {
		t.Fatalf("known nullable fields were not omitted: %+v, %v", in, err)
	}
}
