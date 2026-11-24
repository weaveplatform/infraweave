package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/iac"
	"github.com/weaveplatform/infraweave/internal/iac/terragrunt"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/core"
	vendorsapi "github.com/weaveplatform/infraweave/internal/vendors/api"
)

// viewOf is st as the API answers it, in the space slug, with the
// actions held there and whether the caller starred it, its state and
// whether a run blocks it, versions, a Terragrunt stack's effective
// versions, and no contexts, config elements, AWS integration
// attachments, blocker, outputs or rollback, which views reads from the
// configuration, cloud, runs and state domains. Other attachment, dependency and managed-resource projections
// start empty for the owning domain to populate.
func viewOf(st core.Stack, space string, held []accessapi.Action, starred bool, versions api.EffectiveTerragruntVersionResult) (api.Stack, error) {
	vendor, err := vendorView(st, versions)
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
		TrackedBranchHead:            commitView(st.TrackedBranchHead),
		TrackedCommit:                commitView(st.TrackedCommit),
		TrackedCommitSetBy:           st.TrackedCommitSetBy,
		VcsDetached:                  st.VCSDetached,
		State:                        api.StackState(st.State),
		StateIsTerminal:              st.State.Terminal(),
		Blocked:                      st.Blocked,
		Deleting:                     st.Deleting,
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
	if st.VCSIntegration != nil {
		v.VcsIntegration = &api.VCSIntegrationRef{Id: st.VCSIntegration.String()}
	}
	if st.WorkerPool != nil {
		v.WorkerPool = &api.WorkerPoolRef{Id: st.WorkerPool.String()}
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
// is its terraformVersion when it has one, which takes precedence. A
// Terragrunt stack's effectiveVersions are versions, and its
// useStateManagement whether infraweave keeps its state, as its create
// decided.
func vendorView(st core.Stack, versions api.EffectiveTerragruntVersionResult) (*api.StackConfigVendor, error) {
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
	case iac.Terragrunt:
		s := terragrunt.Read(st.VendorConfig)
		err = out.FromStackConfigVendorTerragrunt(api.StackConfigVendorTerragrunt{
			Kind:                              api.StackConfigVendorTerragruntKindStackConfigVendorTerragrunt,
			EffectiveVersions:                 versions,
			Tool:                              api.TerragruntTool(s.ToolName()),
			TerragruntVersion:                 s.TerragruntVersion,
			TerraformVersion:                  s.TerraformVersion,
			UseRunAll:                         s.UseRunAll,
			UseSmartSanitization:              s.UseSmartSanitization,
			UseStateManagement:                st.ManagesState,
			SkipReplan:                        s.SkipReplan,
			PrefixResourceNamesWithModuleName: s.PrefixResourceNamesWithModuleName,
		})
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

// resolution is a vendor and the constraint a release of it is resolved
// for, empty for none.
type resolution struct {
	vendor      vendorsapi.Vendor
	constraints string
}

// resolver resolves releases in catalog as a run's handover does,
// remembering each resolution in seen, so a page of stacks reads the
// catalogue once for each vendor and constraint it names.
type resolver struct {
	catalog vendorsapi.Catalog
	seen    map[resolution]*string
}

// terragrunt is the effective versions of st, a Terragrunt stack: the
// Terragrunt release its terragruntVersion allows, and the release of the
// tool it drives that its terraformVersion allows, else its Terragrunt
// settings' terraformVersion, each the newest listed when no constraint
// is set, as its runs' handover resolves them.
//
// ref: decision 0008 § "The run specification and its secrets"
func (r resolver) terragrunt(ctx context.Context, st core.Stack) (api.EffectiveTerragruntVersionResult, error) {
	s := terragrunt.Read(st.VendorConfig)
	out := api.EffectiveTerragruntVersionResult{Tool: api.TerragruntTool(s.ToolName())}
	var err error
	if out.EffectiveTerragruntVersion, err = r.release(ctx, vendorsapi.Terragrunt,
		set(s.TerragruntVersion)); err != nil {
		return out, err
	}
	tool := set(st.TerraformVersion)
	if tool == nil {
		tool = set(s.TerraformVersion)
	}
	out.EffectiveTerraformVersion, err = r.release(ctx, vendorsapi.Vendor(s.Driven()), tool)
	return out, err
}

// release is the newest release of vendor the catalogue lists that
// constraints allow, or its newest when constraints are nil; nil when it
// lists none they allow, which a run's handover refuses and a read
// answers as null.
func (r resolver) release(ctx context.Context, vendor vendorsapi.Vendor, constraints *string) (*string, error) {
	key := resolution{vendor: vendor}
	if constraints != nil {
		key.constraints = *constraints
	}
	if v, ok := r.seen[key]; ok {
		return v, nil
	}
	var v *string
	if constraints == nil {
		listed, err := r.catalog.Versions(ctx, vendor)
		if err != nil {
			return nil, err //nolint:wrapcheck // the vendors domain names the read
		}
		if len(listed) > 0 {
			v = &listed[0]
		}
	} else {
		got, err := r.catalog.Effective(ctx, vendor, *constraints)
		if _, ok := errors.AsType[*kernel.Invalid](err); !ok && err != nil {
			return nil, err //nolint:wrapcheck // as above
		}
		if err == nil {
			v = &got
		}
	}
	r.seen[key] = v
	return v, nil
}

// set is p when it holds a constraint, nil when it is nil or blank, which
// sets none.
func set(p *string) *string {
	if p == nil || strings.TrimSpace(*p) == "" {
		return nil
	}
	return p
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

// commitView is c as the API answers it, nil when c is.
func commitView(c *stacksapi.Commit) *api.Commit {
	if c == nil {
		return nil
	}
	return &api.Commit{
		Hash: c.Hash, Tag: c.Tag, Url: c.URL, Message: c.Message, AuthorName: c.AuthorName,
		AuthorLogin: c.AuthorLogin, Timestamp: unix(c.Timestamp.Unix()),
	}
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
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
