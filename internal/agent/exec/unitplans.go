package exec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/weaveplatform/infraweave/internal/iac/plan"
	"github.com/weaveplatform/infraweave/internal/iac/sanitize"
)

// unitPlanFile is each selected unit's show -json document under UnitPlans.
//
// ref: https://docs.terragrunt.com/reference/cli/commands/run/#json-out-dir
const unitPlanFile = "tfplan.json"

type unitDocument struct {
	Path string          `json:"path"`
	Plan json.RawMessage `json:"plan"`
}

// unitPlans validates the selected plans, sums their structural delta and
// collects their unchanged documents. save transforms the whole envelope
// before sealing, so SMART can recognize sensitive copies across units.
func (r *run) unitPlans() (plan.Plan, error) {
	root, err := os.OpenRoot(r.options.UnitPlans)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return plan.Plan{}, errNoUnitPlans
	case err != nil:
		return plan.Plan{}, fmt.Errorf("open their directory: %w", err)
	}
	defer root.Close()
	var out plan.Plan
	var units []unitDocument
	var size int64
	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != unitPlanFile {
			return err
		}
		rel := path.Dir(p)
		if !r.h.Spec.Terragrunt.RunAll && rel != "." {
			return nil
		}
		file, err := root.Open(filepath.FromSlash(p))
		if err != nil {
			return fmt.Errorf("open unit %s: %w", unitName(rel), err)
		}
		defer file.Close()
		raw, err := io.ReadAll(io.LimitReader(file, sanitize.MaxSize-size+1))
		if err != nil {
			return fmt.Errorf("read unit %s: %w", unitName(rel), err)
		}
		size += int64(len(raw))
		if size > sanitize.MaxSize {
			return sanitize.ErrTooLarge
		}
		parsed, err := plan.Parse(bytes.NewReader(raw))
		if err != nil {
			return fmt.Errorf("the unit %s: %w", unitName(rel), err)
		}
		units = append(units, unitDocument{Path: rel, Plan: raw})
		out.Delta = sum(out.Delta, parsed.Delta)
		return nil
	})
	if err != nil {
		return plan.Plan{}, err //nolint:wrapcheck // the walk names the unit or path
	}
	if len(units) == 0 {
		return plan.Plan{}, errNoUnitPlans
	}
	slices.SortFunc(units, func(a, b unitDocument) int { return strings.Compare(a.Path, b.Path) })
	if err := r.writeUnits(units); err != nil {
		return plan.Plan{}, err
	}
	return out, nil
}

// rootUnitPlan wraps the single-unit show fallback in the same upload format.
func (r *run) rootUnitPlan() error {
	file, err := os.Open(r.options.PlanJSON)
	if err != nil {
		return fmt.Errorf("read root unit plan: %w", err)
	}
	raw, err := io.ReadAll(io.LimitReader(file, sanitize.MaxSize+1))
	closeErr := file.Close()
	if err != nil {
		return fmt.Errorf("read root unit plan: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close root unit plan: %w", closeErr)
	}
	if len(raw) > sanitize.MaxSize {
		return sanitize.ErrTooLarge
	}
	return r.writeUnits([]unitDocument{{Path: ".", Plan: raw}})
}

func (r *run) writeUnits(units []unitDocument) error {
	file, err := os.OpenFile(r.options.PlanJSON, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open units document: %w", err)
	}
	err = json.NewEncoder(file).Encode(struct {
		Units []unitDocument `json:"units"`
	}{units})
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write units document: %w", err)
	}
	return nil
}
