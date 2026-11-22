//nolint:tagliatelle // Policy JSON names match the recorded contract; only the namespace is renamed.
package inputs

import "encoding/json"

// Commit is a typed policy contract object.
type Commit struct {
	Author               Field[string]   `json:"author,omitzero"                  contract:"required" capture:"sensitive"`
	Branch               Field[string]   `json:"branch,omitzero"                  contract:"required"`
	CreatedAt            Field[int64]    `json:"created_at,omitzero"              contract:"required"`
	ExistOnTrackedBranch Field[bool]     `json:"exist_on_tracked_branch,omitzero" contract:"nullable"`
	Hash                 Field[string]   `json:"hash,omitzero"                    contract:""`
	Message              Field[string]   `json:"message,omitzero"                 contract:"required" capture:"sensitive"`
	PullRequestID        Field[int64]    `json:"pull_request_id,omitzero"         contract:"nullable"`
	Tag                  Field[string]   `json:"tag,omitzero"                     contract:""`
	URL                  Field[string]   `json:"url,omitzero"                     contract:""`
	AuthorIdentity       Field[Identity] `json:"author_identity,omitzero"         contract:""         capture:"sensitive"`
}

// IntentReview is a typed policy contract object.
type IntentReview struct {
	Author  Field[string]  `json:"author,omitzero"  contract:""`
	Request Field[Request] `json:"request,omitzero" contract:""`
	Session Field[Session] `json:"session,omitzero" contract:""`
}

// Module is a typed policy contract object.
type Module struct {
	Administrative    Field[bool]       `json:"administrative,omitzero"     contract:"required"`
	Branch            Field[string]     `json:"branch,omitzero"             contract:"required"`
	Contexts          Field[[]string]   `json:"contexts,omitzero"           contract:""`
	ID                Field[string]     `json:"id,omitzero"                 contract:"required"`
	Labels            Field[[]string]   `json:"labels,omitzero"             contract:"nullable"`
	Name              Field[string]     `json:"name,omitzero"               contract:"required"`
	Namespace         Field[string]     `json:"namespace,omitzero"          contract:"required"`
	ProjectRoot       Field[string]     `json:"project_root,omitzero"       contract:"required"`
	Repository        Field[string]     `json:"repository,omitzero"         contract:"required"`
	Space             Field[Space]      `json:"space,omitzero"              contract:"nullable"`
	TerraformProvider Field[string]     `json:"terraform_provider,omitzero" contract:"required"`
	WorkerPool        Field[WorkerPool] `json:"worker_pool,omitzero"        contract:"nullable"`
}

// Push is a typed policy contract object.
type Push struct {
	AffectedFiles       Field[[]string]     `json:"affected_files,omitzero"        contract:"required"`
	AffectedStacksCount Field[int64]        `json:"affected_stacks_count,omitzero" contract:"required"`
	Author              Field[string]       `json:"author,omitzero"                contract:"required" capture:"sensitive"`
	Branch              Field[string]       `json:"branch,omitzero"                contract:"required"`
	Commits             Field[[]PushCommit] `json:"commits,omitzero"               contract:""`
	CreatedAt           Field[int64]        `json:"created_at,omitzero"            contract:"required"`
	Hash                Field[string]       `json:"hash,omitzero"                  contract:"required"`
	Message             Field[string]       `json:"message,omitzero"               contract:"required" capture:"sensitive"`
	Tag                 Field[string]       `json:"tag,omitzero"                   contract:""`
}

// PushCommit is a typed policy contract object.
type PushCommit struct {
	Author               Field[string]   `json:"author,omitzero"                  contract:"required" capture:"sensitive"`
	Branch               Field[string]   `json:"branch,omitzero"                  contract:"required"`
	CreatedAt            Field[int64]    `json:"created_at,omitzero"              contract:"required"`
	ExistOnTrackedBranch Field[bool]     `json:"exist_on_tracked_branch,omitzero" contract:"nullable"`
	Hash                 Field[string]   `json:"hash,omitzero"                    contract:""`
	Message              Field[string]   `json:"message,omitzero"                 contract:"required" capture:"sensitive"`
	PullRequestID        Field[int64]    `json:"pull_request_id,omitzero"         contract:"nullable"`
	Tag                  Field[string]   `json:"tag,omitzero"                     contract:""`
	URL                  Field[string]   `json:"url,omitzero"                     contract:""`
	AuthorIdentity       Field[Identity] `json:"author_identity,omitzero"         contract:""         capture:"sensitive"`
	AffectedFiles        Field[[]string] `json:"affected_files,omitzero"          contract:"required"`
}

// Request is a typed policy contract object.
type Request struct {
	RemoteIP    Field[string] `json:"remote_ip,omitzero"    contract:"" capture:"sensitive"`
	TimestampNS Field[int64]  `json:"timestamp_ns,omitzero" contract:""`
}

// RunTrigger is a typed policy contract object.
type RunTrigger struct {
	Run     Field[RunTriggerRun]   `json:"run,omitzero"     contract:""`
	Stack   Field[RunTriggerStack] `json:"stack,omitzero"   contract:"nullable"`
	Trigger Field[*RunTrigger]     `json:"trigger,omitzero" contract:"nullable"`
}

// RunTriggerRun is a typed policy contract object.
type RunTriggerRun struct {
	ID Field[string] `json:"id,omitzero" contract:""`
}

// RunTriggerStack is a typed policy contract object.
type RunTriggerStack struct {
	ID Field[string] `json:"id,omitzero" contract:""`
}

// Session is a typed policy contract object.
type Session struct {
	Admin      Field[Admin]    `json:"admin,omitzero"       contract:""`
	CreatorIP  Field[string]   `json:"creator_ip,omitzero"  contract:""                  capture:"sensitive"`
	IDPSubject Field[string]   `json:"idp_subject,omitzero" contract:""                  capture:"sensitive"`
	Login      Field[string]   `json:"login,omitzero"       contract:""                  capture:"sensitive"`
	Machine    Field[bool]     `json:"machine,omitzero"     contract:"required"`
	Name       Field[string]   `json:"name,omitzero"        contract:""                  capture:"sensitive"`
	StackID    Field[string]   `json:"stack_id,omitzero"    contract:"nullable"`
	Teams      Field[[]string] `json:"teams,omitzero"       contract:"required,nullable"`
	Identity   Field[Identity] `json:"identity,omitzero"    contract:""                  capture:"sensitive"`
}

// Slack is a typed policy contract object.
type Slack struct {
	Channel Field[SlackChannel] `json:"channel,omitzero" contract:"required"`
	Command Field[string]       `json:"command,omitzero" contract:"required" capture:"sensitive"`
	Team    Field[SlackTeam]    `json:"team,omitzero"    contract:"required"`
	User    Field[SlackUser]    `json:"user,omitzero"    contract:"required"`
}

// SlackChannel is a typed policy contract object.
type SlackChannel struct {
	ID   Field[string] `json:"id,omitzero"   contract:""`
	Name Field[string] `json:"name,omitzero" contract:""`
}

// SlackTeam is a typed policy contract object.
type SlackTeam struct {
	ID   Field[string] `json:"id,omitzero"   contract:""`
	Name Field[string] `json:"name,omitzero" contract:""`
}

// SlackUser is a typed policy contract object.
type SlackUser struct {
	Deleted           Field[bool]                `json:"deleted,omitzero"             contract:""`
	DisplayName       Field[string]              `json:"display_name,omitzero"        contract:""`
	Enterprise        Field[SlackUserEnterprise] `json:"enterprise,omitzero"          contract:""`
	Has2fa            Field[bool]                `json:"has_2fa,omitzero"             contract:""`
	ID                Field[string]              `json:"id,omitzero"                  contract:""`
	IsAdmin           Field[bool]                `json:"is_admin,omitzero"            contract:""`
	IsOwner           Field[bool]                `json:"is_owner,omitzero"            contract:""`
	IsPrimaryOwner    Field[bool]                `json:"is_primary_owner,omitzero"    contract:""`
	IsRestricted      Field[bool]                `json:"is_restricted,omitzero"       contract:""`
	IsStranger        Field[bool]                `json:"is_stranger,omitzero"         contract:""`
	IsUltraRestricted Field[bool]                `json:"is_ultra_restricted,omitzero" contract:""`
	RealName          Field[string]              `json:"real_name,omitzero"           contract:""`
	Tz                Field[string]              `json:"tz,omitzero"                  contract:""`
}

