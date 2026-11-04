package specref

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const terraformJSON = `{
  "format_version": "1.0",
  "provider_schemas": {
    "registry.terraform.io/spacelift-io/spacelift": {
      "resource_schemas": {
        "spacelift_stack": {"block": {
          "description": "A stack.",
          "attributes": {"name": {"type": "string", "description": "Name.", "description_kind": "plain", "required": true}},
          "block_types": {"vendor": {"nesting_mode": "list", "max_items": 1, "block": {"attributes": {"version": {"type": "string", "optional": true}}}}}
        }}
      },
      "data_source_schemas": {"spacelift_stack": {"block": {"deprecated": true}}}
    }
  }
}`

func TestUnitParseTerraformSchema(t *testing.T) {
	tf, err := ParseTerraformSchema([]byte(terraformJSON))
	if err != nil {
		t.Fatal(err)
	}
	stack := tf.Resources["spacelift_stack"]
	if stack.Description != "A stack." || !stack.Attributes["name"].Required ||
		stack.BlockTypes["vendor"].MaxItems != 1 ||
		!stack.BlockTypes["vendor"].Block.Attributes["version"].Optional ||
		!tf.DataSources["spacelift_stack"].Deprecated {
		t.Errorf("schema = %+v", tf)
	}
	if _, err := ParseTerraformSchema(
		[]byte(`{"provider_schemas":{}}`),
	); !errors.Is(
		err,
		ErrProviderMissing,
	) {
		t.Errorf("err = %v", err)
	}
	if _, err := ParseTerraformSchema([]byte("{")); err == nil {
		t.Error("bad json: want error")
	}
}

func TestUnitFetchTerraformSchema(t *testing.T) {
	var calls []string
	run := func(_ context.Context, dir, name string, args ...string) ([]byte, error) {
		main, err := os.ReadFile(filepath.Join(dir, "main.tf"))
		if err != nil {
			return nil, err
		}
		if !strings.Contains(string(main), `version = "1.57.0"`) {
			return nil, fmt.Errorf("main.tf = %s", main)
		}
		calls = append(calls, name+" "+strings.Join(args, " "))
		return []byte(terraformJSON), nil
	}
	out, err := FetchTerraformSchema(context.Background(), run, "tf", "1.57.0")
	if err != nil || string(out) != terraformJSON {
		t.Fatalf("out = %s, err = %v", out, err)
	}
	want := []string{"tf init -backend=false -input=false -no-color", "tf providers schema -json"}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %v", calls)
	}
	for _, v := range []string{"latest", "1.0", `1.0.0" }`, "~> 1.0"} {
		if _, err := FetchTerraformSchema(
			context.Background(),
			run,
			"tf",
			v,
		); !errors.Is(
			err,
			ErrBadVersion,
		) {
			t.Errorf("%q: err = %v", v, err)
		}
	}
	boom := errors.New("boom")
	fail := func(context.Context, string, string, ...string) ([]byte, error) { return nil, boom }
	if _, err := FetchTerraformSchema(
		context.Background(),
		fail,
		"tf",
		"1.0.0",
	); !errors.Is(
		err,
		boom,
	) {
		t.Errorf("err = %v", err)
	}
}

// TestUnitHelperProcess is not a test: ExecRunner tests run this binary as
// a stand-in for terraform.
func TestUnitHelperProcess(t *testing.T) {
	switch os.Getenv("SPECREF_HELPER") {
	case "ok":
		fmt.Print("schema")
		os.Exit(0)
	case "fail":
		fmt.Fprint(os.Stderr, "no provider")
		os.Exit(3)
	}
}

func TestUnitExecRunner(t *testing.T) {
	t.Setenv("SPECREF_HELPER", "ok")
	out, err := ExecRunner(
		context.Background(),
		t.TempDir(),
		os.Args[0],
		"-test.run=^TestUnitHelperProcess$",
	)
	if err != nil || string(out) != "schema" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	t.Setenv("SPECREF_HELPER", "fail")
	_, err = ExecRunner(
		context.Background(),
		t.TempDir(),
		os.Args[0],
		"-test.run=^TestUnitHelperProcess$",
	)
	if err == nil || !strings.Contains(err.Error(), "no provider") {
		t.Fatalf("err = %v, want stderr in the message", err)
	}
}
