package runtimeconfig_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/runtimeconfig"
)

func TestUnitRuntimeEnvironmentAliasOrigin(t *testing.T) {
	const secret = "echo runtime-alias-secret"
	for _, tc := range []struct{ name, text, vendor, setting string }{
		{"environment to hook", "environment: {TOKEN: &s \"" + secret + "\"}\nbefore_plan: [*s]", "terraform", "before_plan[0]"},
		{"hook to environment", "before_plan: [&s \"" + secret + "\"]\nenvironment: {TOKEN: *s}", "terraform", "before_plan[0]"},
		{"environment to image", "environment: {TOKEN: &s \"" + secret + "\"}\nrunner_image: *s", "terraform", "runner_image"},
		{"image to environment", "runner_image: &s \"" + secret + "\"\nenvironment: {TOKEN: *s}", "terraform", "runner_image"},
		{"environment to version", "environment: {TOKEN: &s \"" + secret + "\"}\nterraform_version: *s", "terraform", "terraform_version"},
		{"native version to environment", "opentofu: {version: &s \"" + secret + "\"}\nenvironment: {TOKEN: *s}", "opentofu", "opentofu.version"},
		{"environment to Terragrunt version", "environment: {TOKEN: &s \"" + secret + "\"}\nterragrunt: {terragrunt_version: *s}", "terragrunt", "terragrunt.terragrunt_version"},
		{"environment map to native block", "environment: &settings {version: \"" + secret + "\"}\nopentofu: *settings", "opentofu", "opentofu.version"},
		{"native block to environment map", "opentofu: &settings {version: \"" + secret + "\"}\nenvironment: *settings", "opentofu", "opentofu.version"},
		{"native block merged into environment", "opentofu: &settings {version: \"" + secret + "\"}\nenvironment: {<<: *settings}", "opentofu", "opentofu.version"},
		{"shadowed environment merge", "environment: {<<: &defaults {TOKEN: &s \"" + secret + "\"}, TOKEN: safe}\nbefore_plan: [*s]", "terraform", "before_plan[0]"},
		{"shadowed environment merge sequence", "environment: {<<: [{TOKEN: safe}, {TOKEN: &s \"" + secret + "\"}]}\nrunner_image: *s", "terraform", "runner_image"},
		{"shadowed root environment", "<<: {environment: {TOKEN: &s \"" + secret + "\"}}\nenvironment: {TOKEN: safe}\nbefore_plan: [*s]", "terraform", "before_plan[0]"},
		{"winning root setting merge", "environment: {TOKEN: &s \"" + secret + "\"}\n<<: [{runner_image: *s}, {runner_image: safe}]", "terraform", "runner_image"},
		{"environment to root", "environment: {TOKEN: &s \"" + secret + "\"}\nproject_root: *s", "terraform", "project_root"},
		{"environment to sparse path", "environment: {TOKEN: &s \"" + secret + "\"}\ngit_sparse_checkout_paths: [*s]", "terraform", "git_sparse_checkout_paths[0]"},
		{"environment to OpenTofu version", "environment: {TOKEN: &s \"" + secret + "\"}\nopentofu_version: *s", "terraform", "opentofu_version"},
		{"environment to Terragrunt driven version", "environment: {TOKEN: &s \"" + secret + "\"}\nterragrunt: {terraform_version: *s}", "terragrunt", "terragrunt.terraform_version"},
		{"environment to workflow", "environment: {TOKEN: &s CUSTOM}\nterraform_workflow_tool: *s", "terraform", "terraform_workflow_tool"},
		{"native workflow to environment", "opentofu: {workflow_tool: &s CUSTOM}\nenvironment: {TOKEN: *s}", "opentofu", "opentofu.workflow_tool"},
		{"environment to Terragrunt tool", "environment: {TOKEN: &s OPEN_TOFU}\nterragrunt: {terragrunt_tool: *s}", "terragrunt", "terragrunt.terragrunt_tool"},
		{"losing aliased environment key", "environment: {&env environment: baseline}\n<<: { *env : {TOKEN: &s \"" + secret + "\"}}\nbefore_plan: [*s]", "terraform", "before_plan[0]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, err := runtimeconfig.Parse(runtimeYAML(t, tc.text), tc.vendor)
			if err == nil {
				public, _ := json.Marshal(in.Public())
				t.Fatalf("shared environment origin accepted; clear stored value=%t, public value=%t",
					strings.Contains(string(in.Stored()), secret), strings.Contains(string(public), secret))
			}
			inv, ok := errors.AsType[*kernel.Invalid](err)
			if !ok || len(inv.Errors) != 1 || inv.Errors[0].Pointer != "/runtimeConfig/yaml" ||
				inv.Errors[0].Reason != kernel.ReasonRule || !strings.Contains(inv.Errors[0].Message, tc.setting) ||
				strings.Contains(err.Error(), secret) || strings.Contains(inv.Errors[0].Message, secret) {
				t.Fatalf("alias-origin refusal did not safely name %s", tc.setting)
			}
		})
	}
}