// SlackUserEnterprise is a typed policy contract object.
type SlackUserEnterprise struct {
	EnterpriseID   Field[string]   `json:"enterprise_id,omitzero"   contract:""`
	EnterpriseName Field[string]   `json:"enterprise_name,omitzero" contract:""`
	ID             Field[string]   `json:"id,omitzero"              contract:""`
	IsAdmin        Field[bool]     `json:"is_admin,omitzero"        contract:""`
	IsOwner        Field[bool]     `json:"is_owner,omitzero"        contract:""`
	Teams          Field[[]string] `json:"teams,omitzero"           contract:"nullable"`
}

// Space is a typed policy contract object.
type Space struct {
	ID     Field[string]   `json:"id,omitzero"     contract:"required"`
	Labels Field[[]string] `json:"labels,omitzero" contract:"required,nullable"`
	Name   Field[string]   `json:"name,omitzero"   contract:"required"`
}

// Stack is a typed policy contract object.
type Stack struct {
	AdditionalProjectGlobs Field[[]string]   `json:"additional_project_globs,omitzero" contract:""`
	Administrative         Field[bool]       `json:"administrative,omitzero"           contract:"required"`
	Autodeploy             Field[bool]       `json:"autodeploy,omitzero"               contract:"required"`
	Branch                 Field[string]     `json:"branch,omitzero"                   contract:"required"`
	Contexts               Field[[]string]   `json:"contexts,omitzero"                 contract:""`
	ID                     Field[string]     `json:"id,omitzero"                       contract:"required"`
	Labels                 Field[[]string]   `json:"labels,omitzero"                   contract:"nullable"`
	LockedBy               Field[string]     `json:"locked_by,omitzero"                contract:"nullable"`
	Name                   Field[string]     `json:"name,omitzero"                     contract:"required"`
	Namespace              Field[string]     `json:"namespace,omitzero"                contract:"required"`
	ProjectRoot            Field[string]     `json:"project_root,omitzero"             contract:"required"`
	Repository             Field[string]     `json:"repository,omitzero"               contract:"required"`
	Roles                  Field[[]Role]     `json:"roles,omitzero"                    contract:""`
	Space                  Field[Space]      `json:"space,omitzero"                    contract:"nullable"`
	State                  Field[string]     `json:"state,omitzero"                    contract:"required"`
	TerraformVersion       Field[string]     `json:"terraform_version,omitzero"        contract:"nullable"`
	TerraformWorkflowTool  Field[string]     `json:"terraform_workflow_tool,omitzero"  contract:"nullable"`
	TrackedCommit          Field[Commit]     `json:"tracked_commit,omitzero"           contract:"nullable"`
	WorkerPool             Field[WorkerPool] `json:"worker_pool,omitzero"              contract:"nullable"`
}

// Version is a typed policy contract object.
type Version struct {
	Commit    Field[Commit]           `json:"commit,omitzero"     contract:"required"`
	CreatedAt Field[int64]            `json:"created_at,omitzero" contract:"required"`
	ID        Field[string]           `json:"id,omitzero"         contract:"required"`
	Latest    Field[bool]             `json:"latest,omitzero"     contract:"required"`
	Number    Field[string]           `json:"number,omitzero"     contract:"required"`
	State     Field[string]           `json:"state,omitzero"      contract:"required"`
	TestRuns  Field[[]VersionTestRun] `json:"test_runs,omitzero"  contract:"required"`
}

// VersionTestRun is a typed policy contract object.
type VersionTestRun struct {
	CreatedAt Field[int64]  `json:"created_at,omitzero" contract:""`
	ID        Field[string] `json:"id,omitzero"         contract:""`
	State     Field[string] `json:"state,omitzero"      contract:""`
	Title     Field[string] `json:"title,omitzero"      contract:""`
	UpdatedAt Field[int64]  `json:"updated_at,omitzero" contract:""`
}

// WorkerPool is a typed policy contract object.
type WorkerPool struct {
	ID     Field[string]   `json:"id,omitzero"     contract:""`
	Labels Field[[]string] `json:"labels,omitzero" contract:""`
	Name   Field[string]   `json:"name,omitzero"   contract:""`
	Public Field[bool]     `json:"public,omitzero" contract:"required"`
}

// Role is a typed policy contract object.
type Role struct {
	ID   Field[string] `json:"id,omitzero"   contract:"required"`
	Name Field[string] `json:"name,omitzero" contract:"required"`
	Slug Field[string] `json:"slug,omitzero" contract:"required"`
	ULID Field[string] `json:"ulid,omitzero" contract:"required"`
}

// WebhookEndpoint is a typed policy contract object.
type WebhookEndpoint struct {
	ID     Field[string]   `json:"id,omitzero"     contract:"required"`
	Labels Field[[]string] `json:"labels,omitzero" contract:"required"`
}

// VCSIntegration is a typed policy contract object.
type VCSIntegration struct {
	ID          Field[string]   `json:"id,omitzero"          contract:""`
	Description Field[string]   `json:"description,omitzero" contract:""`
	IsDefault   Field[bool]     `json:"is_default,omitzero"  contract:""`
	Labels      Field[[]string] `json:"labels,omitzero"      contract:""`
	Name        Field[string]   `json:"name,omitzero"        contract:""`
	Space       Field[Space]    `json:"space,omitzero"       contract:"nullable"`
	Provider    Field[string]   `json:"provider,omitzero"    contract:""`
	UpdatedAt   Field[int64]    `json:"updated_at,omitzero"  contract:""`
	CreatedAt   Field[int64]    `json:"created_at,omitzero"  contract:""`
}

// PullRequest is a typed policy contract object.
type PullRequest struct {
	ActionInitiator     Field[string]   `json:"action_initiator,omitzero"      contract:""`
	Action              Field[string]   `json:"action,omitzero"                contract:"required"`
	AffectedStacksCount Field[int64]    `json:"affected_stacks_count,omitzero" contract:"required"`
	Author              Field[string]   `json:"author,omitzero"                contract:"required"`
	Base                Field[Commit]   `json:"base,omitzero"                  contract:"required"`
	Closed              Field[bool]     `json:"closed,omitzero"                contract:"required"`
	Diff                Field[[]string] `json:"diff,omitzero"                  contract:"required"`
	Draft               Field[bool]     `json:"draft,omitzero"                 contract:"required"`
	Head                Field[Commit]   `json:"head,omitzero"                  contract:"required"`
	HeadOwner           Field[string]   `json:"head_owner,omitzero"            contract:""`
	ID                  Field[int64]    `json:"id,omitzero"                    contract:"required"`
	Labels              Field[[]string] `json:"labels,omitzero"                contract:"required"`
	Title               Field[string]   `json:"title,omitzero"                 contract:"required"`
	Approved            Field[bool]     `json:"approved,omitzero"              contract:""`
	Mergeable           Field[bool]     `json:"mergeable,omitzero"             contract:""`
	Undiverged          Field[bool]     `json:"undiverged,omitzero"            contract:""`
	Comment             Field[string]   `json:"comment,omitzero"               contract:""`
}

// RuntimeConfig is a typed policy contract object.
type RuntimeConfig struct {
	GitSparseCheckoutPaths Field[[]string]          `json:"git_sparse_checkout_paths,omitzero" contract:"nullable"`
	AfterApply             Field[[]string]          `json:"after_apply,omitzero"               contract:"nullable"          capture:"sensitive"`
	AfterDestroy           Field[[]string]          `json:"after_destroy,omitzero"             contract:"nullable"          capture:"sensitive"`
	AfterInit              Field[[]string]          `json:"after_init,omitzero"                contract:"nullable"          capture:"sensitive"`
	AfterPerform           Field[[]string]          `json:"after_perform,omitzero"             contract:"nullable"          capture:"sensitive"`
	AfterPlan              Field[[]string]          `json:"after_plan,omitzero"                contract:"nullable"          capture:"sensitive"`
	AfterRun               Field[[]string]          `json:"after_run,omitzero"                 contract:"nullable"          capture:"sensitive"`
	BeforeApply            Field[[]string]          `json:"before_apply,omitzero"              contract:"nullable"          capture:"sensitive"`
	BeforeDestroy          Field[[]string]          `json:"before_destroy,omitzero"            contract:"nullable"          capture:"sensitive"`
	BeforeInit             Field[[]string]          `json:"before_init,omitzero"               contract:"nullable"          capture:"sensitive"`
	BeforePerform          Field[[]string]          `json:"before_perform,omitzero"            contract:"nullable"          capture:"sensitive"`
	BeforePlan             Field[[]string]          `json:"before_plan,omitzero"               contract:"nullable"          capture:"sensitive"`
	Environment            Field[map[string]string] `json:"environment,omitzero"               contract:"required"          capture:"sensitive"`
	ProjectRoot            Field[string]            `json:"project_root,omitzero"              contract:"required"`
	RunnerImage            Field[string]            `json:"runner_image,omitzero"              contract:"required"`
	TerraformVersion       Field[string]            `json:"terraform_version,omitzero"         contract:"required,nullable"`
}

