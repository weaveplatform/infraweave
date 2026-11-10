package api

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
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

// Settings are what a run of the stack reads. Vendor is the iac vendor's
// kind and VendorConfig its settings as the vendor checked them; Hooks
// maps each phase that has commands to them, in order. WorkerPool and
// VCSIntegration are nil when the stack names none. Version counts the
// stack's writes.
type Settings struct {
	Stack
	Repository             string
	RepositoryURL          *string
	Branch                 string
	Provider               string
	Namespace              string
	ProjectRoot            *string
	AdditionalProjectGlobs []string
	GitSparseCheckoutPaths []string
	RunnerImage            *string
	WorkerPool             *kernel.ULID
	VCSIntegration         *kernel.ULID
	Vendor                 string
	VendorConfig           json.RawMessage
	TerraformVersion       *string
	Hooks                  map[string][]string
	Autodeploy             bool
	Autoretry              bool
	Disabled               bool
	Lock                   *Lock
	Version                int64
}

// Stacks is what other domains call of the stacks domain. Each method
// joins the transaction its context carries.
type Stacks interface {
	// Get reads acct's stack id; ErrNotFound when acct lacks it.
	Get(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (Stack, error)
	// BySlug reads acct's stack slug; ErrNotFound when acct lacks it.
	BySlug(ctx context.Context, acct kernel.AccountID, slug string) (Stack, error)
	// ByIDs reads those of acct's stacks ids that it has, keyed by ID.
	ByIDs(
		ctx context.Context,
		acct kernel.AccountID,
		ids []kernel.ULID,
	) (map[kernel.ULID]Stack, error)
	// Settings reads acct's stack id's settings; ErrNotFound when acct
	// lacks it.
	Settings(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (Settings, error)
	// ByRepository lists acct's stacks that track branch of repository
	// through integration, nil for the stacks that name no VCS
	// integration, by slug.
	ByRepository(
		ctx context.Context,
		acct kernel.AccountID,
		integration *kernel.ULID,
		repository, branch string,
	) ([]Stack, error)
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
	SetRunState(
		ctx context.Context,
		acct kernel.AccountID,
		id kernel.ULID,
		state kernel.StackState,
		blocked bool,
	) error
	// Find lists acct's stacks in spaces that carry any of labels, by
	// slug.
	Find(
		ctx context.Context,
		acct kernel.AccountID,
		spaces []kernel.ULID,
		labels []string,
	) ([]Stack, error)
	// InSpaces lists every stack of acct in spaces, by slug.
	InSpaces(ctx context.Context, acct kernel.AccountID, spaces []kernel.ULID) ([]Stack, error)
	// Labels reads the labels of those of acct's stacks ids that it has,
	// sorted, keyed by ID.
	Labels(
		ctx context.Context,
		acct kernel.AccountID,
		ids []kernel.ULID,
	) (map[kernel.ULID][]string, error)
}

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

// StackMove is a proposed move of a stack from the space it lives in to
// To.
type StackMove struct {
	Stack
	To kernel.ULID
}

// StackConfiguration is what the stack view reads of the configuration
// domain, which is constructed after stacks and implements it: the
// contexts attached to each of the account's stacks, by priority, and
// each stack's own config elements, by ID, keyed by stack ID, with a
// stack that has none absent. The stacks domain declares it, so the back
// edge internal/app binds after construction adds no import.
//
// ref: decision 0004
type StackConfiguration interface {
	AttachedContexts(
		ctx context.Context,
		acct kernel.AccountID,
		stacks []kernel.ULID,
	) (map[kernel.ULID][]AttachedContext, error)
	Config(
		ctx context.Context,
		acct kernel.AccountID,
		stacks []kernel.ULID,
	) (map[kernel.ULID][]ConfigElement, error)
}

// StackStatus is what the stack view reads of the runs domain, which is
// constructed after stacks and implements it: the run blocking each of
// the account's stacks, the run that holds its run lock, keyed by stack
// ID, with a stack no run blocks absent. The stacks domain declares it,
// so the back edge internal/app binds after construction adds no import.
//
// ref: decisions 0004 and 0008
type StackStatus interface {
	Blockers(
		ctx context.Context,
		acct kernel.AccountID,
		stacks []kernel.ULID,
	) (map[kernel.ULID]kernel.ULID, error)
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
// after the change, and which change it was.
type Changed struct {
	Stack  kernel.ULID `json:"stack"`
	Slug   string      `json:"slug"`
	Change string      `json:"change"`
}

// ChangedSchema is the JSON Schema of ChangedVersion's documents.
var ChangedSchema = []byte(`{
	"type": "object",
	"required": ["stack", "slug", "change"],
	"properties": {
		"stack": {"type": "string", "minLength": 26, "maxLength": 26},
		"slug": {"type": "string", "minLength": 1},
		"change": {"enum": ["created", "updated", "reslugged", "disabled", "enabled",
			"locked", "unlocked", "deleted"]}
	}
}`)
