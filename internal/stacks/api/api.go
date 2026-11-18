package api

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
)

// ErrNotFound reports a stack the account lacks.
var ErrNotFound = errors.New("stacks: not found")

// Stack is a stack as other domains read it: its ULID, which every other
// domain holds, its slug, which a reslug changes, its name, and the space
// it lives in.
type Stack struct {
	ID      kernel.ULID
	Account kernel.AccountID
	Slug    string
	Name    string
	Space   kernel.ULID
}

// The hook phases, in the order a run reaches them. Each holds the
// commands a stack runs there, in order.
const (
	BeforeInit    = "before_init"
	AfterInit     = "after_init"
	BeforePlan    = "before_plan"
	AfterPlan     = "after_plan"
	BeforeApply   = "before_apply"
	AfterApply    = "after_apply"
	BeforeDestroy = "before_destroy"
	AfterDestroy  = "after_destroy"
	BeforePerform = "before_perform"
	AfterPerform  = "after_perform"
	AfterRun      = "after_run"
)

// HookPhases are the hook phases in the order a run reaches them.
var HookPhases = []string{
	BeforeInit, AfterInit, BeforePlan, AfterPlan, BeforeApply, AfterApply,
	BeforeDestroy, AfterDestroy, BeforePerform, AfterPerform, AfterRun,
}

// Lock is a stack's lock: Holder is the holder's caller key, which
// decides who unlocks without force, and By the name the API shows.
type Lock struct {
	Holder string
	By     string
	Note   string
	At     time.Time
}

