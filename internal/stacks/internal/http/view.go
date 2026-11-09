package http

import (
	"encoding/json"
	"fmt"
	"slices"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/iac"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/core"
)

// viewOf is st as the API answers it, in the space slug, with the
// actions held there and whether the caller starred it. The fields later
// phases own read their empty values: no runs, so the state NONE, which
// is terminal, and no blocker; nothing attached, no dependencies and no
// managed resources.
func viewOf(
	st core.Stack,
	space string,
	held []accessapi.Action,
	starred bool,
) (api.Stack, error) {
	vendor, err := vendorView(st)
	if err != nil {
		return api.Stack{}, err
	}
	v := api.Stack{
		Id:                           st.Slug,
		Name:                         st.Name,
		Description:                  st.Description,
		Space:                        space,
		SpaceDetails:                 api.SpaceRef{Id: space},
		Kind:                         api.StackKindStack,
		AllowedActions:               actions(held),
		CanRead:                      true,
		CanWrite:                     slices.Contains(held, accessapi.ActionSpaceWrite),
		Starred:                      starred,
		Labels:                       orEmpty(st.Labels),
		Repository:                   st.Repository,
		RepositoryURL:                st.RepositoryURL,
		Branch:                       st.Branch,
		Provider:                     api.VCSProvider(st.Provider),
		Namespace:                    st.Namespace,
		ProjectRoot:                  st.ProjectRoot,
		AdditionalProjectGlobs:       orEmpty(st.AdditionalProjectGlobs),
		GitSparseCheckoutPaths:       sparse(st.GitSparseCheckoutPaths),
		RunnerImage:                  st.RunnerImage,
		TerraformVersion:             st.TerraformVersion,
		VendorConfig:                 vendor,
		Hooks:                        hooksOf(st.Hooks),
		Autodeploy:                   st.Autodeploy,
		Autoretry:                    st.Autoretry,
		ProtectFromDeletion:          st.ProtectFromDeletion,
		IsDisabled:                   st.Disabled,
		LocalPreviewEnabled:          st.LocalPreviewEnabled,
		GithubActionDeploy:           st.GithubActionDeploy,
		EnableSensitiveOutputUpload:  st.EnableSensitiveOutputUpload,
		EnableWellKnownSecretMasking: st.EnableWellKnownSecretMasking,
		SkipVendorBinaryDownload:     st.SkipVendorBinaryDownload,
		ManagesStateFile:             st.ManagesState,
		CreatedAt:                    unix(st.CreatedAt.Unix()),
		State:                        api.StackStateNONE,
		StateIsTerminal:              true,
		AttachedAwsIntegrations:      []api.StackAwsIntegrationAttachment{},
		AttachedAzureIntegrations:    []api.StackAzureIntegrationAttachment{},
		AttachedContexts:             []api.StackContextAttachment{},
		AttachedPolicies:             []api.StackPolicyAttachment{},
		Config:                       []api.ConfigElement{},
		RuntimeConfig:                []api.ConfigElementWithSource{},
		DependenciesFullGraph:        []api.StackDependency{},
		DependsOn:                    []api.StackDependency{},
		IsDependedOnBy:               []api.StackDependency{},
		Entities:                     []api.ManagedEntity{},
		ModuleVersionsUsed:           []api.Version{},
		Outputs:                      []api.StackOutput{},
		PullRequests:                 []api.PullRequest{},
		RepositoryTags:               []string{},
		ScheduledDeletes:             []api.ScheduledDelete{},
		ScheduledRuns:                []api.ScheduledRun{},
		ScheduledTasks:               []api.ScheduledTask{},
	}
	if l := st.Lock; l != nil {
		at := unix(l.At.Unix())
		v.LockedAt, v.LockedBy = &at, &l.By
		if l.Note != "" {
			v.LockNote = &l.Note
		}
	}
	return v, nil
}

