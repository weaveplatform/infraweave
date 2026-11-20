package api

import (
	"context"
	"errors"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// DedupMode is an internal trigger's creation rule. Human trigger operations
// keep their existing active-run reuse; this port chooses its rule explicitly.
type DedupMode string

const (
	// None always creates a fresh run, including when the same commit ran before.
	None DedupMode = "none"
	// OncePerCommit reuses any run of this stack, type and commit, in any state
	// and from any trigger. The check holds the stack's creation lock.
	OncePerCommit DedupMode = "once_per_commit"
)

var (
	// ErrNoTransaction refuses an internal write outside its caller's transaction.
	ErrNoTransaction = errors.New("runs: a run moves only in a transaction")
	// ErrCreationInput refuses an invalid internal creation request.
	ErrCreationInput = errors.New("runs: invalid creation request")
	// ErrCreationRefused marks a disabled, deleting or human-locked stack.
	ErrCreationRefused = errors.New("runs: creation refused by the stack")
	// ErrSupersedeInput refuses an incomplete internal superseding request.
	ErrSupersedeInput = errors.New("runs: invalid superseding request")
)

// Commit is the resolved repository commit an internal trigger supplies.
// The public port owns this value; conversion to another domain's metadata
// happens inside the runs hexagon.
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

// CreateRequest creates a tracked or proposed run on a resolved commit.
// Commit.Branch remains the caller's source branch. TriggeredBy identifies the
// trigger, for example vcs/commit or the user who pressed a provider action.
// BeforeCreate is an optional transactional admission check: it runs only after
// stack refusals and deduplication, immediately before a new run is inserted.
// It must use the supplied context for its database work and make no external
// calls. An error is returned unchanged through wrapping; no run is created.
type CreateRequest struct {
	Account     kernel.AccountID
	Stack       kernel.ULID
	Type        kernel.RunType
	Commit      Commit
	TriggeredBy string
	// PolicyLockCaller is a stack-policy owner key, never a human caller key.
	// Ordinary internal triggers leave it empty.
	PolicyLockCaller string
	Priority         kernel.RunPriority
	Receipts         []PolicyReceipt
	Mode             DedupMode
	BeforeCreate     func(context.Context) error
}

// CreatedRun identifies the new or existing run. Created is false only for a
// OncePerCommit duplicate; such a call invokes no admission check or side effect.
type CreatedRun struct {
	ID      kernel.ULID
	Created bool
}

// Creator is the forward creation port internal/app hands another domain.
// Create joins the transaction ctx carries, so its run, outbox and scheduling
// effects commit with the caller's queue fence and completion. The caller must
// roll back on an error after BeforeCreate succeeds.
type Creator interface {
	Create(ctx context.Context, request CreateRequest) (CreatedRun, error)
}

// Supersede asks workers to stop a branch's older proposed runs still planning.
// It joins the caller's transaction and reports newly recorded stop requests.
// Repeated requests preserve an existing stop, so a worker receives one signal.
type Supersede interface {
	Supersede(ctx context.Context, acct kernel.AccountID, stack kernel.ULID, branch, newHash string) (int, error)
}