// Change is a typed policy contract object.
type Change struct {
	Phase  Field[string] `json:"phase,omitzero"  contract:"required"`
	Action Field[string] `json:"action,omitzero" contract:"required,enum=added,changed,deleted,no-op,read,destroy-Before-create-replaced,create-Before-destroy-replaced,import,forget,create-after-forgotten,ok,skipped,rescued,ignored,unreachable,failed,open,renew,close"`
	Moved  Field[bool]   `json:"moved,omitzero"  contract:""`
	Entity Field[Entity] `json:"entity,omitzero" contract:"required"`
}

// Review is a typed policy contract object.
type Review struct {
	Author       Field[string]   `json:"author,omitzero"        contract:"required"`
	AuthorRoles  Field[[]Role]   `json:"author_roles,omitzero"  contract:"nullable"`
	Identity     Field[Identity] `json:"identity,omitzero"      contract:""`
	Note         Field[string]   `json:"note,omitzero"          contract:""`
	Request      Field[Request]  `json:"request,omitzero"       contract:""`
	Session      Field[Session]  `json:"session,omitzero"       contract:""`
	State        Field[string]   `json:"state,omitzero"         contract:"required"`
	StateVersion Field[int64]    `json:"state_version,omitzero" contract:"required"`
}

// Receipt is a typed policy contract object.
type Receipt struct {
	Type    Field[string]   `json:"type,omitzero"    contract:"required"`
	Name    Field[string]   `json:"name,omitzero"    contract:"required"`
	Outcome Field[string]   `json:"outcome,omitzero" contract:"required"`
	Flags   Field[[]string] `json:"flags,omitzero"   contract:"required"`
}

// PlanOutcome is a typed policy contract object.
type PlanOutcome struct {
	Deny Field[[]string] `json:"deny,omitzero" contract:""`
	Info Field[[]string] `json:"info,omitzero" contract:""`
	Warn Field[[]string] `json:"warn,omitzero" contract:""`
}

// ApprovalRun is a typed policy contract object.
type ApprovalRun struct {
	BasedOnLocalWorkspace          Field[bool]          `json:"based_on_local_workspace,omitzero"          contract:""`
	Branch                         Field[string]        `json:"branch,omitzero"                            contract:""`
	Changes                        Field[[]Change]      `json:"changes,omitzero"                           contract:""`
	Command                        Field[string]        `json:"command,omitzero"                           contract:"nullable"                                                  capture:"sensitive"`
	Commit                         Field[Commit]        `json:"commit,omitzero"                            contract:""`
	CreatedAt                      Field[int64]         `json:"created_at,omitzero"                        contract:"required"`
	CreatorSession                 Field[Session]       `json:"creator_session,omitzero"                   contract:""`
	DriftDetection                 Field[bool]          `json:"drift_detection,omitzero"                   contract:""`
	Flags                          Field[[]string]      `json:"flags,omitzero"                             contract:""`
	ForceApplyMode                 Field[string]        `json:"force_apply_mode,omitzero"                  contract:"enum=DISABLED,SINGLE,CASCADE"`
	ID                             Field[string]        `json:"id,omitzero"                                contract:"required"`
	RepositoryRuntimeConfigChanged Field[bool]          `json:"repository_runtime_config_changed,omitzero" contract:""`
	RuntimeConfig                  Field[RuntimeConfig] `json:"runtime_config,omitzero"                    contract:"nullable"`
	State                          Field[string]        `json:"state,omitzero"                             contract:"required"`
	Trigger                        Field[*RunTrigger]   `json:"trigger,omitzero"                           contract:"nullable"`
	TriggeredBy                    Field[string]        `json:"triggered_by,omitzero"                      contract:"required,nullable"`
	Type                           Field[string]        `json:"type,omitzero"                              contract:"required,enum=PROPOSED,TRACKED,TASK,TESTING,DESTROY,PARSE"`
	UpdatedAt                      Field[int64]         `json:"updated_at,omitzero"                        contract:"required"`
	UserProvidedMetadata           Field[[]string]      `json:"user_provided_metadata,omitzero"            contract:""                                                          capture:"sensitive"`
}

// PlanRun is a typed policy contract object.
type PlanRun struct {
	BasedOnLocalWorkspace Field[bool]          `json:"based_on_local_workspace,omitzero" contract:""`
	Branch                Field[string]        `json:"branch,omitzero"                   contract:""`
	Changes               Field[[]Change]      `json:"changes,omitzero"                  contract:""`
	Command               Field[string]        `json:"command,omitzero"                  contract:"nullable"                                                  capture:"sensitive"`
	Commit                Field[Commit]        `json:"commit,omitzero"                   contract:""`
	CreatedAt             Field[int64]         `json:"created_at,omitzero"               contract:"required"`
	CreatorSession        Field[Session]       `json:"creator_session,omitzero"          contract:""`
	DriftDetection        Field[bool]          `json:"drift_detection,omitzero"          contract:""`
	Flags                 Field[[]string]      `json:"flags,omitzero"                    contract:""`
	ForceApplyMode        Field[string]        `json:"force_apply_mode,omitzero"         contract:"enum=DISABLED,SINGLE,CASCADE"`
	ID                    Field[string]        `json:"id,omitzero"                       contract:"required"`
	Promoted              Field[bool]          `json:"promoted,omitzero"                 contract:""`
	RuntimeConfig         Field[RuntimeConfig] `json:"runtime_config,omitzero"           contract:"nullable"`
	State                 Field[string]        `json:"state,omitzero"                    contract:"required"`
	Trigger               Field[*RunTrigger]   `json:"trigger,omitzero"                  contract:"nullable"`
	TriggeredBy           Field[string]        `json:"triggered_by,omitzero"             contract:"required,nullable"`
	Type                  Field[string]        `json:"type,omitzero"                     contract:"required,enum=PROPOSED,TRACKED,TASK,TESTING,DESTROY,PARSE"`
	UpdatedAt             Field[int64]         `json:"updated_at,omitzero"               contract:"required"`
	UserProvidedMetadata  Field[[]string]      `json:"user_provided_metadata,omitzero"   contract:""                                                          capture:"sensitive"`
}

// NotificationRun is a typed policy contract object.
type NotificationRun struct {
	BasedOnLocalWorkspace Field[bool]              `json:"based_on_local_workspace,omitzero" contract:""`
	Branch                Field[string]            `json:"branch,omitzero"                   contract:""`
	Changes               Field[[]Change]          `json:"changes,omitzero"                  contract:""`
	Command               Field[string]            `json:"command,omitzero"                  contract:"nullable"                                                  capture:"sensitive"`
	Commit                Field[Commit]            `json:"commit,omitzero"                   contract:""`
	CreatedAt             Field[int64]             `json:"created_at,omitzero"               contract:"required"`
	CreatorSession        Field[Session]           `json:"creator_session,omitzero"          contract:""`
	DriftDetection        Field[bool]              `json:"drift_detection,omitzero"          contract:""`
	Flags                 Field[[]string]          `json:"flags,omitzero"                    contract:""`
	ForceApplyMode        Field[string]            `json:"force_apply_mode,omitzero"         contract:"enum=DISABLED,SINGLE,CASCADE"`
	ID                    Field[string]            `json:"id,omitzero"                       contract:"required"`
	Promoted              Field[bool]              `json:"promoted,omitzero"                 contract:""`
	RuntimeConfig         Field[RuntimeConfig]     `json:"runtime_config,omitzero"           contract:"nullable"`
	State                 Field[string]            `json:"state,omitzero"                    contract:"required"`
	StatesHistory         Field[[]StateTransition] `json:"states_history,omitzero"           contract:""`
	Trigger               Field[*RunTrigger]       `json:"trigger,omitzero"                  contract:"nullable"`
	TriggeredBy           Field[string]            `json:"triggered_by,omitzero"             contract:"required,nullable"`
	Type                  Field[string]            `json:"type,omitzero"                     contract:"required,enum=PROPOSED,TRACKED,TASK,TESTING,DESTROY,PARSE"`
	UpdatedAt             Field[int64]             `json:"updated_at,omitzero"               contract:"required"`
	UserProvidedMetadata  Field[[]string]          `json:"user_provided_metadata,omitzero"   contract:""                                                          capture:"sensitive"`
}