// vendorView is st's vendorConfig in the shape of its vendor, each
// setting its input left out at its default. A Terraform stack's version
// is its terraformVersion when it has one, which takes precedence.
func vendorView(st core.Stack) (*api.StackConfigVendor, error) {
	var c struct {
		Version                    *string `json:"version"`
		WorkflowTool               *string `json:"workflowTool"`
		Workspace                  *string `json:"workspace"`
		ExternalStateAccessEnabled bool    `json:"externalStateAccessEnabled"`
		UseSmartSanitization       bool    `json:"useSmartSanitization"`
		Concise                    bool    `json:"concise"`
		EnableCPUProfiling         bool    `json:"enableCPUProfiling"` //nolint:tagliatelle // the reference names it so
	}
	if err := json.Unmarshal(st.VendorConfig, &c); err != nil {
		return nil, fmt.Errorf("stacks: vendorConfig of %s: %w", st.Slug, err)
	}
	var out api.StackConfigVendor
	var err error
	switch iac.Kind(st.Vendor) {
	case iac.OpenTofu:
		tool := api.OpenTofuWorkflowToolOPENTOFU
		if c.WorkflowTool != nil {
			tool = api.OpenTofuWorkflowTool(*c.WorkflowTool)
		}
		err = out.FromStackConfigVendorOpenTofu(api.StackConfigVendorOpenTofu{
			Kind:    api.StackConfigVendorOpenTofuKindStackConfigVendorOpenTofu,
			Version: c.Version, WorkflowTool: tool, Workspace: c.Workspace,
			ExternalStateAccessEnabled: c.ExternalStateAccessEnabled,
			UseSmartSanitization:       c.UseSmartSanitization,
			Concise:                    c.Concise, EnableCPUProfiling: c.EnableCPUProfiling,
			SupportedFeatures: []api.OpenTofuFeatureCheck{},
		})
	default:
		tool := api.TerraformWorkflowToolTERRAFORMFOSS
		if c.WorkflowTool != nil {
			tool = api.TerraformWorkflowTool(*c.WorkflowTool)
		}
		version := c.Version
		if st.TerraformVersion != nil {
			version = st.TerraformVersion
		}
		err = out.FromStackConfigVendorTerraform(api.StackConfigVendorTerraform{
			Kind:    api.StackConfigVendorTerraformKindStackConfigVendorTerraform,
			Version: version, WorkflowTool: tool, Workspace: c.Workspace,
			ExternalStateAccessEnabled: c.ExternalStateAccessEnabled,
			UseSmartSanitization:       c.UseSmartSanitization,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("stacks: vendorConfig of %s: %w", st.Slug, err)
	}
	return &out, nil
}

// hooksOf is each phase's commands, an empty list for a phase with none.
func hooksOf(h map[string][]string) api.Hooks {
	at := func(phase string) []string { return orEmpty(h[phase]) }
	return api.Hooks{
		BeforeInit: at(stacksapi.BeforeInit), AfterInit: at(stacksapi.AfterInit),
		BeforePlan: at(stacksapi.BeforePlan), AfterPlan: at(stacksapi.AfterPlan),
		BeforeApply: at(stacksapi.BeforeApply), AfterApply: at(stacksapi.AfterApply),
		BeforeDestroy: at(stacksapi.BeforeDestroy), AfterDestroy: at(stacksapi.AfterDestroy),
		BeforePerform: at(stacksapi.BeforePerform), AfterPerform: at(stacksapi.AfterPerform),
		AfterRun: at(stacksapi.AfterRun),
	}
}

func actions(as []accessapi.Action) []api.Action {
	out := make([]api.Action, 0, len(as))
	for _, a := range as {
		out = append(out, api.Action(a))
	}
	return out
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func sparse(s []string) *[]string {
	if s == nil {
		return nil
	}
	return &s
}

// unix is seconds as the API's int32 timestamps carry them, which hold
// every time before 2038.
func unix(s int64) int32 { return int32(s) } //nolint:gosec // as above
