package http

import (
	"encoding/json"

	api "github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/runtimeconfig"
)

// customOf projects the validated typed trigger input without its sealed values.
func customOf(raw json.RawMessage) *api.RuntimeConfig {
	if len(raw) == 0 {
		return nil
	}
	input, err := runtimeconfig.Read(raw)
	if err != nil {
		return nil
	}
	return resolvedOf(&runtimeconfig.Record{Configuration: input.Public()})
}

// resolvedOf projects only the public configuration. Execution metadata is
// an internal storage detail and never enters a run read or its YAML.
func resolvedOf(record *runtimeconfig.Record) *api.RuntimeConfig {
	if record == nil {
		return nil
	}
	c := record.Configuration
	out := &api.RuntimeConfig{
		AfterApply: c.AfterApply, AfterDestroy: c.AfterDestroy, AfterInit: c.AfterInit,
		AfterPerform: c.AfterPerform, AfterPlan: c.AfterPlan, AfterRun: c.AfterRun,
		BeforeApply: c.BeforeApply, BeforeDestroy: c.BeforeDestroy, BeforeInit: c.BeforeInit,
		BeforePerform: c.BeforePerform, BeforePlan: c.BeforePlan,
		ProjectRoot: c.ProjectRoot, RunnerImage: c.RunnerImage,
		TerraformVersion:       c.TerraformVersion,
		TerraformWorkflowTool:  api.TerraformWorkflowTool(c.TerraformWorkflowTool),
		GitSparseCheckoutPaths: c.GitSparseCheckoutPaths, Environment: []api.EnvVar{}, Yaml: c.YAML,
	}
	for _, v := range c.Environment {
		out.Environment = append(out.Environment, api.EnvVar{Key: v.Key, Value: ""})
	}
	if t := c.Terraform; t != nil {
		out.Terraform = &api.TerraformRuntimeConfig{
			Version: t.Version, WorkflowTool: api.TerraformWorkflowTool(t.WorkflowTool),
		}
	}
	if t := c.OpenTofu; t != nil {
		out.Opentofu = &api.OpenTofuRuntimeConfig{
			Version: t.Version, WorkflowTool: api.OpenTofuWorkflowTool(t.WorkflowTool),
			Concise: t.Concise, EnableCPUProfiling: t.EnableCPUProfiling,
			ExternalStateAccessEnabled: t.ExternalStateAccessEnabled, UseSmartSanitization: t.UseSmartSanitization,
		}
	}
	if t := c.Terragrunt; t != nil {
		out.Terragrunt = &api.TerragruntRuntimeConfig{
			TerragruntVersion: t.TerragruntVersion, TerraformVersion: t.TerraformVersion,
			Tool: api.TerragruntTool(t.Tool), UseRunAll: t.UseRunAll,
			UseSmartSanitization: t.UseSmartSanitization, UseStateManagement: t.UseStateManagement,
			PrefixResourceNamesWithModuleName: t.PrefixResourceNamesWithModuleName,
		}
	}
	return out
}