// TriggerRun is a typed policy contract object.
type TriggerRun struct {
	BasedOnLocalWorkspace Field[bool]              `json:"based_on_local_workspace,omitzero" contract:""`
	Branch                Field[string]            `json:"branch,omitzero"                   contract:""`
	Changes               Field[[]Change]          `json:"changes,omitzero"                  contract:""`
	Command               Field[string]            `json:"command,omitzero"                  contract:"nullable"                                                         capture:"sensitive"`
	Commit                Field[Commit]            `json:"commit,omitzero"                   contract:""`
	CreatedAt             Field[int64]             `json:"created_at,omitzero"               contract:"required"`
	CreatorSession        Field[Session]           `json:"creator_session,omitzero"          contract:""`
	DriftDetection        Field[bool]              `json:"drift_detection,omitzero"          contract:""`
	Flags                 Field[[]string]          `json:"flags,omitzero"                    contract:""`
	ForceApplyMode        Field[string]            `json:"force_apply_mode,omitzero"         contract:"enum=DISABLED,SINGLE,CASCADE"`
	ID                    Field[string]            `json:"id,omitzero"                       contract:"required"`
	Priority              Field[string]            `json:"priority,omitzero"                 contract:"required"`
	Promoted              Field[bool]              `json:"promoted,omitzero"                 contract:""`
	RuntimeConfig         Field[RuntimeConfig]     `json:"runtime_config,omitzero"           contract:"nullable"`
	State                 Field[string]            `json:"state,omitzero"                    contract:"required,enum=FINISHED,FAILED,CANCELED,DISCARDED,STOPPED,SKIPPED"`
	StatesHistory         Field[[]StateTransition] `json:"states_history,omitzero"           contract:""`
	Trigger               Field[*RunTrigger]       `json:"trigger,omitzero"                  contract:"nullable"`
	TriggeredBy           Field[string]            `json:"triggered_by,omitzero"             contract:"required,nullable"`
	Type                  Field[string]            `json:"type,omitzero"                     contract:"required,enum=PROPOSED,TRACKED,TASK,TESTING,DESTROY,PARSE"`
	UpdatedAt             Field[int64]             `json:"updated_at,omitzero"               contract:"required"`
	UserProvidedMetadata  Field[[]string]          `json:"user_provided_metadata,omitzero"   contract:""                                                                 capture:"sensitive"`
}

// InProgressRun is a typed policy contract object.
type InProgressRun struct {
	BasedOnLocalWorkspace Field[bool]        `json:"based_on_local_workspace,omitzero" contract:""`
	Branch                Field[string]      `json:"branch,omitzero"                   contract:""`
	Command               Field[string]      `json:"command,omitzero"                  contract:"nullable"                                                  capture:"sensitive"`
	Commit                Field[Commit]      `json:"commit,omitzero"                   contract:""`
	CreatedAt             Field[int64]       `json:"created_at,omitzero"               contract:"required"`
	DriftDetection        Field[bool]        `json:"drift_detection,omitzero"          contract:""`
	ForceApplyMode        Field[string]      `json:"force_apply_mode,omitzero"         contract:"enum=DISABLED,SINGLE,CASCADE"`
	ID                    Field[string]      `json:"id,omitzero"                       contract:"required"`
	State                 Field[string]      `json:"state,omitzero"                    contract:"required"`
	Trigger               Field[*RunTrigger] `json:"trigger,omitzero"                  contract:"nullable"`
	TriggeredBy           Field[string]      `json:"triggered_by,omitzero"             contract:"required,nullable"`
	Type                  Field[string]      `json:"type,omitzero"                     contract:"required,enum=PROPOSED,TRACKED,TASK,TESTING,DESTROY,PARSE"`
	UpdatedAt             Field[int64]       `json:"updated_at,omitzero"               contract:"required"`
	UserProvidedMetadata  Field[[]string]    `json:"user_provided_metadata,omitzero"   contract:""                                                          capture:"sensitive"`
}

// Entity is a typed policy contract object.
type Entity struct {
	Address      Field[string]     `json:"address,omitzero"       contract:""`
	Data         Field[EntityData] `json:"data,omitzero"          contract:""`
	EntityType   Field[string]     `json:"entity_type,omitzero"   contract:""`
	EntityVendor Field[string]     `json:"entity_vendor,omitzero" contract:""`
	HostName     Field[string]     `json:"host_name,omitzero"     contract:""`
	Name         Field[string]     `json:"name,omitzero"          contract:""`
	PlaybookName Field[string]     `json:"playbook_name,omitzero" contract:""`
	RoleName     Field[string]     `json:"role_name,omitzero"     contract:""`
	TaskAction   Field[string]     `json:"task_action,omitzero"   contract:""`
	TaskName     Field[string]     `json:"task_name,omitzero"     contract:""`
	Type         Field[string]     `json:"type,omitzero"          contract:""`
}

// EntityData is a typed policy contract object.
type EntityData struct {
	Address            Field[string]          `json:"address,omitzero"              contract:""`
	ID                 Field[string]          `json:"id,omitzero"                   contract:""`
	LogicalResourceID  Field[string]          `json:"logical_resource_id,omitzero"  contract:""`
	Mode               Field[string]          `json:"mode,omitzero"                 contract:""`
	Module             Field[string]          `json:"module,omitzero"               contract:""`
	Outputs            Field[Object]          `json:"outputs,omitzero"              contract:""         capture:"sensitive"`
	OutputsHash        Field[int64]           `json:"outputs_hash,omitzero"         contract:""`
	Parent             Field[string]          `json:"parent,omitzero"               contract:""`
	PhysicalResourceID Field[string]          `json:"physical_resource_id,omitzero" contract:""`
	Provider           Field[string]          `json:"provider,omitzero"             contract:""`
	Sensitive          Field[bool]            `json:"sensitive,omitzero"            contract:""`
	Tainted            Field[bool]            `json:"tainted,omitzero"              contract:""`
	Template           Field[json.RawMessage] `json:"template,omitzero"             contract:"nullable" capture:"sensitive"`
	Urn                Field[string]          `json:"urn,omitzero"                  contract:""`
	Value              Field[string]          `json:"value,omitzero"                contract:""         capture:"sensitive"`
	ValueHash          Field[int64]           `json:"value_hash,omitzero"           contract:""`
	Values             Field[Object]          `json:"values,omitzero"               contract:""         capture:"sensitive"`
	ValuesHash         Field[int64]           `json:"values_hash,omitzero"          contract:""`
}

// Identity is a typed policy contract object.
type Identity struct {
	Federations Field[map[string]Federation] `json:"federations,omitzero" contract:""`
	ULID        Field[string]                `json:"ulid,omitzero"        contract:""`
}

// Federation is a typed policy contract object.
type Federation struct {
	ID       Field[string] `json:"id,omitzero"       contract:""`
	Username Field[string] `json:"username,omitzero" contract:""`
}

// ResourceChange is a typed policy contract object.
type ResourceChange struct {
	IndexUnknown    Field[bool]          `json:"index_unknown,omitzero"    contract:""`
	ActionReason    Field[string]        `json:"action_reason,omitzero"    contract:""`
	PreviousAddress Field[string]        `json:"previous_address,omitzero" contract:""`
	Index           Field[PathStep]      `json:"index,omitzero"            contract:""`
	Deposed         Field[string]        `json:"deposed,omitzero"          contract:""`
	Address         Field[string]        `json:"address,omitzero"          contract:""`
	Change          Field[ResourceDelta] `json:"change,omitzero"           contract:""`
	Mode            Field[string]        `json:"mode,omitzero"             contract:""`
	ModuleAddress   Field[string]        `json:"module_address,omitzero"   contract:""`
	Name            Field[string]        `json:"name,omitzero"             contract:""`
	ProviderName    Field[string]        `json:"provider_name,omitzero"    contract:""`
	Type            Field[string]        `json:"type,omitzero"             contract:""`
}

// ResourceDelta is a typed policy contract object.
type ResourceDelta struct {
	ReplacePaths    Field[[][]PathStep]             `json:"replace_paths,omitzero"    contract:""`
	Importing       Field[Importing]                `json:"importing,omitzero"        contract:"nullable"`
	GeneratedConfig Field[string]                   `json:"generated_config,omitzero" contract:""         capture:"sensitive"`
	BeforeIdentity  Field[Object]                   `json:"before_identity,omitzero"  contract:"nullable" capture:"sensitive"`
	AfterIdentity   Field[Object]                   `json:"after_identity,omitzero"   contract:"nullable" capture:"sensitive"`
	Actions         Field[[]string]                 `json:"actions,omitzero"          contract:""`
	After           Field[Object]                   `json:"after,omitzero"            contract:"nullable" capture:"sensitive"`
	AfterUnknown    Field[map[string]SensitiveMask] `json:"after_unknown,omitzero"    contract:""`
	Before          Field[Object]                   `json:"before,omitzero"           contract:"nullable" capture:"sensitive"`
	BeforeSensitive Field[SensitiveMask]            `json:"before_sensitive,omitzero" contract:""`
	AfterSensitive  Field[SensitiveMask]            `json:"after_sensitive,omitzero"  contract:""`
}

