package runtimeconfig

import (
	"encoding/json"
	"fmt"
	"slices"

	"gopkg.in/yaml.v3"
)

// Input is one stack's runtime overrides. Pointer fields and hook-map membership
// distinguish omitted values from explicitly empty ones. YAMLInput retains only
// the input form for diagnostics; the original YAML, which may hold secrets, is
// never stored. Stored strips environment values before persistence.
type Input struct {
	Hooks                  map[string][]string `json:"hooks,omitempty"`
	Environment            []Variable          `json:"environment,omitempty"`
	ProjectRoot            *string             `json:"projectRoot,omitempty"`
	RunnerImage            *string             `json:"runnerImage,omitempty"`
	GitSparseCheckoutPaths *[]string           `json:"gitSparseCheckoutPaths,omitempty"`
	TerraformVersion       *string             `json:"terraformVersion,omitempty"`
	OpenTofuVersion        *string             `json:"opentofuVersion,omitempty"`
	WorkflowTool           *string             `json:"workflowTool,omitempty"`
	OpenTofu               *OpenTofuInput      `json:"opentofu,omitempty"`
	Terragrunt             *TerragruntInput    `json:"terragrunt,omitempty"`
	YAMLInput              bool                `json:"yamlInput,omitempty"`
}

// OpenTofuInput contains the native OpenTofu settings a runtime file supports.
type OpenTofuInput struct {
	Version      *string `json:"version,omitempty"`
	WorkflowTool *string `json:"workflowTool,omitempty"`
}

// TerragruntInput replaces the whole vendor block. Omitted switches are false;
// an omitted tool uses Terraform. State management is recorded, but never
// changes the stack's backend, which was chosen at stack creation.
type TerragruntInput struct {
	Tool                 *string `json:"tool,omitempty"`
	Version              *string `json:"version,omitempty"`
	TerraformVersion     *string `json:"terraformVersion,omitempty"`
	UseRunAll            bool    `json:"useRunAll"`
	UseSmartSanitization bool    `json:"useSmartSanitization"`
	UseStateManagement   bool    `json:"useStateManagement"`
}

// Stored is the typed configuration without variable values. Its values are
// supplied separately to the lifecycle's sealed override write.
func (in Input) Stored() json.RawMessage {
	in.Environment = in.Keys()
	doc, _ := json.Marshal(in) // strings, booleans and lists always encode
	return doc
}

// Read reads the typed trigger configuration after storage. It never reparses
// the original YAML or obtains values from an unsealed column.
func Read(raw json.RawMessage) (Input, error) {
	var in Input
	if len(raw) == 0 {
		return in, nil
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return Input{}, fmt.Errorf("runs: read the stored runtime configuration: %w", err)
	}
	return in, nil
}

// Keys is an independent variable list with every value redacted.
func (in Input) Keys() []Variable {
	out := make([]Variable, len(in.Environment))
	for i, v := range in.Environment {
		out[i] = Variable{Key: v.Key}
	}
	return out
}

// Public projects the trigger's configuration into the served RuntimeConfig
// shape, including its supported runtime-file keys as redacted YAML.
func (in Input) Public() Configuration {
	c := Configuration{
		Environment: in.Keys(), GitSparseCheckoutPaths: []string{},
		TerraformWorkflowTool: "TERRAFORM_FOSS",
	}
	c.setHooks(in.Hooks)
	if in.ProjectRoot != nil {
		c.ProjectRoot = *in.ProjectRoot
	}
	if in.RunnerImage != nil {
		c.RunnerImage = *in.RunnerImage
	}
	if in.GitSparseCheckoutPaths != nil {
		c.GitSparseCheckoutPaths = clone(*in.GitSparseCheckoutPaths)
	}
	if in.TerraformVersion != nil {
		c.TerraformVersion = *in.TerraformVersion
	}
	if in.WorkflowTool != nil {
		c.TerraformWorkflowTool = *in.WorkflowTool
	}
	if t := in.OpenTofu; t != nil {
		workflow := "OPENTOFU"
		if t.WorkflowTool != nil {
			workflow = *t.WorkflowTool
		}
		c.TerraformWorkflowTool = "OPEN_TOFU"
		if workflow == "CUSTOM" {
			c.TerraformWorkflowTool = workflow
		}
		if t.Version != nil {
			c.TerraformVersion = *t.Version
		}
		c.OpenTofu = &OpenTofu{Version: t.Version, WorkflowTool: workflow}
	}
	if t := in.Terragrunt; t != nil {
		tool, release := "TERRAFORM_FOSS", ""
		if t.Tool != nil {
			tool = *t.Tool
		}
		if t.Version != nil {
			release = *t.Version
		}
		c.TerraformWorkflowTool = tool
		if t.TerraformVersion != nil {
			c.TerraformVersion = *t.TerraformVersion
		}
		c.Terragrunt = &Terragrunt{
			TerragruntVersion: release, TerraformVersion: t.TerraformVersion, Tool: tool,
			UseRunAll: t.UseRunAll, UseSmartSanitization: t.UseSmartSanitization,
			UseStateManagement: t.UseStateManagement,
		}
	}
	c.YAML = in.RenderYAML()
	return c
}

