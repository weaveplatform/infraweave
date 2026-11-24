package kernel

// The run variables the agent supplies after the server's metadata.
const (
	APITokenVariable    = "INFRAWEAVE_API_TOKEN" //nolint:gosec // an environment variable name, not a credential
	APIEndpointVariable = "TF_VAR_infraweave_api_endpoint"
	RunStateVariable    = "TF_VAR_infraweave_run_state"
	// OIDCTokenVariable holds a run's OIDC federation token, which a run
	// receives only when its account's tier has oidc-federation, so it is
	// no member of ComputedEnvironmentNames.
	OIDCTokenVariable = "INFRAWEAVE_OIDC_TOKEN" //nolint:gosec // an environment variable name, not a credential
)

// ComputedEnvironmentNames lists the metadata and token shown as computed
// elements in a stack's runtimeConfig, sorted by name. The four launcher
// settings are execution settings and are not part of this view.
//
// ref: decision 0008 § "infraweave's own variables"
func ComputedEnvironmentNames() []string {
	return []string{
		APITokenVariable,
		"TF_VAR_infraweave_account_name",
		APIEndpointVariable,
		"TF_VAR_infraweave_commit_branch",
		"TF_VAR_infraweave_commit_sha",
		"TF_VAR_infraweave_local_preview",
		"TF_VAR_infraweave_project_root",
		"TF_VAR_infraweave_repository",
		"TF_VAR_infraweave_run_id",
		RunStateVariable,
		"TF_VAR_infraweave_run_trigger",
		"TF_VAR_infraweave_run_type",
		"TF_VAR_infraweave_space_id",
		"TF_VAR_infraweave_stack_branch",
		"TF_VAR_infraweave_stack_id",
		"TF_VAR_infraweave_stack_labels",
		"TF_VAR_infraweave_stack_labels_list",
		"TF_VAR_infraweave_workspace_root",
	}
}
