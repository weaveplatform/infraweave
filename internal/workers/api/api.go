package api

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ErrNoPool reports a worker pool the account lacks or that the space
// asking for it does not reach.
var ErrNoPool = errors.New("workers: no such worker pool in reach")

// Pools is what other domains call of the workers domain's pools.
type Pools interface {
	// Hold checks that acct's pool id lives in space or in a space space
	// inherits from, through an unbroken chain of inheriting spaces, and
	// holds the pool for the transaction ctx carries, so no delete or move
	// of it commits before the caller's write; ErrNoPool otherwise.
	Hold(ctx context.Context, acct kernel.AccountID, space, id kernel.ULID) error
}

// PoolUse is a stack that names a pool: its slug and the space it lives
// in.
type PoolUse struct {
	Stack string
	Space kernel.ULID
}

// PoolGuard is a domain whose rows name pools. A pool's delete and move
// ask it, after the pool's row is locked, which of its rows name the pool.
type PoolGuard interface {
	// PoolInUse lists the uses of acct's pool id.
	PoolInUse(ctx context.Context, acct kernel.AccountID, id kernel.ULID) ([]PoolUse, error)
}

// Claim is a worker's claim of a run: the worker, its account and pool,
// and DriftLimit, the pool's drift detection run limit, nil or negative
// for none.
type Claim struct {
	Account    kernel.AccountID
	Pool       kernel.ULID
	Worker     kernel.ULID
	DriftLimit *int
}

// Assignment is a claimed run, the state the claim moved it to,
// PREPARING, PREPARING_APPLY or PREPARING_REPLAN, and its state version
// after the claim.
type Assignment struct {
	Run          kernel.ULID
	State        kernel.RunState
	StateVersion int64
}

// ErrNotOnWorker reports a run that is not on a private pool's worker, or
// that no account has, whose run token the worker API refuses.
var ErrNotOnWorker = errors.New("workers: the run is not on a worker")

// Handover is what a claimed run needs, as the runs domain builds it: the
// run's account and pool, which the workers domain seals Secrets to, the
// specification, and Secrets, the JSON of the worker API's
// RunSpecSecrets, which Reveal alone renders.
type Handover struct {
	Account kernel.AccountID
	Pool    kernel.ULID
	Spec    Spec
	Secrets Secret
}

// Secret is a value that renders as [redacted] everywhere but Reveal.
type Secret interface{ Reveal() []byte }

// Spec is a run's specification, what the worker API's RunSpec answers
// but sealed: no write-only value is in it.
type Spec struct {
	Run    SpecRun
	Stack  SpecStack
	Commit SpecCommit
	// RuntimeConfig is the runtime configuration the run's trigger gave,
	// nil when it gave none.
	RuntimeConfig json.RawMessage
	// RunnerImage is the runtime configuration's or the stack's image;
	// nil when neither names one.
	RunnerImage *string
	Hooks       map[string][]string
	Environment []Variable
	Files       []File
}

// SpecRun is the run a Spec is for, at the state version its secrets are
// sealed for.
type SpecRun struct {
	ID             kernel.ULID
	Type           kernel.RunType
	State          kernel.RunState
	StateVersion   int64
	TriggeredBy    string
	Priority       kernel.RunPriority
	DriftDetection bool
	CreatedAt      time.Time
}

// SpecStack is a Spec's stack: Slug, and Space as its slug, as the API
// names them; ProjectRoot is nil for the repository's root.
type SpecStack struct {
	Slug         string
	Name         string
	Space        string
	Labels       []string
	Repository   string
	Branch       string
	ProjectRoot  *string
	Vendor       string
	VendorConfig json.RawMessage
}

// SpecCommit is the commit a Spec's run works on.
type SpecCommit struct {
	Hash        string
	Tag         string
	URL         string
	Message     string
	AuthorName  string
	AuthorLogin *string
	Timestamp   time.Time
}

// Variable is an environment variable of a Spec: Value is nil when it is
// write-only, its value being in the Handover's Secrets.
type Variable struct {
	Name      string
	Value     *string
	WriteOnly bool
}

// File is a file mounted into a Spec's run: Content is nil when it is
// write-only, its content being in the Handover's Secrets.
type File struct {
	Path      string
	Mode      *string
	Checksum  string
	Content   []byte
	WriteOnly bool
}

// Scheduler is the runs domain's side of the worker protocol, a back edge
// internal/app binds after it constructs that domain.
//
// ref: decisions 0004 and 0008 § "Claiming runs"
type Scheduler interface {
	// Claim moves the run that has waited longest in the first tier of
	// c's pool to its preparing state for c's worker, in the transaction
	// ctx carries, skipping rows another transaction holds, so two
	// claims never take one run. It claims nothing, and reports false,
	// when no run waits, when the worker holds a run already, or when
	// only drift runs wait and the pool's drift runs on workers reach
	// DriftLimit.
	Claim(ctx context.Context, c Claim) (Assignment, bool, error)
	// Handover builds the specification of run, which a run token names,
	// in the transaction ctx carries: ErrNotOnWorker when no account has
	// the run or it is not on a private pool's worker, and a
	// *kernel.Invalid naming the variable whose template fails to render.
	// It mints the run's in-run API token, and the run's artifact key the
	// first time.
	//
	// ref: decision 0008 § "The run specification and its secrets"
	Handover(ctx context.Context, run kernel.ULID) (Handover, error)
}

// Waiting is the infraweave_runs payload that says run waits in pool,
// which wakes one held poll of the pool. The run's ID keeps two runs made
// ready in one transaction from folding into one notification, which
// PostgreSQL does to identical payloads. A payload of the pool's ID alone
// wakes every held poll of the pool.
func Waiting(pool, run kernel.ULID) string { return pool.String() + "/" + run.String() }