// RenderYAML answers only supported runtime-file keys and never a variable's
// value. It describes configuration; private execution flags stay in the record.
func (in Input) RenderYAML() string {
	out := map[string]any{}
	for phase, cmds := range in.Hooks {
		out[phase] = clone(cmds)
	}
	if in.Environment != nil {
		env := make(map[string]string, len(in.Environment))
		for _, v := range in.Environment {
			env[v.Key] = ""
		}
		out["environment"] = env
	}
	for key, value := range map[string]*string{
		"project_root": in.ProjectRoot, "runner_image": in.RunnerImage,
		"terraform_version": in.TerraformVersion, "opentofu_version": in.OpenTofuVersion,
		"terraform_workflow_tool": in.WorkflowTool,
	} {
		if value != nil {
			out[key] = *value
		}
	}
	if in.GitSparseCheckoutPaths != nil {
		out["git_sparse_checkout_paths"] = slices.Clone(*in.GitSparseCheckoutPaths)
	}
	if t := in.OpenTofu; t != nil {
		block := map[string]any{}
		if t.Version != nil {
			block["version"] = *t.Version
		}
		if t.WorkflowTool != nil {
			block["workflow_tool"] = *t.WorkflowTool
		}
		out["opentofu"] = block
	}
	if t := in.Terragrunt; t != nil {
		block := map[string]any{
			"use_run_all": t.UseRunAll, "use_smart_sanitization": t.UseSmartSanitization,
			"use_state_management": t.UseStateManagement,
		}
		for key, value := range map[string]*string{
			"terragrunt_tool": t.Tool, "terragrunt_version": t.Version, "terraform_version": t.TerraformVersion,
		} {
			if value != nil {
				block[key] = *value
			}
		}
		out["terragrunt"] = block
	}
	doc, _ := yaml.Marshal(out) // only strings, booleans, maps and lists
	return string(doc)
}

// RuntimeYAML describes a resolved configuration with only keys the runtime
// file supports. Public JSON and private execution metadata retain vendor flags
// that the file cannot override, such as native OpenTofu's profiling flag.
func (c Configuration) RuntimeYAML() string {
	root, image := c.ProjectRoot, c.RunnerImage
	paths := clone(c.GitSparseCheckoutPaths)
	in := Input{
		Hooks: c.Hooks(), Environment: c.Environment, ProjectRoot: &root,
		RunnerImage: &image, GitSparseCheckoutPaths: &paths,
	}
	if t := c.Terraform; t != nil {
		in.TerraformVersion, in.WorkflowTool = t.Version, &t.WorkflowTool
	}
	if t := c.OpenTofu; t != nil {
		in.OpenTofu = &OpenTofuInput{Version: t.Version, WorkflowTool: &t.WorkflowTool}
	}
	if t := c.Terragrunt; t != nil {
		in.Terragrunt = &TerragruntInput{
			Tool: &t.Tool, Version: &t.TerragruntVersion, TerraformVersion: t.TerraformVersion,
			UseRunAll: t.UseRunAll, UseSmartSanitization: t.UseSmartSanitization,
			UseStateManagement: t.UseStateManagement,
		}
	}
	return in.RenderYAML()
}