// Importing retains tool-written import metadata. Identity keys belong to the provider.
type Importing struct {
	ID       Field[string] `json:"id,omitzero"       contract:""         capture:"sensitive"`
	Unknown  Field[bool]   `json:"unknown,omitzero"  contract:""`
	Identity Field[Object] `json:"identity,omitzero" contract:"nullable" capture:"sensitive"`
}

// StateTransition is a typed policy contract object.
type StateTransition struct {
	NewState Field[string] `json:"new_state,omitzero" contract:""`
	Note     Field[string] `json:"note,omitzero"      contract:"" capture:"sensitive"`
	Username Field[string] `json:"username,omitzero"  contract:""`
}

// AccessInput is a typed policy contract object.
type AccessInput struct {
	Request Field[Request] `json:"request,omitzero" contract:"required"`
	Session Field[Session] `json:"session,omitzero" contract:""`
	Slack   Field[Slack]   `json:"slack,omitzero"   contract:""`
	Stack   Field[Stack]   `json:"stack,omitzero"   contract:""`
	Module  Field[Module]  `json:"module,omitzero"  contract:""`
}

// AccessDecision is a typed policy contract object.
type AccessDecision struct {
	Deny      Field[bool]     `json:"deny,omitzero"       contract:""`
	DenyWrite Field[bool]     `json:"deny_write,omitzero" contract:""`
	Flag      Field[[]string] `json:"flag,omitzero"       contract:""`
	Read      Field[bool]     `json:"read,omitzero"       contract:""`
	ResetFlag Field[[]string] `json:"reset_flag,omitzero" contract:""`
	Sample    Field[bool]     `json:"sample,omitzero"     contract:""`
	Write     Field[bool]     `json:"write,omitzero"      contract:""`
}

// ApprovalInput is a typed policy contract object.
type ApprovalInput struct {
	Reviews Field[Reviews]     `json:"reviews,omitzero" contract:""`
	Run     Field[ApprovalRun] `json:"run,omitzero"     contract:"required"`
	Stack   Field[Stack]       `json:"stack,omitzero"   contract:"required"`
}

// Reviews is a typed policy contract object.
type Reviews struct {
	Current Field[ReviewGroup] `json:"current,omitzero" contract:"required"`
	Older   Field[ReviewGroup] `json:"older,omitzero"   contract:"required"`
}

// ReviewGroup is a typed policy contract object.
type ReviewGroup struct {
	Approvals  Field[[]Review] `json:"approvals,omitzero"  contract:"required"`
	Rejections Field[[]Review] `json:"rejections,omitzero" contract:"required"`
}

// ApprovalDecision is a typed policy contract object.
type ApprovalDecision struct {
	Approve         Field[bool]     `json:"approve,omitzero"           contract:""`
	ApproveWithNote Field[[]string] `json:"approve_with_note,omitzero" contract:""`
	Flag            Field[[]string] `json:"flag,omitzero"              contract:""`
	Reject          Field[bool]     `json:"reject,omitzero"            contract:""`
	RejectWithNote  Field[[]string] `json:"reject_with_note,omitzero"  contract:""`
	ResetFlag       Field[[]string] `json:"reset_flag,omitzero"        contract:""`
	Sample          Field[bool]     `json:"sample,omitzero"            contract:""`
}

// PushInput is a typed policy contract object.
type PushInput struct {
	InProgress     Field[InProgress]     `json:"in_progress,omitzero"     contract:""`
	PullRequest    Field[PullRequest]    `json:"pull_request,omitzero"    contract:"nullable"`
	Push           Field[Push]           `json:"push,omitzero"            contract:"nullable"`
	VCSIntegration Field[VCSIntegration] `json:"vcs_integration,omitzero" contract:"nullable"`
	Stack          Field[Stack]          `json:"stack,omitzero"           contract:""`
	Module         Field[PushModule]     `json:"module,omitzero"          contract:""`
}

// PushModule is a typed policy contract object.
type PushModule struct {
	Administrative    Field[bool]       `json:"administrative,omitzero"     contract:"required"`
	Branch            Field[string]     `json:"branch,omitzero"             contract:"required"`
	Contexts          Field[[]string]   `json:"contexts,omitzero"           contract:""`
	ID                Field[string]     `json:"id,omitzero"                 contract:"required"`
	Labels            Field[[]string]   `json:"labels,omitzero"             contract:"nullable"`
	Name              Field[string]     `json:"name,omitzero"               contract:"required"`
	Namespace         Field[string]     `json:"namespace,omitzero"          contract:"required"`
	ProjectRoot       Field[string]     `json:"project_root,omitzero"       contract:"required"`
	Repository        Field[string]     `json:"repository,omitzero"         contract:"required"`
	Space             Field[Space]      `json:"space,omitzero"              contract:"nullable"`
	TerraformProvider Field[string]     `json:"terraform_provider,omitzero" contract:"required"`
	WorkerPool        Field[WorkerPool] `json:"worker_pool,omitzero"        contract:"nullable"`
	CurrentVersion    Field[string]     `json:"current_version,omitzero"    contract:""`
}

// PushDecision is a typed policy contract object.
type PushDecision struct {
	AllowFork          Field[bool]     `json:"allow_fork,omitzero"          contract:""`
	Cancel             Field[[]string] `json:"cancel,omitzero"              contract:""`
	ExternalDependency Field[[]string] `json:"external_dependency,omitzero" contract:""`
	Fail               Field[bool]     `json:"fail,omitzero"                contract:""`
	Flag               Field[[]string] `json:"flag,omitzero"                contract:""`
	Ignore             Field[bool]     `json:"ignore,omitzero"              contract:""`
	IgnoreTrack        Field[bool]     `json:"ignore_track,omitzero"        contract:""`
	Lock               Field[string]   `json:"lock,omitzero"                contract:""`
	Message            Field[[]string] `json:"message,omitzero"             contract:""`
	ModuleVersion      Field[string]   `json:"module_version,omitzero"      contract:""`
	Notify             Field[bool]     `json:"notify,omitzero"              contract:""`
	Notrigger          Field[bool]     `json:"notrigger,omitzero"           contract:""`
	Prioritize         Field[bool]     `json:"prioritize,omitzero"          contract:""`
	Priority           Field[string]   `json:"priority,omitzero"            contract:"enum=,high,normal,low"`
	Propose            Field[bool]     `json:"propose,omitzero"             contract:""`
	ResetFlag          Field[[]string] `json:"reset_flag,omitzero"          contract:""`
	Sample             Field[bool]     `json:"sample,omitzero"              contract:""`
	Track              Field[bool]     `json:"track,omitzero"               contract:""`
	Unlock             Field[string]   `json:"unlock,omitzero"              contract:""`
}

// IntentInput is a typed policy contract object.
type IntentInput struct {
	Resource Field[IntentResource] `json:"resource,omitzero" contract:"required"`
	Reviews  Field[IntentReviews]  `json:"reviews,omitzero"  contract:""`
}

// IntentResource is a typed policy contract object.
type IntentResource struct {
	ResourceID      Field[string] `json:"resource_id,omitzero"      contract:"required"`
	Provider        Field[string] `json:"provider,omitzero"         contract:"required"`
	ProviderVersion Field[string] `json:"provider_version,omitzero" contract:""`
	ResourceType    Field[string] `json:"resource_type,omitzero"    contract:"required"`
	Operation       Field[string] `json:"operation,omitzero"        contract:"required"`
	CurrentState    Field[Object] `json:"current_state,omitzero"    contract:"nullable"`
	ProposedState   Field[Object] `json:"proposed_state,omitzero"   contract:"nullable"`
}

// IntentReviews is a typed policy contract object.
type IntentReviews struct {
	Allow Field[[]IntentReview] `json:"allow,omitzero" contract:""`
	Deny  Field[[]IntentReview] `json:"deny,omitzero"  contract:""`
}