// Commit is a commit of a stack's repository as the stack keeps it:
// Message is its first line, and AuthorLogin is nil when the provider
// knows no login for its author. Branch is the branch a run was created
// for, which the run's commit records; the commits a stack tracks are on
// its branch and leave it empty.
type Commit struct {
	Hash        string    `json:"hash"`
	Tag         string    `json:"tag,omitempty"`
	URL         string    `json:"url,omitempty"`
	Message     string    `json:"message"`
	AuthorName  string    `json:"authorName"`
	AuthorLogin *string   `json:"authorLogin,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
	Branch      string    `json:"branch,omitempty"`
}

// Settings are what a run of the stack reads. Vendor is the iac vendor's
// kind and VendorConfig its settings as the vendor checked them; Hooks
// maps each phase that has commands to them, in order. WorkerPool and
// VCSIntegration are nil when the stack names none, and VCSDetached marks
// a stack whose integration was deleted. TrackedBranchHead and
// TrackedCommit are nil until the vcs domain resolves the branch, and
// TrackedCommitSetBy names who set TrackedCommit. ManagesState is
// whether infraweave keeps the stack's state, which its creation decides
// once. ExternalStateAccess is the Terraform or OpenTofu vendor's
// externalStateAccessEnabled, whether callers other than the stack's own
// runs may read its managed state, and false for every other vendor.
// EnableWellKnownSecretMasking turns on the runner's well-known
// secret patterns, EnableSensitiveOutputUpload lets a run keep its
// sensitive outputs' values where its worker pool allows it too, and
// SkipVendorBinaryDownload runs the image's own tool rather than the one
// the launcher mounts. LocalPreviewEnabled lets runs be proposed on
// uploaded local workspaces, and GithubActionDeploy lets a proposed run
// be promoted to a tracked one. Deleting marks a stack whose delete waits
// for its destroy run, which no other run joins. Version counts the
// stack's writes, which SetTracked and Destroyed are not.
type Settings struct {
	Stack
	Repository                   string
	RepositoryURL                *string
	Branch                       string
	Provider                     string
	Namespace                    string
	ProjectRoot                  *string
	AdditionalProjectGlobs       []string
	GitSparseCheckoutPaths       []string
	RunnerImage                  *string
	WorkerPool                   *kernel.ULID
	VCSIntegration               *kernel.ULID
	VCSDetached                  bool
	Vendor                       string
	VendorConfig                 json.RawMessage
	TerraformVersion             *string
	Hooks                        map[string][]string
	EnableWellKnownSecretMasking bool
	EnableSensitiveOutputUpload  bool
	SkipVendorBinaryDownload     bool
	Autodeploy                   bool
	Autoretry                    bool
	Disabled                     bool
	LocalPreviewEnabled          bool
	GithubActionDeploy           bool
	Lock                         *Lock
	TrackedBranchHead            *Commit
	TrackedCommit                *Commit
	TrackedCommitSetBy           string
	ManagesState                 bool
	ExternalStateAccess          bool
	Deleting                     bool
	Version                      int64
}

// Stacks is what other domains call of the stacks domain. Each method
// joins the transaction its context carries.
type Stacks interface {
	// Get reads acct's stack id; ErrNotFound when acct lacks it.
	Get(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (Stack, error)
	// BySlug reads acct's stack slug; ErrNotFound when acct lacks it.
	BySlug(ctx context.Context, acct kernel.AccountID, slug string) (Stack, error)
	// ByIDs reads those of acct's stacks ids that it has, keyed by ID.
	ByIDs(ctx context.Context, acct kernel.AccountID, ids []kernel.ULID) (map[kernel.ULID]Stack, error)
	// Settings reads acct's stack id's settings; ErrNotFound when acct
	// lacks it.
	Settings(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (Settings, error)
	// ByRepository lists acct's stacks that track branch of repository
	// through integration, nil for the stacks that name no VCS
	// integration, by slug.
	ByRepository(ctx context.Context, acct kernel.AccountID, integration *kernel.ULID, repository, branch string) ([]Stack, error)
	// ByVCSRepository lists every branch's stacks using u on the exact
	// namespace and repository, excluding detached sources, by slug.
	ByVCSRepository(ctx context.Context, acct kernel.AccountID, u Use, namespace, repository string) ([]Stack, error)
	// LockForRun reads acct's stack id's settings and locks its row FOR
	// UPDATE in the context's transaction, so no write changes them
	// before a run created in that transaction commits; ErrNotFound when
	// acct lacks it.
	LockForRun(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (Settings, error)
	// Hold locks acct's stack id in the context's transaction until it
	// ends, as accessapi.Spaces.Hold does a space, for a domain about to
	// store the stack's ID: a delete or move of the stack waits for the
	// caller to commit, and each StackGuard it then asks sees what the
	// caller wrote. ErrNotFound when acct lacks it.
	Hold(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error
	// SetRunState writes the runs domain's projection of acct's stack id
	// in the context's transaction: state, its tracked run's state, and
	// blocked, whether a run blocks it. The stack's settings and version
	// stay as they are. ErrNotFound when acct lacks it.
	SetRunState(ctx context.Context, acct kernel.AccountID, id kernel.ULID, state kernel.StackState, blocked bool) error
	// Find lists acct's stacks in spaces that carry any of labels, by
	// slug.
	Find(ctx context.Context, acct kernel.AccountID, spaces []kernel.ULID, labels []string) ([]Stack, error)
	// InSpaces lists every stack of acct in spaces, by slug.
	InSpaces(ctx context.Context, acct kernel.AccountID, spaces []kernel.ULID) ([]Stack, error)
	// Labels reads the labels of those of acct's stacks ids that it has,
	// sorted, keyed by ID.
	Labels(ctx context.Context, acct kernel.AccountID, ids []kernel.ULID) (map[kernel.ULID][]string, error)
	// SetTracked sets acct's stack id's branch head to head and its
	// tracked commit to commit, set by setBy, leaving either unchanged
	// when nil. It changes neither the stack's version nor its events;
	// ErrNotFound when acct lacks it.
	SetTracked(ctx context.Context, acct kernel.AccountID, id kernel.ULID, head, commit *Commit, setBy string) error
	// Destroyed ends the deletion of acct's stack id once its destroy run
	// ends, in the context's transaction: a finished run deletes the stack
	// as a delete does, and any other end leaves it, no longer deleting.
	// It leaves a stack that is not deleting as it is; ErrNotFound when
	// acct lacks it.
	//
	// ref: decision 0008 § "Destroy on delete"
	Destroyed(ctx context.Context, acct kernel.AccountID, id kernel.ULID, finished bool) error
	// DetachVCS detaches acct's stacks bound to integration, in the
	// context's transaction: each one forgets the integration and
	// refuses its commit operations until a write gives it a source
	// again. It changes neither the stacks' versions nor their events.
	DetachVCS(ctx context.Context, acct kernel.AccountID, integration kernel.ULID) error
	// SearchUsers reads a page of the stacks of p's account in the spaces
	// p may read that use u, which the query leaves the others out of; an
	// input the stacks' search fields cannot answer is a
	// *search.Invalid.
	SearchUsers(ctx context.Context, p kernel.Principal, u Use, in search.Input) (search.Result[Stack], error)
	// SuggestUsers lists the values those stacks hold under in's
	// predicates, and counts them.
	SuggestUsers(ctx context.Context, p kernel.Principal, u Use, in search.SuggestionsInput) (search.Suggestions, error)
	// VCSRepositories lists the distinct repository coordinates of every
	// stack using u in acct, including implicit default bindings, without
	// an actor's search filter. Only the integration-admin connection test
	// consumes this repository-only projection.
	VCSRepositories(ctx context.Context, acct kernel.AccountID, u Use) ([]VCSRepository, error)
}

// VCSRepository is a used repository's provider coordinates, without any
// stack identity or setting unrelated to webhook configuration.
type VCSRepository struct{ Namespace, Name string }

// Use names the stacks that use a VCS integration: those bound to
// Integration and, when Default is set, those on Provider that name no
// integration, which use their provider's default. Aliases are additional
// provider names with that same default; they never match a named binding.
type Use struct {
	Integration kernel.ULID
	Provider    string
	Aliases     []string
	Default     bool
}

// ErrNoIntegration reports a VCS integration a stack may not bind: one
// the account lacks, of another provider, or in a space the stack's space
// does not reach.
var ErrNoIntegration = errors.New("stacks: no such VCS integration")

// StackIntegrations is what a stack's write checks its VCS integration
// with, a back edge the vcs domain implements: Bindable is nil when
// acct's integration id is of provider and in space or a space space
// inherits from, and ErrNoIntegration otherwise.
//
// ref: decisions 0004 and 0012
type StackIntegrations interface {
	Bindable(ctx context.Context, acct kernel.AccountID, space kernel.ULID, provider string, id kernel.ULID) error
	// DefaultAvailable reports whether the account has a default for provider,
	// without reading credentials or persisting that integration on the stack.
	DefaultAvailable(ctx context.Context, acct kernel.AccountID, provider string) (bool, error)
}

// ErrRefNotFound reports a branch or commit a stack's repository lacks.
var ErrRefNotFound = errors.New("stacks: ref not found")

// StackSources is what the stack's commit operations read of the vcs
// domain, which is constructed after stacks and implements it: ref, a
// branch, a tag or a full commit SHA, resolved on acct's stack id's
// repository; ErrRefNotFound when the repository lacks it,
// kernel.ErrUnavailable when its provider cannot be reached, and a
// *kernel.Invalid when the stack's source is one infraweave cannot read.
// The stacks domain declares it, so the back edge internal/app binds
// after construction adds no import.
//
// ref: decision 0004
type StackSources interface {
	Resolve(ctx context.Context, acct kernel.AccountID, id kernel.ULID, ref string) (Commit, error)
}

// StackDestroyer is what a stack's delete with destroyResources asks of
// the runs domain, which is constructed after stacks and implements it:
// Destroy creates p's DESTROY run on p's account's stack id in the
// transaction ctx carries, which holds the stack's row; an *Undestroyable when the
// stack cannot run one. The stacks domain declares it, so the back edge
// internal/app binds after construction adds no import.
//
// ref: decisions 0004 and 0008
type StackDestroyer interface {
	Destroy(ctx context.Context, p kernel.Principal, id kernel.ULID) error
}

// Undestroyable is a destroy run the runs domain refuses, with Detail
// saying why as the API answers it, and LockedBy naming the holder of the
// stack's lock when another caller's lock refuses it.
type Undestroyable struct {
	Detail   string
	LockedBy string
}

func (u *Undestroyable) Error() string { return "stacks: no destroy run: " + u.Detail }

// StackGuard is a domain's say in a stack's moves and deletes: a domain
// whose rows name stacks registers one, which the stacks domain asks in
// the move's or delete's transaction, which the context carries, after it
// locks the stack's row FOR UPDATE. That lock waits for every transaction
// holding the stack, and each method's statement then sees what it
// committed, so a row written under a hold is never missed.
//
// ref: decision 0004
type StackGuard interface {
	// LostOnMove lists each entity the stack would no longer reach if m
	// were made, by its kind and name, such as "context prod", which
	// refuses the move.
	LostOnMove(ctx context.Context, m StackMove) ([]string, error)
	// Deleting deletes the domain's rows of st, which belong to the stack
	// and mean nothing without it.
	Deleting(ctx context.Context, st Stack) error
}

// ManagedState is the state domain's part in creating a stack whose
// state infraweave keeps, which that domain, constructed after stacks,
// implements and internal/app binds. The stacks domain calls Created in
// the creation's transaction, which the context carries, after it writes
// the stack's row, so the state's rows commit with the stack and a
// failure fails the create, leaving nothing created. It is the one port
// a stack's creation reaches the state domain through. The stacks domain
// declares it, so the back edge adds no import.
//
// ref: decision 0004
// ref: decision 0010 § "Storage"
// ref: decision 0010 § "Upload, import and download URLs"
type ManagedState interface {
	// Created opens st's default workspace and, when object is not
	// empty, imports the state file p uploaded under that object ID as
	// the workspace's first version, marked imported by p. An upload p
	// did not ask for, or that is gone, too large or no state, is a
	// *kernel.Invalid pointing at /stackObjectID.
	Created(ctx context.Context, p kernel.Principal, st Stack, object string) error
}

// StateRollbacks is what the stack view reads of the state domain, which
// is constructed after stacks and implements it: which of the account's
// stacks are rolled back, those whose default workspace's current state
// version is a rollback, keyed by stack ID, with any other absent, which
// isStateRollback answers. The stacks domain declares it, so the back
// edge internal/app binds after construction adds no import.
//
// ref: decision 0004
// ref: decision 0010 § "Rollback, searches and retention"
type StateRollbacks interface {
	RolledBack(ctx context.Context, acct kernel.AccountID, stacks []kernel.ULID) (map[kernel.ULID]bool, error)
}

// StackMove is a proposed move of a stack from the space it lives in to
// To.
type StackMove struct {
	Stack
	To kernel.ULID
}

// StackConfiguration is what the stack view reads of the configuration
// domain, which is constructed after stacks and implements it: the
// contexts attached to each of the account's stacks, by priority, and
// each stack's own config elements and resolved runtime configuration,
// by ID, keyed by stack ID. A stack with no stored elements still has
// computed runtime elements. The stacks domain declares it, so the back
// edge internal/app binds after construction adds no import.
//
// ref: decision 0004
type StackConfiguration interface {
	AttachedContexts(ctx context.Context, acct kernel.AccountID, stacks []kernel.ULID) (map[kernel.ULID][]AttachedContext, error)
	Config(ctx context.Context, acct kernel.AccountID, stacks []kernel.ULID) (map[kernel.ULID][]ConfigElement, error)
	// RuntimeConfig reads the stored-source winners and computed fallback
	// elements, by ID, for all stacks in one batch.
	RuntimeConfig(ctx context.Context, acct kernel.AccountID, stacks []kernel.ULID) (map[kernel.ULID][]ConfigElementWithSource, error)
}

// StackStatus is what the stack view reads of the runs domain, which is
// constructed after stacks and implements it: the run blocking each of
// the account's stacks, the run that holds its run lock, keyed by stack
// ID, with a stack no run blocks absent, the stack a run belongs to, and
// the outputs of each stack's last applied run.
// The stacks domain declares it,
// so the back edge internal/app binds after construction adds no import.
//
// ref: decisions 0004 and 0008
type StackStatus interface {
	Blockers(ctx context.Context, acct kernel.AccountID, stacks []kernel.ULID) (map[kernel.ULID]Blocker, error)
	// RunStack is the stack of acct's run, which runStack answers;
	// ErrNotFound when acct lacks the run.
	RunStack(ctx context.Context, acct kernel.AccountID, run kernel.ULID) (kernel.ULID, error)
	// Outputs are the outputs of each of acct's stacks, by name, keyed by
	// stack ID, with a stack that has none absent.
	Outputs(ctx context.Context, acct kernel.AccountID, stacks []kernel.ULID) (map[kernel.ULID][]Output, error)
}

// Output is one of a stack's outputs as its last applied run left it:
// its name, description, whether it is sensitive, and its value as JSON,
// nil for a sensitive output, whose value no read answers.
//
// ref: decision 0008 § "Logs and artifacts"
type Output struct {
	Name        string
	Description string
	Sensitive   bool
	Value       json.RawMessage
}

// Blocker is the run that holds a stack's run lock: Run, its ID, and
// Delta, its plan's delta while it waits in UNCONFIRMED for the plan's
// confirmation, which the stack's view shows; nil in any other state.
//
// ref: decision 0008 § "Phases, reports and the plan decision"
type Blocker struct {
	Run   kernel.ULID
	Delta *kernel.PlanDelta
}

// AttachedContext is a context attached to a stack as the stack's view
// shows it: the attachment's ID; the context's slug, name, description,
// labels, hooks by phase and config elements; the attachment's priority,
// lower first; and whether one of the context's autoattach labels
// attached it.
type AttachedContext struct {
	ID           kernel.ULID
	Context      string
	Name         string
	Description  *string
	Labels       []string
	Hooks        map[string][]string
	Config       []ConfigElement
	Priority     int32
	Autoattached bool
}

// ConfigElement is an environment variable, or a file mounted in the
// workspace when FileMount is set, as a view shows it: ID is the
// variable's name or the file's path, Value is nil when WriteOnly is set,
// and Checksum is the value's SHA-256 in hex.
type ConfigElement struct {
	ID          string
	FileMount   bool
	Value       *string
	WriteOnly   bool
	Checksum    string
	Description string
	FileMode    *string
	CreatedAt   time.Time
}

// ConfigElementWithSource is a stored winner with its context attachment,
// or an infraweave computed fallback when Runtime is set. A computed
// element has no value or creation time; its checksum and description are
// empty. The actual run token and phase state are injected at execution.
type ConfigElementWithSource struct {
	ConfigElement
	Context *AttachedContext
	Runtime bool
}

// ChangedKind is the outbox event a stack's write appends in its
// transaction, at ChangedVersion, with a Changed document. Its subject is
// the stack's ULID and its sequence the stack's Version after the write.
const (
	ChangedKind    = "stack.changed"
	ChangedVersion = 1
)

// The changes a Changed event names.
const (
	ChangeCreated   = "created"
	ChangeUpdated   = "updated"
	ChangeReslugged = "reslugged"
	ChangeDisabled  = "disabled"
	ChangeEnabled   = "enabled"
	ChangeLocked    = "locked"
	ChangeUnlocked  = "unlocked"
	ChangeDeleted   = "deleted"
)

// Changed is a ChangedKind event's document: the stack's ULID and slug
// after the change, which change it was, and whether it gave the stack
// its source or changed it: its provider, VCS integration, namespace,
// repository, repository URL or branch.
type Changed struct {
	Stack  kernel.ULID `json:"stack"`
	Slug   string      `json:"slug"`
	Change string      `json:"change"`
	Source bool        `json:"source,omitempty"`
}

// ChangedSchema is the JSON Schema of ChangedVersion's documents.
var ChangedSchema = []byte(`{
	"type": "object",
	"required": ["stack", "slug", "change"],
	"properties": {
		"stack": {"type": "string", "minLength": 26, "maxLength": 26},
		"slug": {"type": "string", "minLength": 1},
		"change": {"enum": ["created", "updated", "reslugged", "disabled", "enabled",
			"locked", "unlocked", "deleted"]},
		"source": {"type": "boolean"}
	}
}`)
