package runs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/iac/sanitize"
	"github.com/weaveplatform/infraweave/internal/policies/policytest"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func registerActionSanitization(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(
		`^the opened action plan of the run "(\w+)" uses "(DEFAULT|SMART|NONE)" and stays artifact-only$`,
		func(ctx context.Context, run, mode string) error {
			plain, stored, err := w.ReadPlanArtifact(ctx, run)
			if err != nil {
				return err
			}
			const secret = "dummy-action-secret-675"
			if !bytes.HasPrefix(stored, []byte("IWSB")) || bytes.Contains(stored, []byte(secret)) {
				return fmt.Errorf("%w: action plan is not sealed", harness.ErrStep)
			}
			if err := actionValues(plain, mode); err != nil {
				return err
			}
			projection, err := policytest.PlanVendor("terraform", bytes.NewReader(plain))
			if err != nil {
				return err
			}
			var root map[string]json.RawMessage
			if err := json.Unmarshal(projection, &root); err != nil {
				return err
			}
			var block map[string]json.RawMessage
			if err := json.Unmarshal(root["terraform"], &block); err != nil {
				return err
			}
			if _, leaked := block["action_invocations"]; leaked || bytes.Contains(projection, []byte(secret)) {
				return fmt.Errorf("%w: action family escaped the artifact-only contract", harness.ErrStep)
			}
			return samePlanSections(plain, root["terraform"], "terraform_version")
		},
	)
	sc.Step(
		`^the opened Terragrunt action plan of the run "(\w+)" uses "(DEFAULT|SMART|NONE)" with units "([^"]*)"$`,
		func(ctx context.Context, run, mode, paths string) error {
			plain, stored, err := w.ReadPlanArtifact(ctx, run)
			if err != nil {
				return err
			}
			const secret = "dummy-action-secret-675"
			if !bytes.HasPrefix(stored, []byte("IWSB")) || bytes.Contains(stored, []byte(secret)) {
				return fmt.Errorf("%w: action envelope is not sealed", harness.ErrStep)
			}
			if bytes.Contains(plain, []byte(secret)) != (mode == "NONE") {
				return fmt.Errorf("%w: unit action values escaped the mode", harness.ErrStep)
			}
			var envelope struct {
				Units []struct {
					Path string          `json:"path"`
					Plan json.RawMessage `json:"plan"`
				} `json:"units"`
			}
			if err := json.Unmarshal(plain, &envelope); err != nil {
				return err
			}
			projection, err := policytest.PlanVendor("terragrunt", bytes.NewReader(plain))
			if err != nil {
				return err
			}
			var root map[string]json.RawMessage
			if err := json.Unmarshal(projection, &root); err != nil {
				return err
			}
			var units map[string]json.RawMessage
			if err := json.Unmarshal(root["terragrunt"], &units); err != nil {
				return err
			}
			got := make([]string, 0, len(envelope.Units))
			for _, u := range envelope.Units {
				got = append(got, u.Path)
				if err := samePlanSections(u.Plan, units[u.Path], "terraform_version"); err != nil {
					return err
				}
				var block map[string]json.RawMessage
				if err := json.Unmarshal(units[u.Path], &block); err != nil {
					return err
				}
				if _, present := block["action_invocations"]; present {
					return fmt.Errorf("%w: action family became policy-visible", harness.ErrStep)
				}
				if u.Path == "." {
					if err := actionValues(u.Plan, mode); err != nil {
						return err
					}
				}
				if u.Path == "child" {
					var child struct {
						Changes []struct {
							Change struct {
								After struct {
									Input string `json:"input"`
								} `json:"after"`
							} `json:"change"`
						} `json:"resource_changes"` //nolint:tagliatelle // Terraform JSON spelling.
					}
					if err := json.Unmarshal(u.Plan, &child); err != nil {
						return err
					}
					want := secret
					if mode != "NONE" {
						want = sanitize.Value(secret).(string)
					}
					if len(child.Changes) != 1 || child.Changes[0].Change.After.Input != want {
						return fmt.Errorf("%w: action-sensitive copy escaped across units", harness.ErrStep)
					}
				}
			}
			want := strings.Split(paths, ", ")
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) || len(units) != len(want) {
				return fmt.Errorf("%w: selected action unit paths differ", harness.ErrStep)
			}
			return nil
		},
	)
	sc.Step(
		`^the opened deferred-resource compatibility plan of the run "(\w+)" uses "(DEFAULT|SMART|NONE)" and stays artifact-only$`,
		func(ctx context.Context, run, mode string) error {
			plain, stored, err := w.ReadPlanArtifact(ctx, run)
			if err != nil {
				return err
			}
			const secret = "dummy-action-secret-675"
			if !bytes.HasPrefix(stored, []byte("IWSB")) || bytes.Contains(stored, []byte(secret)) || bytes.Contains(plain, []byte(secret)) != (mode == "NONE") {
				return fmt.Errorf("%w: deferred compatibility values or sealing differ", harness.ErrStep)
			}
			source, err := os.ReadFile(filepath.Join(harness.RepoRoot, "test", "fixtures", "plan-payloads", "terraform-deferred", "plan.json"))
			if err != nil {
				return err
			}
			if mode == "NONE" && !bytes.Equal(plain, source) {
				return fmt.Errorf("%w: NONE changed the captured document", harness.ErrStep)
			}
			var before, after map[string]any
			if json.Unmarshal(source, &before) != nil || json.Unmarshal(plain, &after) != nil {
				return fmt.Errorf("%w: malformed compatibility document", harness.ErrStep)
			}
			original := before["deferred_changes"].([]any)[0].(map[string]any)
			got := after["deferred_changes"].([]any)[0].(map[string]any)
			bc := original["resource_change"].(map[string]any)["change"].(map[string]any)
			ac := got["resource_change"].(map[string]any)["change"].(map[string]any)
			input := ac["after"].(map[string]any)["input"].(map[string]any)
			want, public := secret, "visible-675"
			if mode != "NONE" {
				want = sanitize.Value(want).(string)
			}
			if mode == "DEFAULT" {
				public = sanitize.Value(public).(string)
			}
			if input["private"] != want || input["copy"] != want || input["public"] != public {
				return fmt.Errorf("%w: deferred values or copies escaped the mode", harness.ErrStep)
			}
			delete(bc, "after")
			delete(ac, "after")
			if !reflect.DeepEqual(original, got) {
				return fmt.Errorf("%w: deferred metadata, unknowns or masks changed", harness.ErrStep)
			}
			projection, err := policytest.PlanVendor("terraform", bytes.NewReader(plain))
			if err != nil {
				return err
			}
			var root map[string]json.RawMessage
			if err := json.Unmarshal(projection, &root); err != nil {
				return err
			}
			var block map[string]json.RawMessage
			if err := json.Unmarshal(root["terraform"], &block); err != nil {
				return err
			}
			if _, present := block["deferred_changes"]; present {
				return fmt.Errorf("%w: deferred family became policy-visible", harness.ErrStep)
			}
			return samePlanSections(plain, root["terraform"], "terraform_version")
		},
	)
}

