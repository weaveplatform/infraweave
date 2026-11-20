package inputs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
)

const maxPlanDocument = 64 << 20

// ErrPlanDocument marks an invalid artifact or unsupported vendor. Projection
// never sanitizes or recovers original values; its source is the opened worker
// artifact, whose mode was applied before sealing.
var ErrPlanDocument = errors.New("invalid policy plan document")

// ApplyPlan sets the minimal Terraform-family block from an already sanitized
// plan.json. Tool metadata inside resource_changes is decoded strictly so a
// newly introduced field fails explicitly instead of disappearing. The other
// top-level vendor document sections stay in the artifact.
func ApplyPlan(in *PlanInput, vendor string, r io.Reader) error {
	raw, err := io.ReadAll(io.LimitReader(r, maxPlanDocument+1))
	if err != nil {
		return fmt.Errorf("read policy plan: %w", err)
	}
	if len(raw) > maxPlanDocument {
		return ErrPlanDocument
	}
	var next PlanInput
	switch vendor {
	case "terraform", "opentofu":
		block, err := projectPlan(raw)
		if err != nil {
			return err
		}
		next.Terraform = Value(block)
		if vendor == "opentofu" {
			next.Opentofu = Value(OpenTofuPlan{Checks: block.Checks, ResourceChanges: block.ResourceChanges, OpentofuVersion: block.TerraformVersion})
		}
	case "terragrunt":
		var document struct {
			Units []struct {
				Path string          `json:"path"`
				Plan json.RawMessage `json:"plan"`
			} `json:"units"`
		}
		if err := decode(raw, &document); err != nil || len(document.Units) == 0 {
			return ErrPlanDocument
		}
		units := make(map[string]TerragruntProject, len(document.Units))
		for _, unit := range document.Units {
			if unit.Path == "" || path.IsAbs(unit.Path) || path.Clean(unit.Path) != unit.Path || unit.Path == ".." || len(unit.Path) > 3 && unit.Path[:3] == "../" {
				return ErrPlanDocument
			}
			if _, exists := units[unit.Path]; exists {
				return ErrPlanDocument
			}
			block, err := projectPlan(unit.Plan)
			if err != nil {
				return fmt.Errorf("project unit %s: %w", unit.Path, err)
			}
			units[unit.Path] = TerragruntProject(block)
		}
		next.Terragrunt = Value(units)
	default:
		return ErrPlanDocument
	}
	in.Terraform, in.Opentofu, in.Terragrunt = next.Terraform, next.Opentofu, next.Terragrunt
	return nil
}

func projectPlan(raw []byte) (TerraformPlan, error) {
	// Read only the ADR's three published sections, retaining absent vs null.
	// The complete tool document intentionally remains in plan.json.
	//nolint:tagliatelle // keys are the infrastructure tool's documented format
	var document struct {
		FormatVersion    string                  `json:"format_version"`
		TerraformVersion Field[string]           `json:"terraform_version"`
		Checks           Field[[]PlanCheck]      `json:"checks"`
		ResourceChanges  Field[[]ResourceChange] `json:"resource_changes"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return TerraformPlan{}, fmt.Errorf("%w: %w", ErrPlanDocument, err)
	}
	if len(document.FormatVersion) < 2 || document.FormatVersion[:2] != "1." {
		return TerraformPlan{}, ErrPlanDocument
	}
	block := TerraformPlan{Checks: document.Checks, ResourceChanges: document.ResourceChanges, TerraformVersion: document.TerraformVersion}
	return block, nil
}