// IntentDecision is a typed policy contract object.
type IntentDecision struct {
	Allow     Field[[]string] `json:"allow,omitzero"      contract:""`
	Deny      Field[[]string] `json:"deny,omitzero"       contract:""`
	Flag      Field[[]string] `json:"flag,omitzero"       contract:""`
	ResetFlag Field[[]string] `json:"reset_flag,omitzero" contract:""`
	Sample    Field[bool]     `json:"sample,omitzero"     contract:""`
}

// LoginInput is a typed policy contract object.
type LoginInput struct {
	Request Field[Request]      `json:"request,omitzero" contract:"required"`
	Session Field[LoginSession] `json:"session,omitzero" contract:""`
	Slack   Field[Slack]        `json:"slack,omitzero"   contract:""`
	Spaces  Field[[]Space]      `json:"spaces,omitzero"  contract:""`
	Roles   Field[[]Role]       `json:"roles,omitzero"   contract:""`
}

// LoginSession is a typed policy contract object.
type LoginSession struct {
	Admin      Field[Admin]    `json:"admin,omitzero"       contract:""`
	CreatorIP  Field[string]   `json:"creator_ip,omitzero"  contract:""                  capture:"sensitive"`
	IDPSubject Field[string]   `json:"idp_subject,omitzero" contract:""                  capture:"sensitive"`
	Login      Field[string]   `json:"login,omitzero"       contract:""                  capture:"sensitive"`
	Machine    Field[bool]     `json:"machine,omitzero"     contract:"required"`
	Name       Field[string]   `json:"name,omitzero"        contract:""                  capture:"sensitive"`
	StackID    Field[string]   `json:"stack_id,omitzero"    contract:"nullable"`
	Teams      Field[[]string] `json:"teams,omitzero"       contract:"required,nullable"`
	Identity   Field[Identity] `json:"identity,omitzero"    contract:""                  capture:"sensitive"`
	Member     Field[bool]     `json:"member,omitzero"      contract:"required"`
}

// LoginDecision is a typed policy contract object.
type LoginDecision struct {
	Admin       Field[bool]               `json:"admin,omitzero"        contract:""`
	Allow       Field[bool]               `json:"allow,omitzero"        contract:""`
	Deny        Field[bool]               `json:"deny,omitzero"         contract:""`
	DenyAdmin   Field[bool]               `json:"deny_admin,omitzero"   contract:""`
	DenyMessage Field[[]string]           `json:"deny_message,omitzero" contract:""`
	Flag        Field[[]string]           `json:"flag,omitzero"         contract:""`
	ResetFlag   Field[[]string]           `json:"reset_flag,omitzero"   contract:""`
	Roles       Field[map[string]RoleSet] `json:"roles,omitzero"        contract:""`
	Sample      Field[bool]               `json:"sample,omitzero"       contract:""`
	Team        Field[[]string]           `json:"team,omitzero"         contract:""`
	SpaceAdmin  Field[[]string]           `json:"space_admin,omitzero"  contract:""`
	SpaceWrite  Field[[]string]           `json:"space_write,omitzero"  contract:""`
	SpaceRead   Field[[]string]           `json:"space_read,omitzero"   contract:""`
}

// NotificationInput is a typed policy contract object.
type NotificationInput struct {
	InternalError    Field[InternalError]     `json:"internal_error,omitzero"    contract:""`
	RunUpdated       Field[RunUpdated]        `json:"run_updated,omitzero"       contract:""`
	ModuleVersion    Field[ModuleVersion]     `json:"module_version,omitzero"    contract:""`
	WorkerUsage      Field[WorkerUsage]       `json:"worker_usage,omitzero"      contract:""`
	Account          Field[Account]           `json:"account,omitzero"           contract:"required"`
	WebhookEndpoints Field[[]WebhookEndpoint] `json:"webhook_endpoints,omitzero" contract:"required"`
}

// InternalError is a typed policy contract object.
type InternalError struct {
	Error    Field[string] `json:"error,omitzero"    contract:"required" capture:"sensitive"`
	Message  Field[string] `json:"message,omitzero"  contract:"required" capture:"sensitive"`
	Severity Field[string] `json:"severity,omitzero" contract:"required"`
}

// RunUpdated is a typed policy contract object.
type RunUpdated struct {
	Note               Field[string]          `json:"note,omitzero"                 contract:"nullable"`
	PlanPolicyDecision Field[PlanOutcome]     `json:"plan_policy_decision,omitzero" contract:"nullable"`
	PolicyReceipts     Field[[]Receipt]       `json:"policy_receipts,omitzero"      contract:"required,nullable"`
	Run                Field[NotificationRun] `json:"run,omitzero"                  contract:"required"`
	Stack              Field[Stack]           `json:"stack,omitzero"                contract:"required"`
	State              Field[string]          `json:"state,omitzero"                contract:""`
	Timing             Field[[]StateTiming]   `json:"timing,omitzero"               contract:"required,nullable"`
	URLs               Field[RunURLs]         `json:"urls,omitzero"                 contract:"required"`
	Username           Field[string]          `json:"username,omitzero"             contract:"nullable"`
}

// StateTiming is a typed policy contract object.
type StateTiming struct {
	Duration Field[int64]  `json:"duration,omitzero" contract:"required"`
	State    Field[string] `json:"state,omitzero"    contract:"required"`
}

// RunURLs is a typed policy contract object.
type RunURLs struct {
	Run Field[string] `json:"run,omitzero" contract:""`
}

// ModuleVersion is a typed policy contract object.
type ModuleVersion struct {
	Module  Field[Module]  `json:"module,omitzero"  contract:"required"`
	Version Field[Version] `json:"version,omitzero" contract:"required"`
}

// WorkerUsage is a typed policy contract object.
type WorkerUsage struct {
	Severity                                  Field[string]  `json:"severity,omitzero"                                      contract:"required"`
	Message                                   Field[string]  `json:"message,omitzero"                                       contract:"required"`
	ContractedLimit                           Field[int64]   `json:"contracted_limit,omitzero"                              contract:"required"`
	ContractedPercentile                      Field[int64]   `json:"contracted_percentile,omitzero"                         contract:"required"`
	ConcurrencyAtContractedPercentile         Field[int64]   `json:"concurrency_at_contracted_percentile,omitzero"          contract:"required"`
	PreviousConcurrencyAtContractedPercentile Field[int64]   `json:"previous_concurrency_at_contracted_percentile,omitzero" contract:"required"`
	LimitRatio                                Field[float64] `json:"limit_ratio,omitzero"                                   contract:"required"`
	PreviousLimitRatio                        Field[float64] `json:"previous_limit_ratio,omitzero"                          contract:"required"`
	BurstBudgetMinutes                        Field[int64]   `json:"burst_budget_minutes,omitzero"                          contract:"required"`
	BurstUsedMinutes                          Field[int64]   `json:"burst_used_minutes,omitzero"                            contract:"required"`
	BurstRemainingMinutes                     Field[int64]   `json:"burst_remaining_minutes,omitzero"                       contract:"required"`
	BurstRatio                                Field[float64] `json:"burst_ratio,omitzero"                                   contract:"required"`
	PreviousBurstUsedMinutes                  Field[int64]   `json:"previous_burst_used_minutes,omitzero"                   contract:"required"`
	PreviousBurstRatio                        Field[float64] `json:"previous_burst_ratio,omitzero"                          contract:"required"`
	CycleStart                                Field[int64]   `json:"cycle_start,omitzero"                                   contract:"required"`
	CycleEnd                                  Field[int64]   `json:"cycle_end,omitzero"                                     contract:"required"`
}

// Account is a typed policy contract object.
type Account struct {
	Name Field[string] `json:"name,omitzero" contract:"required"`
}

// NotificationDecision is a typed policy contract object.
type NotificationDecision struct {
	Flag        Field[[]string]             `json:"flag,omitzero"         contract:""`
	Inbox       Field[[]InboxMessage]       `json:"inbox,omitzero"        contract:""`
	PullRequest Field[[]PullRequestMessage] `json:"pull_request,omitzero" contract:""`
	ResetFlag   Field[[]string]             `json:"reset_flag,omitzero"   contract:""`
	Sample      Field[bool]                 `json:"sample,omitzero"       contract:""`
	Slack       Field[[]SlackMessage]       `json:"slack,omitzero"        contract:""`
	Webhook     Field[[]WebhookMessage]     `json:"webhook,omitzero"      contract:""`
}

// InboxMessage is a typed policy contract object.
type InboxMessage struct {
	Body     Field[string] `json:"body,omitzero"     contract:""`
	Severity Field[string] `json:"severity,omitzero" contract:""`
	Title    Field[string] `json:"title,omitzero"    contract:""`
}

