package runtimeconfig

import (
	"encoding/json"
	"maps"
	"slices"

	configapi "github.com/weaveplatform/infraweave/internal/configuration/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// File is one repository file's configuration for a stack. YAML is a bounded,
// redacted projection of its entry, or defaults when no entry exists.
// Defaults and Entry retain execution values only until they are sealed.
type File struct {
	Version  string
	Defaults Input
	Entry    *Input
	YAML     string
	Notices  []string
	Refusal  *FileError
}

// Settings applies entry/default replacement before stack precedence. Own
// empty hooks have no persisted presence flag; only stored phases override a
// version-1 default phase. No setter intent is reconstructed.
func (f File) Settings(st stacksapi.Settings) Input {
	lower := f.Defaults
	if f.Version == "1" {
		lower.Hooks = maps.Clone(lower.Hooks)
		for phase := range st.Hooks {
			delete(lower.Hooks, phase)
		}
		if st.ProjectRoot != nil {
			lower.ProjectRoot = nil
		}
		if st.RunnerImage != nil {
			lower.RunnerImage = nil
		}
		if st.TerraformVersion != nil {
			lower.TerraformVersion, lower.OpenTofuVersion = nil, nil
		}
		if len(st.GitSparseCheckoutPaths) > 0 {
			lower.GitSparseCheckoutPaths = nil
		}
		var vendor map[string]json.RawMessage
		_ = json.Unmarshal(st.VendorConfig, &vendor) // stored settings were validated
		if _, present := vendor["version"]; present {
			lower.TerraformVersion, lower.OpenTofuVersion = nil, nil
		}
		if len(vendor) > 0 {
			lower.OpenTofu, lower.Terragrunt = nil, nil
		}
		if _, present := vendor["workflowTool"]; present {
			lower.WorkflowTool = nil
		}
	}
	if f.Entry != nil {
		lower = Overlay(lower, *f.Entry)
	}
	return lower
}

// Variables selects the entry's entire map over defaults, including an empty
// map. Version-1 defaults yield only to stack-own variables, while both file
// levels beat context values. Custom trigger overrides are applied afterward.
func (f File) Variables(elements []configapi.Element) []Variable {
	if f.Entry != nil && f.Entry.Environment != nil {
		return slices.Clone(f.Entry.Environment)
	}
	values := slices.Clone(f.Defaults.Environment)
	if f.Version != "1" {
		return values
	}
	own := map[string]bool{}
	for _, e := range elements {
		if !e.FileMount && e.Source == configapi.SourceStack {
			own[e.Name] = true
		}
	}
	return slices.DeleteFunc(values, func(v Variable) bool { return own[v.Key] })
}

// Overlay replaces each supplied top-level value, keeping hook presence per
// phase. Environment maps and native vendor blocks are replaced as a whole.
func Overlay(lower, upper Input) Input {
	out := lower
	out.Hooks = maps.Clone(lower.Hooks)
	if out.Hooks == nil {
		out.Hooks = map[string][]string{}
	}
	for phase, commands := range upper.Hooks {
		out.Hooks[phase] = slices.Clone(commands)
	}
	if upper.Environment != nil {
		out.Environment = slices.Clone(upper.Environment)
	}
	if upper.ProjectRoot != nil {
		out.ProjectRoot = upper.ProjectRoot
	}
	if upper.RunnerImage != nil {
		out.RunnerImage = upper.RunnerImage
	}
	if upper.GitSparseCheckoutPaths != nil {
		out.GitSparseCheckoutPaths = upper.GitSparseCheckoutPaths
	}
	if upper.TerraformVersion != nil {
		out.TerraformVersion = upper.TerraformVersion
	}
	if upper.OpenTofuVersion != nil {
		out.OpenTofuVersion = upper.OpenTofuVersion
	}
	if upper.WorkflowTool != nil {
		out.WorkflowTool = upper.WorkflowTool
	}
	if upper.OpenTofu != nil {
		out.OpenTofu = upper.OpenTofu
	}
	if upper.Terragrunt != nil {
		out.Terragrunt = upper.Terragrunt
	}
	return out
}