func actionValues(plain []byte, mode string) error {
	const secret = "dummy-action-secret-675"
	if bytes.Contains(plain, []byte(secret)) != (mode == "NONE") {
		return fmt.Errorf("%w: action sensitive copies differ", harness.ErrStep)
	}
	var plan struct {
		Actions []struct {
			Address string `json:"address"`
			Values  struct {
				Stdin       string            `json:"stdin"`
				Environment map[string]string `json:"environment"`
				Arguments   []any             `json:"arguments"`
			} `json:"config_values"` //nolint:tagliatelle // Terraform JSON spelling.
		} `json:"action_invocations"` //nolint:tagliatelle // Terraform JSON spelling.
	}
	if err := json.Unmarshal(plain, &plan); err != nil {
		return err
	}
	if len(plan.Actions) != 1 || plan.Actions[0].Address != "action.local_command.capture" {
		return fmt.Errorf("%w: native action identity was changed", harness.ErrStep)
	}
	a := plan.Actions[0]
	want := secret
	if mode != "NONE" {
		want = sanitize.Value(secret).(string)
	}
	if a.Values.Stdin != want || a.Values.Environment["private"] != want || a.Values.Environment["copy"] != want ||
		len(a.Values.Arguments) != 3 || a.Values.Arguments[1] != want || a.Values.Arguments[2] != nil {
		return fmt.Errorf("%w: action values, copies or unknowns were changed incorrectly", harness.ErrStep)
	}
	public := "visible-675"
	if mode == "DEFAULT" {
		public = sanitize.Value(public).(string)
	}
	if a.Values.Environment["public"] != public || a.Values.Arguments[0] != public {
		return fmt.Errorf("%w: action public values differ", harness.ErrStep)
	}

	return nil
}