// PullRequestMessage is a typed policy contract object.
type PullRequestMessage struct {
	Body             Field[string] `json:"body,omitzero"              contract:""`
	Branch           Field[string] `json:"branch,omitzero"            contract:""`
	Commit           Field[string] `json:"commit,omitzero"            contract:""`
	DeduplicationKey Field[string] `json:"deduplication_key,omitzero" contract:""`
	ID               Field[int64]  `json:"id,omitzero"                contract:""`
	TruncateBody     Field[bool]   `json:"truncate_body,omitzero"     contract:""`
}

// SlackMessage is a typed policy contract object.
type SlackMessage struct {
	ChannelID     Field[string]   `json:"channel_id,omitzero"     contract:"required"`
	MentionGroups Field[[]string] `json:"mention_groups,omitzero" contract:""`
	MentionUsers  Field[[]string] `json:"mention_users,omitzero"  contract:""`
	Message       Field[string]   `json:"message,omitzero"        contract:""`
	ThreadKey     Field[string]   `json:"thread_key,omitzero"     contract:""`
}

// WebhookMessage is a typed policy contract object.
type WebhookMessage struct {
	EndpointID Field[string]            `json:"endpoint_id,omitzero" contract:"required"`
	Headers    Field[map[string]string] `json:"headers,omitzero"     contract:""`
	Method     Field[string]            `json:"method,omitzero"      contract:""`
	Payload    Field[json.RawMessage]   `json:"payload,omitzero"     contract:"nullable"`
}

// PlanInput is a typed policy contract object.
type PlanInput struct {
	Ansible            Field[AnsiblePlan]                  `json:"ansible,omitzero"              contract:"nullable"`
	Cloudformation     Field[CloudFormationPlan]           `json:"cloudformation,omitzero"       contract:"nullable"`
	Kubernetes         Field[KubernetesPlan]               `json:"kubernetes,omitzero"           contract:"nullable" capture:"sensitive"`
	Opentofu           Field[OpenTofuPlan]                 `json:"opentofu,omitzero"             contract:"nullable"`
	Pulumi             Field[PulumiPlan]                   `json:"pulumi,omitzero"               contract:"nullable"`
	Infraweave         Field[PlanContext]                  `json:"infraweave,omitzero"           contract:"required"`
	Terraform          Field[TerraformPlan]                `json:"terraform,omitzero"            contract:"nullable"`
	Terragrunt         Field[map[string]TerragruntProject] `json:"terragrunt,omitzero"           contract:"nullable"`
	ThirdPartyMetadata Field[ThirdPartyMetadata]           `json:"third_party_metadata,omitzero" contract:"nullable" capture:"sensitive"`
}

// AnsiblePlan is a typed policy contract object.
type AnsiblePlan struct {
	Changed   Field[map[string]int64] `json:"changed,omitzero"   contract:""`
	Dark      Field[map[string]int64] `json:"dark,omitzero"      contract:""`
	Failures  Field[map[string]int64] `json:"failures,omitzero"  contract:""`
	Ignored   Field[map[string]int64] `json:"ignored,omitzero"   contract:""`
	Ok        Field[map[string]int64] `json:"ok,omitzero"        contract:""`
	Processed Field[map[string]int64] `json:"processed,omitzero" contract:""`
	Rescued   Field[map[string]int64] `json:"rescued,omitzero"   contract:""`
	Skipped   Field[map[string]int64] `json:"skipped,omitzero"   contract:""`
	Playbook  Field[string]           `json:"playbook,omitzero"  contract:""`
}

// CloudFormationPlan is a typed policy contract object.
type CloudFormationPlan struct {
	Changes Field[[]CloudFormationChange] `json:"changes,omitzero" contract:"nullable"`
}

// CloudFormationChange is a typed policy contract object.
type CloudFormationChange struct {
	Type           Field[string]                       `json:"Type,omitzero"           contract:""`
	Resourcechange Field[CloudFormationResourceChange] `json:"ResourceChange,omitzero" contract:""`
}

// CloudFormationResourceChange is a typed policy contract object.
type CloudFormationResourceChange struct {
	Action             Field[string]                 `json:"Action,omitzero"             contract:""`
	Logicalresourceid  Field[string]                 `json:"LogicalResourceId,omitzero"  contract:""`
	Physicalresourceid Field[string]                 `json:"PhysicalResourceId,omitzero" contract:""`
	Resourcetype       Field[string]                 `json:"ResourceType,omitzero"       contract:""`
	Replacement        Field[string]                 `json:"Replacement,omitzero"        contract:""`
	Scope              Field[[]string]               `json:"Scope,omitzero"              contract:""`
	Details            Field[[]CloudFormationDetail] `json:"Details,omitzero"            contract:""`
}

// CloudFormationDetail is a typed policy contract object.
type CloudFormationDetail struct {
	Target        Field[CloudFormationTarget] `json:"Target,omitzero"        contract:""`
	Evaluation    Field[string]               `json:"Evaluation,omitzero"    contract:""`
	Changesource  Field[string]               `json:"ChangeSource,omitzero"  contract:""`
	Causingentity Field[string]               `json:"CausingEntity,omitzero" contract:""`
}

// CloudFormationTarget is a typed policy contract object.
type CloudFormationTarget struct {
	Attribute          Field[string] `json:"Attribute,omitzero"          contract:""`
	Name               Field[string] `json:"Name,omitzero"               contract:""`
	Requiresrecreation Field[string] `json:"RequiresRecreation,omitzero" contract:""`
}

// OpenTofuPlan is a typed policy contract object.
type OpenTofuPlan struct {
	Checks          Field[[]PlanCheck]      `json:"checks,omitzero"           contract:""         capture:"sensitive"`
	OpentofuVersion Field[string]           `json:"opentofu_version,omitzero" contract:""`
	ResourceChanges Field[[]ResourceChange] `json:"resource_changes,omitzero" contract:"nullable"`
}

// PulumiPlan is a typed policy contract object.
type PulumiPlan struct {
	Steps Field[[]PulumiStep] `json:"steps,omitzero" contract:"nullable"`
}

// PulumiStep is a typed policy contract object.
type PulumiStep struct {
	Op             Field[string]                `json:"op,omitzero"             contract:""`
	Provider       Field[string]                `json:"provider,omitzero"       contract:""`
	Type           Field[string]                `json:"type,omitzero"           contract:""`
	Urn            Field[string]                `json:"urn,omitzero"            contract:""`
	Old            Field[PulumiState]           `json:"old,omitzero"            contract:"nullable"`
	New            Field[PulumiState]           `json:"new,omitzero"            contract:"nullable"`
	Diffs          Field[[]string]              `json:"diffs,omitzero"          contract:""`
	Detaileddiff   Field[map[string]PulumiDiff] `json:"detailedDiff,omitzero"   contract:""`
	Replacereasons Field[[]string]              `json:"replaceReasons,omitzero" contract:""`
	Logical        Field[bool]                  `json:"logical,omitzero"        contract:""`
}

// PulumiState is a typed policy contract object.
type PulumiState struct {
	Custom   Field[bool]   `json:"custom,omitzero"   contract:""`
	Delete   Field[bool]   `json:"delete,omitzero"   contract:""`
	External Field[bool]   `json:"external,omitzero" contract:""`
	ID       Field[string] `json:"id,omitzero"       contract:""`
	Inputs   Field[Object] `json:"inputs,omitzero"   contract:"" capture:"sensitive"`
	Outputs  Field[Object] `json:"outputs,omitzero"  contract:"" capture:"sensitive"`
	Parent   Field[string] `json:"parent,omitzero"   contract:""`
	Protect  Field[bool]   `json:"protect,omitzero"  contract:""`
	Provider Field[string] `json:"provider,omitzero" contract:""`
	Type     Field[string] `json:"type,omitzero"     contract:""`
	Urn      Field[string] `json:"urn,omitzero"      contract:""`
}

// PulumiDiff is a typed policy contract object.
type PulumiDiff struct {
	Kind      Field[string] `json:"kind,omitzero"      contract:""`
	Inputdiff Field[bool]   `json:"inputDiff,omitzero" contract:""`
}

// PlanContext is a typed policy contract object.
type PlanContext struct {
	Commit      Field[Commit]      `json:"commit,omitzero"       contract:"required"`
	PreviousRun Field[PlanRun]     `json:"previous_run,omitzero" contract:"nullable"`
	Request     Field[PlanRequest] `json:"request,omitzero"      contract:"required"`
	Run         Field[PlanRun]     `json:"run,omitzero"          contract:"required"`
	Stack       Field[Stack]       `json:"stack,omitzero"        contract:""`
	Module      Field[Module]      `json:"module,omitzero"       contract:""`
}

