package runtimeconfig

import (
	"bytes"
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"

	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// Record fixes a run's configuration at its first successful handover.
// Configuration is the public view; Execution retains settings the public
// schema cannot express. Neither contains variables, files or credentials.
type Record struct {
	Configuration Configuration `json:"configuration"`
	Execution     Execution     `json:"execution"`
}

// Execution holds only execution settings absent from Configuration.
// Tool versions and Terragrunt's mode live in Configuration alone.
type Execution struct {
	Vendor                       string          `json:"vendor"`
	VendorConfig                 json.RawMessage `json:"vendorConfig"`
	Tool                         *Tool           `json:"tool,omitempty"`
	TerragruntDownload           bool            `json:"terragruntDownload"`
	EnableWellKnownSecretMasking bool            `json:"enableWellKnownSecretMasking"`
	EnableSensitiveOutputUpload  bool            `json:"enableSensitiveOutputUpload"`
	ManagesState                 bool            `json:"managesState"`
	ProjectRootSpecified         bool            `json:"projectRootSpecified"`
	RunnerImageSpecified         bool            `json:"runnerImageSpecified"`
}

// Tool retains the tool's execution flags, without duplicating its release.
type Tool struct {
	Name         string `json:"name"`
	Download     bool   `json:"download"`
	CPUProfiling bool   `json:"cpuProfiling"`
}

// Configuration is the served RuntimeConfig shape, without execution metadata.
//
//nolint:tagliatelle // the runtime file's YAML keys use snake_case
type Configuration struct {
	AfterApply             []string    `json:"afterApply"             yaml:"after_apply"`
	AfterDestroy           []string    `json:"afterDestroy"           yaml:"after_destroy"`
	AfterInit              []string    `json:"afterInit"              yaml:"after_init"`
	AfterPerform           []string    `json:"afterPerform"           yaml:"after_perform"`
	AfterPlan              []string    `json:"afterPlan"              yaml:"after_plan"`
	AfterRun               []string    `json:"afterRun"               yaml:"after_run"`
	BeforeApply            []string    `json:"beforeApply"            yaml:"before_apply"`
	BeforeDestroy          []string    `json:"beforeDestroy"          yaml:"before_destroy"`
	BeforeInit             []string    `json:"beforeInit"             yaml:"before_init"`
	BeforePerform          []string    `json:"beforePerform"          yaml:"before_perform"`
	BeforePlan             []string    `json:"beforePlan"             yaml:"before_plan"`
	Environment            []Variable  `json:"environment"            yaml:"-"`
	GitSparseCheckoutPaths []string    `json:"gitSparseCheckoutPaths" yaml:"git_sparse_checkout_paths"`
	ProjectRoot            string      `json:"projectRoot"            yaml:"project_root"`
	RunnerImage            string      `json:"runnerImage"            yaml:"runner_image"`
	TerraformVersion       string      `json:"terraformVersion"       yaml:"terraform_version"`
	TerraformWorkflowTool  string      `json:"terraformWorkflowTool"  yaml:"terraform_workflow_tool"`
	Terraform              *Terraform  `json:"terraform,omitempty"    yaml:"-"`
	OpenTofu               *OpenTofu   `json:"opentofu,omitempty"     yaml:"opentofu,omitempty"`
	Terragrunt             *Terragrunt `json:"terragrunt,omitempty"   yaml:"terragrunt,omitempty"`
	YAML                   string      `json:"yaml"                   yaml:"-"`
}

// Variable is a runtime variable's public key and redacted value.
type Variable struct {
	Key   string `json:"key"   yaml:"key"`
	Value string `json:"value" yaml:"value"`
}

// Terraform is the public Terraform runtime block.
//
//nolint:tagliatelle // the runtime file's YAML keys use snake_case
type Terraform struct {
	Version      *string `json:"version,omitempty" yaml:"version,omitempty"`
	WorkflowTool string  `json:"workflowTool"      yaml:"workflow_tool"`
}

// OpenTofu is the public OpenTofu runtime block.
//
//nolint:tagliatelle // runtime YAML uses snake_case; public JSON fixes the CPU acronym
type OpenTofu struct {
	Version                    *string `json:"version,omitempty"          yaml:"version,omitempty"`
	WorkflowTool               string  `json:"workflowTool"               yaml:"workflow_tool"`
	Concise                    bool    `json:"concise"                    yaml:"concise"`
	EnableCPUProfiling         bool    `json:"enableCPUProfiling"         yaml:"enable_cpu_profiling"`
	ExternalStateAccessEnabled bool    `json:"externalStateAccessEnabled" yaml:"external_state_access_enabled"`
	UseSmartSanitization       bool    `json:"useSmartSanitization"       yaml:"use_smart_sanitization"`
}

// Terragrunt is the public Terragrunt runtime block.
//
//nolint:tagliatelle // the runtime file's YAML keys use snake_case
type Terragrunt struct {
	TerragruntVersion                 string  `json:"terragruntVersion"                           yaml:"terragrunt_version"`
	TerraformVersion                  *string `json:"terraformVersion,omitempty"                  yaml:"terraform_version,omitempty"`
	Tool                              string  `json:"tool"                                        yaml:"terragrunt_tool"`
	UseRunAll                         bool    `json:"useRunAll"                                   yaml:"use_run_all"`
	UseSmartSanitization              bool    `json:"useSmartSanitization"                        yaml:"use_smart_sanitization"`
	UseStateManagement                bool    `json:"useStateManagement"                          yaml:"use_state_management"`
	PrefixResourceNamesWithModuleName *bool   `json:"prefixResourceNamesWithModuleName,omitempty" yaml:"prefix_resource_names_with_module_name,omitempty"`
}

// New captures spec's resolved execution configuration. Variables, mounted
// files, run identity, source and tokens are deliberately not copied.
func New(spec workersapi.Spec, managesState bool) (*Record, error) {
	c := Configuration{
		Environment: []Variable{}, GitSparseCheckoutPaths: []string{},
		TerraformWorkflowTool: "TERRAFORM_FOSS",
	}
	c.setHooks(spec.Hooks)
	if spec.Stack.ProjectRoot != nil {
		c.ProjectRoot = *spec.Stack.ProjectRoot
	}
	if spec.RunnerImage != nil {
		c.RunnerImage = *spec.RunnerImage
	}
	e := Execution{
		Vendor: spec.Stack.Vendor, VendorConfig: bytes.Clone(spec.Stack.VendorConfig),
		EnableWellKnownSecretMasking: spec.Stack.EnableWellKnownSecretMasking,
		EnableSensitiveOutputUpload:  spec.Stack.EnableSensitiveOutputUpload,
		ManagesState:                 managesState,
		ProjectRootSpecified:         spec.Stack.ProjectRoot != nil,
		RunnerImageSpecified:         spec.RunnerImage != nil,
	}
	if spec.Tool != nil {
		c.TerraformVersion = spec.Tool.Version
		if spec.Tool.Name == "tofu" {
			c.TerraformWorkflowTool = "OPEN_TOFU"
		}
		e.Tool = &Tool{Name: spec.Tool.Name, Download: spec.Tool.Download, CPUProfiling: spec.Tool.CPUProfiling}
	}
	var settings struct {
		WorkflowTool                      string `json:"workflowTool"`
		Concise                           bool   `json:"concise"`
		EnableCPUProfiling                bool   `json:"enableCPUProfiling"` //nolint:tagliatelle // the settings name it so
		ExternalStateAccessEnabled        bool   `json:"externalStateAccessEnabled"`
		UseSmartSanitization              bool   `json:"useSmartSanitization"`
		UseStateManagement                bool   `json:"useStateManagement"`
		PrefixResourceNamesWithModuleName *bool  `json:"prefixResourceNamesWithModuleName"`
	}
	if len(e.VendorConfig) > 0 {
		if err := json.Unmarshal(e.VendorConfig, &settings); err != nil {
			return nil, fmt.Errorf("runs: read the resolved vendor settings: %w", err)
		}
	}
	if settings.WorkflowTool == "CUSTOM" {
		c.TerraformWorkflowTool = "CUSTOM"
	}
	switch e.Vendor {
	case "terraform":
		c.Terraform = &Terraform{Version: &c.TerraformVersion, WorkflowTool: c.TerraformWorkflowTool}
	case "opentofu":
		workflow := "OPENTOFU"
		if settings.WorkflowTool == "CUSTOM" {
			workflow = "CUSTOM"
		}
		c.OpenTofu = &OpenTofu{
			Version: &c.TerraformVersion, WorkflowTool: workflow,
			Concise: settings.Concise, EnableCPUProfiling: settings.EnableCPUProfiling,
			ExternalStateAccessEnabled: settings.ExternalStateAccessEnabled,
			UseSmartSanitization:       settings.UseSmartSanitization,
		}
	case "terragrunt":
		if spec.Terragrunt != nil {
			c.Terragrunt = &Terragrunt{
				TerragruntVersion: spec.Terragrunt.Version, TerraformVersion: &c.TerraformVersion,
				Tool: c.TerraformWorkflowTool, UseRunAll: spec.Terragrunt.RunAll,
				UseSmartSanitization:              settings.UseSmartSanitization,
				UseStateManagement:                settings.UseStateManagement,
				PrefixResourceNamesWithModuleName: settings.PrefixResourceNamesWithModuleName,
			}
			e.TerragruntDownload = spec.Terragrunt.Download
		}
	}
	// Configuration contains only strings, booleans and slices of strings;
	// its YAML cannot fail and excludes the private execution metadata.
	doc, _ := yaml.Marshal(struct {
		Configuration `yaml:",inline"`
		Environment   map[string]string `yaml:"environment"`
	}{Configuration: c, Environment: map[string]string{}})
	c.YAML = string(doc)
	return &Record{Configuration: c, Execution: e}, nil
}

// Apply restores the recorded settings while leaving run identity, current
// variables/files and source credentials to the handover that reads them.
func (r *Record) Apply(spec *workersapi.Spec) {
	c, e := r.Configuration, r.Execution
	root, image := c.ProjectRoot, c.RunnerImage
	spec.Stack.ProjectRoot, spec.RunnerImage = nil, nil
	if e.ProjectRootSpecified {
		spec.Stack.ProjectRoot = &root
	}
	if e.RunnerImageSpecified {
		spec.RunnerImage = &image
	}
	spec.Stack.Vendor, spec.Stack.VendorConfig = e.Vendor, bytes.Clone(e.VendorConfig)
	spec.Stack.EnableWellKnownSecretMasking = e.EnableWellKnownSecretMasking
	spec.Stack.EnableSensitiveOutputUpload = e.EnableSensitiveOutputUpload
	spec.Hooks = c.Hooks()
	spec.Tool, spec.Terragrunt = nil, nil
	spec.StateBackend = nil
	if e.Tool != nil {
		spec.Tool = &workersapi.SpecTool{
			Name: e.Tool.Name, Version: c.TerraformVersion,
			Download: e.Tool.Download, CPUProfiling: e.Tool.CPUProfiling,
		}
	}
	if c.Terragrunt != nil {
		spec.Terragrunt = &workersapi.SpecTerragrunt{
			Version: c.Terragrunt.TerragruntVersion, RunAll: c.Terragrunt.UseRunAll,
			Download: e.TerragruntDownload,
		}
	}
}

// Hooks answers independent phase lists, including an empty list for each
// phase with no hook, in the worker specification's names.
func (c Configuration) Hooks() map[string][]string {
	return map[string][]string{
		"after_apply": clone(c.AfterApply), "after_destroy": clone(c.AfterDestroy),
		"after_init": clone(c.AfterInit), "after_perform": clone(c.AfterPerform),
		"after_plan": clone(c.AfterPlan), "after_run": clone(c.AfterRun),
		"before_apply": clone(c.BeforeApply), "before_destroy": clone(c.BeforeDestroy),
		"before_init": clone(c.BeforeInit), "before_perform": clone(c.BeforePerform),
		"before_plan": clone(c.BeforePlan),
	}
}

func (c *Configuration) setHooks(h map[string][]string) {
	c.AfterApply, c.AfterDestroy = clone(h["after_apply"]), clone(h["after_destroy"])
	c.AfterInit, c.AfterPerform = clone(h["after_init"]), clone(h["after_perform"])
	c.AfterPlan, c.AfterRun = clone(h["after_plan"]), clone(h["after_run"])
	c.BeforeApply, c.BeforeDestroy = clone(h["before_apply"]), clone(h["before_destroy"])
	c.BeforeInit, c.BeforePerform = clone(h["before_init"]), clone(h["before_perform"])
	c.BeforePlan = clone(h["before_plan"])
}

func clone(s []string) []string { return append([]string{}, s...) }