func TestUnitRuntimeEnvironmentAliasAllowed(t *testing.T) {
	const secret = "echo runtime-alias-secret"
	for _, tc := range []struct {
		name, text   string
		clear        bool
		secretValues int
	}{
		{"environment only", "environment: {FIRST: &s \"" + secret + "\", SECOND: *s}", false, 2},
		{"environment merge aliases", "environment: {<<: [{FIRST: &s \"" + secret + "\"}, {SECOND: *s}]}", false, 2},
		{"independent identical literals", "environment: {TOKEN: \"" + secret + "\"}\nbefore_plan: [\"" + secret + "\"]", true, 1},
		{"unreferenced environment anchor", "environment: {TOKEN: &s \"" + secret + "\"}\nbefore_plan: [\"" + secret + "\"]", true, 1},
		{"execution aliases only", "before_plan: [&s \"" + secret + "\", *s]\nenvironment: {TOKEN: safe}", true, 0},
		{"explicit execution value shadows merge", "environment: {TOKEN: &s \"" + secret + "\"}\n<<: {runner_image: *s}\nrunner_image: safe", false, 1},
		{"first execution merge wins", "environment: {TOKEN: &s \"" + secret + "\"}\n<<: [{runner_image: safe}, {runner_image: *s}]", false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, err := runtimeconfig.Parse(runtimeYAML(t, tc.text), "terraform")
			if err != nil {
				t.Fatalf("independent/unused origin refused: %v", err)
			}
			public, _ := json.Marshal(in.Public())
			if strings.Contains(string(in.Stored()), secret) != tc.clear || strings.Contains(string(public), secret) != tc.clear {
				t.Fatal("environment-only aliases were exposed or independent execution literals changed")
			}
			values := 0
			for _, variable := range in.Environment {
				if variable.Value == secret {
					values++
				}
			}
			if values != tc.secretValues {
				t.Fatalf("allowed environment aliases retained %d execution values, want %d", values, tc.secretValues)
			}
		})
	}
}

func TestUnitRuntimeEnvironmentKeyOrigin(t *testing.T) {
	const marker = "runtime-key-origin-secret"
	for _, tc := range []struct{ name, text, vendor string }{
		{"anchor key", "&s " + marker + ": kept\nenvironment: {TOKEN: *s}", "terraform"},
		{"alias key", "environment: {TOKEN: &s " + marker + "}\n*s: kept", "terraform"},
		{"duplicate root", "&s " + marker + ": kept\n" + marker + ": again\nenvironment: {TOKEN: *s}", "terraform"},
		{"vendor key", "opentofu: {&s " + marker + ": kept}\nenvironment: {TOKEN: *s}", "opentofu"},
		{"environment key", "environment: {&s " + marker + ": *s}", "terraform"},
		{"duplicate environment", "environment: {&s " + marker + ": *s, " + marker + ": again}", "terraform"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runtimeconfig.Parse(runtimeYAML(t, tc.text), tc.vendor)
			inv, ok := errors.AsType[*kernel.Invalid](err)
			raw := ""
			if ok {
				raw = inv.Detail + fmt.Sprint(inv.Errors)
			}
			if !ok || len(inv.Errors) != 1 || inv.Errors[0].Pointer != "/runtimeConfig/yaml" ||
				inv.Errors[0].Reason != kernel.ReasonRule || strings.Contains(raw, marker) {
				t.Fatalf("sensitive key was not safely refused before diagnostics; marker exposed=%t",
					strings.Contains(raw, marker))
			}
		})
	}
	_, err := runtimeconfig.Parse(runtimeYAML(t, marker+": kept\nenvironment: {TOKEN: "+marker+"}"), "terraform")
	if inv, ok := errors.AsType[*kernel.Invalid](err); !ok || inv.Errors[0].Reason != kernel.ReasonUnknownField {
		t.Fatal("independent identical key literal was classified by its text")
	}
	_, err = runtimeconfig.Parse(runtimeYAML(t, "environment: {TOKEN: {nested: value}}"), "terraform")
	if inv, ok := errors.AsType[*kernel.Invalid](err); !ok || inv.Errors[0].Reason != kernel.ReasonRule {
		t.Fatal("invalid environment containers did not take the privacy-first key refusal")
	}
	for _, value := range []string{"true", "[value]"} {
		_, err := runtimeconfig.Parse(runtimeYAML(t, "environment: {TOKEN: "+value+"}"), "terraform")
		if inv, ok := errors.AsType[*kernel.Invalid](err); !ok || inv.Errors[0].Reason != kernel.ReasonFormat {
			t.Fatal("scalar/list environment values lost their ordinary type refusal")
		}
	}
}