// PlanRequest is a typed policy contract object.
type PlanRequest struct {
	TimestampNS Field[int64] `json:"timestamp_ns,omitzero" contract:""`
}

// TerraformPlan is a typed policy contract object.
type TerraformPlan struct {
	Checks           Field[[]PlanCheck]      `json:"checks,omitzero"            contract:""         capture:"sensitive"`
	ResourceChanges  Field[[]ResourceChange] `json:"resource_changes,omitzero"  contract:"nullable"`
	TerraformVersion Field[string]           `json:"terraform_version,omitzero" contract:""`
}

// TerragruntProject is a typed policy contract object.
type TerragruntProject struct {
	Checks           Field[[]PlanCheck]      `json:"checks,omitzero"            contract:""`
	ResourceChanges  Field[[]ResourceChange] `json:"resource_changes,omitzero"  contract:"nullable"`
	TerraformVersion Field[string]           `json:"terraform_version,omitzero" contract:""`
}

// ThirdPartyMetadata is a typed policy contract object.
type ThirdPartyMetadata struct {
	Custom    Field[map[string]Object] `json:"custom,omitzero"    contract:""`
	Infracost Field[Object]            `json:"infracost,omitzero" contract:""`
}

// PlanDecision is a typed policy contract object.
type PlanDecision struct {
	Deny      Field[[]string] `json:"deny,omitzero"       contract:""`
	Flag      Field[[]string] `json:"flag,omitzero"       contract:""`
	Info      Field[[]string] `json:"info,omitzero"       contract:""`
	ResetFlag Field[[]string] `json:"reset_flag,omitzero" contract:""`
	Sample    Field[bool]     `json:"sample,omitzero"     contract:""`
	Warn      Field[[]string] `json:"warn,omitzero"       contract:""`
}

// TaskInput is a typed policy contract object.
type TaskInput struct {
	Request Field[TaskInputRequest] `json:"request,omitzero" contract:"required"`
	Session Field[Session]          `json:"session,omitzero" contract:"required"`
	Stack   Field[Stack]            `json:"stack,omitzero"   contract:""`
	Module  Field[Module]           `json:"module,omitzero"  contract:""`
}

// TaskInputRequest is a typed policy contract object.
type TaskInputRequest struct {
	Command     Field[string] `json:"command,omitzero"      contract:"required" capture:"sensitive"`
	RemoteIP    Field[string] `json:"remote_ip,omitzero"    contract:""         capture:"sensitive"`
	TimestampNS Field[int64]  `json:"timestamp_ns,omitzero" contract:"required"`
}

// TaskDecision is a typed policy contract object.
type TaskDecision struct {
	Deny      Field[[]string] `json:"deny,omitzero"       contract:""`
	Flag      Field[[]string] `json:"flag,omitzero"       contract:""`
	ResetFlag Field[[]string] `json:"reset_flag,omitzero" contract:""`
	Sample    Field[bool]     `json:"sample,omitzero"     contract:""`
}

// TriggerInput is a typed policy contract object.
type TriggerInput struct {
	Run      Field[TriggerRun]     `json:"run,omitzero"      contract:""`
	Stack    Field[Stack]          `json:"stack,omitzero"    contract:""`
	Stacks   Field[[]TriggerStack] `json:"stacks,omitzero"   contract:""`
	Workflow Field[[]WorkflowRun]  `json:"workflow,omitzero" contract:""`
	Module   Field[TriggerModule]  `json:"module,omitzero"   contract:""`
}

// TriggerStack is a typed policy contract object.
type TriggerStack struct {
	AdditionalProjectGlobs Field[[]string]   `json:"additional_project_globs,omitzero" contract:""`
	Administrative         Field[bool]       `json:"administrative,omitzero"           contract:"required"`
	Autodeploy             Field[bool]       `json:"autodeploy,omitzero"               contract:"required"`
	Branch                 Field[string]     `json:"branch,omitzero"                   contract:"required"`
	Contexts               Field[[]string]   `json:"contexts,omitzero"                 contract:""`
	ID                     Field[string]     `json:"id,omitzero"                       contract:"required"`
	Labels                 Field[[]string]   `json:"labels,omitzero"                   contract:"nullable"`
	LockedBy               Field[string]     `json:"locked_by,omitzero"                contract:"nullable"`
	Name                   Field[string]     `json:"name,omitzero"                     contract:"required"`
	Namespace              Field[string]     `json:"namespace,omitzero"                contract:"required"`
	ProjectRoot            Field[string]     `json:"project_root,omitzero"             contract:"required"`
	Repository             Field[string]     `json:"repository,omitzero"               contract:"required"`
	Roles                  Field[[]Role]     `json:"roles,omitzero"                    contract:""`
	Space                  Field[Space]      `json:"space,omitzero"                    contract:"nullable"`
	State                  Field[string]     `json:"state,omitzero"                    contract:"required"`
	TerraformVersion       Field[string]     `json:"terraform_version,omitzero"        contract:"nullable"`
	TerraformWorkflowTool  Field[string]     `json:"terraform_workflow_tool,omitzero"  contract:"nullable"`
	TrackedCommit          Field[Commit]     `json:"tracked_commit,omitzero"           contract:"nullable"`
	WorkerPool             Field[WorkerPool] `json:"worker_pool,omitzero"              contract:"nullable"`
	QueuedRuns             Field[int64]      `json:"queued_runs,omitzero"              contract:"required"`
}

// WorkflowRun is a typed policy contract object.
type WorkflowRun struct {
	ID      Field[string] `json:"id,omitzero"       contract:"required"`
	StackID Field[string] `json:"stack_id,omitzero" contract:"required"`
	State   Field[string] `json:"state,omitzero"    contract:"required"`
	Type    Field[string] `json:"type,omitzero"     contract:"required,enum=PROPOSED,TRACKED,TASK,TESTING,DESTROY,PARSE"`
}

// TriggerModule is a typed policy contract object.
type TriggerModule struct {
	Administrative    Field[bool]       `json:"administrative,omitzero"     contract:"required"`
	Branch            Field[string]     `json:"branch,omitzero"             contract:"required"`
	Contexts          Field[[]string]   `json:"contexts,omitzero"           contract:""`
	ID                Field[string]     `json:"id,omitzero"                 contract:"required"`
	Labels            Field[[]string]   `json:"labels,omitzero"             contract:"nullable"`
	Name              Field[string]     `json:"name,omitzero"               contract:"required"`
	Namespace         Field[string]     `json:"namespace,omitzero"          contract:"required"`
	ProjectRoot       Field[string]     `json:"project_root,omitzero"       contract:"required"`
	Repository        Field[string]     `json:"repository,omitzero"         contract:"required"`
	Space             Field[Space]      `json:"space,omitzero"              contract:"nullable"`
	TerraformProvider Field[string]     `json:"terraform_provider,omitzero" contract:"required"`
	WorkerPool        Field[WorkerPool] `json:"worker_pool,omitzero"        contract:"nullable"`
	Version           Field[Version]    `json:"version,omitzero"            contract:"nullable"`
}

// TriggerDecision is a typed policy contract object.
type TriggerDecision struct {
	Flag      Field[[]string] `json:"flag,omitzero"       contract:""`
	Priority  Field[string]   `json:"priority,omitzero"   contract:"enum=,high,normal,low"`
	ResetFlag Field[[]string] `json:"reset_flag,omitzero" contract:""`
	Sample    Field[bool]     `json:"sample,omitzero"     contract:""`
	Trigger   Field[[]string] `json:"trigger,omitzero"    contract:""`
}

// InitializationInput is a typed policy contract object.
type InitializationInput struct {
	Commit  Field[Commit]                `json:"commit,omitzero"  contract:"required"`
	Run     Field[PlanRun]               `json:"run,omitzero"     contract:"required"`
	Stack   Field[Stack]                 `json:"stack,omitzero"   contract:"required"`
	Request Field[InitializationRequest] `json:"request,omitzero" contract:"required"`
}

// InitializationRequest is a typed policy contract object.
type InitializationRequest struct {
	TimestampNS Field[int64] `json:"timestamp_ns,omitzero" contract:"required"`
}

// InitializationDecision is a typed policy contract object.
type InitializationDecision struct {
	Deny      Field[[]string] `json:"deny,omitzero"       contract:""`
	Flag      Field[[]string] `json:"flag,omitzero"       contract:""`
	ResetFlag Field[[]string] `json:"reset_flag,omitzero" contract:""`
	Sample    Field[bool]     `json:"sample,omitzero"     contract:""`
}
