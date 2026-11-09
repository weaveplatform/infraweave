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
// domain holds, its slug, which a reslug changes, and the space it lives
// in.
type Stack struct {
	ID      kernel.ULID
	Account kernel.AccountID
	Slug    string
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
	Name                   string
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
