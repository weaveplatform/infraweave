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

func registerDevelopmentDeferredActions(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(
		`^the opened development deferred-action compatibility plan of the run "(\w+)" uses "(DEFAULT|SMART|NONE)" and stays artifact-only$`,
		func(ctx context.Context, run, mode string) error {
			plain, err := developmentDeferredArtifact(ctx, w, run, mode)
			if err != nil {
				return err
			}
			return developmentDeferredDocument(plain, mode)
		},
	)
	sc.Step(
		`^the opened Terragrunt development deferred-action plan of the run "(\w+)" uses "(DEFAULT|SMART|NONE)" with units "([^"]*)"$`,
		func(ctx context.Context, run, mode, paths string) error {
			plain, err := developmentDeferredArtifact(ctx, w, run, mode)
			if err != nil {
				return err
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
			var root map[string]map[string]json.RawMessage
			if err := json.Unmarshal(projection, &root); err != nil {
				return err
			}
			var got []string
			for _, unit := range envelope.Units {
				got = append(got, unit.Path)
				if err := samePlanSections(unit.Plan, root["terragrunt"][unit.Path], "terraform_version"); err != nil {
					return err
				}
				switch unit.Path {
				case ".":
					if err := developmentDeferredDocument(unit.Plan, mode); err != nil {
						return err
					}
				case "child":
					var child map[string]any
					if err := json.Unmarshal(unit.Plan, &child); err != nil {
						return err
					}
					changes := child["resource_changes"].([]any)
					if len(changes) != 1 {
						return fmt.Errorf("%w: selected child change count differs", harness.ErrStep)
					}
					input := changes[0].(map[string]any)["change"].(map[string]any)["after"].(map[string]any)["input"]
					want := "dummy-action-secret-675"
					if mode != "NONE" {
						want = sanitize.Value(want).(string)
					}
					if input != want {
						return fmt.Errorf("%w: deferred sensitivity did not reach selected unit copy", harness.ErrStep)
					}
				}
			}
			want := strings.Split(paths, ", ")
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) || len(root["terragrunt"]) != len(want) {
				return fmt.Errorf("%w: selected deferred-action paths differ", harness.ErrStep)
			}
			return nil
		},
	)
}

func developmentDeferredArtifact(ctx context.Context, w *harness.World, run, mode string) ([]byte, error) {
	plain, stored, err := w.ReadPlanArtifact(ctx, run)
	if err != nil {
		return nil, err
	}
	const secret = "dummy-action-secret-675"
	if !bytes.HasPrefix(stored, []byte("IWSB")) || bytes.Contains(stored, []byte(secret)) || bytes.Contains(plain, []byte(secret)) != (mode == "NONE") {
		return nil, fmt.Errorf("%w: development deferred-action values or sealing differ", harness.ErrStep)
	}
	return plain, nil
}

func developmentDeferredDocument(plain []byte, mode string) error {
	const secret = "dummy-action-secret-675"
	source, err := os.ReadFile(filepath.Join(harness.RepoRoot, "test", "fixtures", "plan-payloads", "terraform-deferred-actions-development", "plan.json"))
	if err != nil {
		return err
	}
	// Collecting a Terragrunt unit removes surrounding JSON whitespace.
	if mode == "NONE" && !bytes.Equal(bytes.TrimSpace(plain), bytes.TrimSpace(source)) {
		return fmt.Errorf("%w: NONE changed the development producer's bytes", harness.ErrStep)
	}
	var before, after map[string]any
	if json.Unmarshal(source, &before) != nil || json.Unmarshal(plain, &after) != nil {
		return fmt.Errorf("%w: malformed development document", harness.ErrStep)
	}
	// The producer omits action values. Its entire wrapper must stay structural.
	if !reflect.DeepEqual(before["deferred_action_invocations"], after["deferred_action_invocations"]) {
		return fmt.Errorf("%w: deferred action metadata changed", harness.ErrStep)
	}
	original := before["deferred_changes"].([]any)
	got := after["deferred_changes"].([]any)
	if len(original) != len(got) {
		return fmt.Errorf("%w: deferred changes were removed", harness.ErrStep)
	}
	for i, item := range original {
		oldChange := item.(map[string]any)["resource_change"].(map[string]any)["change"].(map[string]any)
		newChange := got[i].(map[string]any)["resource_change"].(map[string]any)["change"].(map[string]any)
		if i == 0 {
			input := newChange["after"].(map[string]any)["input"].(map[string]any)
			want, public := secret, "visible-675"
			if mode != "NONE" {
				want = sanitize.Value(want).(string)
			}
			if mode == "DEFAULT" {
				public = sanitize.Value(public).(string)
			}
			if input["private"] != want || input["copy"] != want || input["public"] != public {
				return fmt.Errorf("%w: deferred values or copies differ", harness.ErrStep)
			}
		}
		delete(oldChange, "after")
		delete(newChange, "after")
	}
	if !reflect.DeepEqual(original, got) {
		return fmt.Errorf("%w: deferred identity, masks or unknowns changed", harness.ErrStep)
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
	for _, family := range []string{"deferred_changes", "action_invocations", "deferred_action_invocations"} {
		if _, present := block[family]; present {
			return fmt.Errorf("%w: full-artifact family became policy-visible", harness.ErrStep)
		}
	}
	return samePlanSections(plain, root["terraform"], "terraform_version")
}
