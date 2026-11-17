package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// SourceFileLimit bounds repository files read for a run's configuration.
const SourceFileLimit = 1 << 20

// ErrFileTooLarge is a repository file exceeding SourceFileLimit. It is a
// configuration refusal, rather than a provider outage.
var ErrFileTooLarge = errors.New("runs: repository file exceeds 1 MiB")

// RunSources is what a run's handover and its end ask of the vcs domain,
// a back edge internal/app binds after it constructs that domain.
type RunSources interface {
	// File reads path at commit from acct's stack's repository. False means
	// no such file; ErrFileTooLarge means it exceeds SourceFileLimit.
	File(ctx context.Context, acct kernel.AccountID, stack kernel.ULID, commit, path string) ([]byte, bool, error)
	// Source is where acct's run fetches commit of stack from, with the
	// credential it fetches with, minted for the run alone where the
	// provider narrows one. It joins the transaction ctx carries, so a
	// handover that fails keeps no record of a minted token.
	Source(ctx context.Context, acct kernel.AccountID, stack, run kernel.ULID, commit string) (Source, error)
	// Release revokes the credentials minted for acct's run, in a job the
	// transaction ctx carries enqueues once per run.
	Release(ctx context.Context, acct kernel.AccountID, run kernel.ULID) error
}

// Source is a run's source: the HTTPS Git URL its worker fetches the
// run's commit from, and the credential it fetches with, nil for a
// repository reached with none, as raw Git's is.
type Source struct {
	URL        string
	Credential *SourceCredential
}

// SourceCredential is the token a run's worker fetches its source with,
// over git's HTTPS basic auth as Username. Narrowed is true for a token
// minted to read the run's repository alone. It is secret material: the
// handover seals it, and its String, GoString and LogValue leave the
// token out, so a log, an error or an event that formats it never
// carries the token.
type SourceCredential struct {
	Username string
	Token    []byte
	Narrowed bool
}

// String is c without its token.
func (c SourceCredential) String() string {
	return fmt.Sprintf("source credential of %s (narrowed %t)", c.Username, c.Narrowed)
}

// GoString is c without its token.
func (c SourceCredential) GoString() string { return c.String() }

// LogValue is c without its token.
func (c SourceCredential) LogValue() slog.Value {
	return slog.GroupValue(slog.String("username", c.Username), slog.Bool("narrowed", c.Narrowed))
}

// StateLock is the lock on its stack's state that a run may take.
type StateLock int

const (
	// StateLockNone grants no lock.
	StateLockNone StateLock = iota
	// StateLockShared grants a lock that other shared holders hold too,
	// as runs that only read the state do.
	StateLockShared
	// StateLockExclusive grants a lock no other run holds, as a run that
	// writes the state needs.
	StateLockExclusive
)

// StateGrant is what a run may do with a stack's managed state in the
// run's current state; one with Read, Lock and Write unset grants nothing.
// Account and Stack are the run's account and its own stack whenever some
// account has the run; the state domain scopes its rows and a stack's
// slug by Account, so no part of a request's URL chooses the account it
// reaches. On another stack's state a run may at most read, as its stack's
// bindings and the stack's external access, which the state domain
// decides, allow. TriggeredBy is the login or trigger that created the
// run, which the state domain records with each lock the run takes and
// answers as the workspace's lockedBy.
//
// ref: decision 0010 § "Who may call what"
// ref: decision 0010 § "Storage"
type StateGrant struct {
	Account     kernel.AccountID
	Stack       kernel.ULID
	Read        bool
	Lock        StateLock
	Write       bool
	TriggeredBy string
}

// StateAccess decides a run's access to a stack's managed state, a
// forward edge internal/app hands the state domain.
type StateAccess interface {
	// Access answers run's grant on stack's state: the zero StateGrant
	// when no account has the run, one granting what the run's state
	// allows on its own stack, and on another stack Read alone, when the
	// run's state allows it a read. It reads the run on every call, so a
	// run that ends or stops loses its access at once, whatever its
	// token's expiry.
	Access(ctx context.Context, run, stack kernel.ULID) (StateGrant, error)
}

// StackRuns reads a stack's runs for a domain that changes the stack only
// while no run that may change it is under way, such as a state import, a
// forward edge internal/app hands the state domain.
type StackRuns interface {
	// Unended lists acct's stack's runs that have not ended, oldest first,
	// with their types and states, in the transaction ctx carries; none
	// for a stack acct lacks. A caller that holds the stack's row FOR
	// UPDATE reads every run there is, since a run's creation locks that
	// row first.
	Unended(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) ([]UnendedRun, error)
}

// RunEnds tells which runs have not ended, for a domain that keeps what a
// run wrote until the run ends, such as state retention, which never
// deletes a version a run under way wrote; a forward edge internal/app
// hands the state domain.
type RunEnds interface {
	// NotEnded answers those of acct's runs that have not ended, in the
	// order of their IDs, in the transaction ctx carries. A run acct
	// lacks has ended, since only its stack's or its account's deletion
	// removes a run.
	NotEnded(ctx context.Context, acct kernel.AccountID, runs []kernel.ULID) ([]kernel.ULID, error)
}

// UnendedRun is a run that has not ended: its ID, its type, which tells a
// run that takes its stack's exclusive lock from one that never does, and
// its state.
type UnendedRun struct {
	ID    kernel.ULID
	Type  kernel.RunType
	State kernel.RunState
}

// UpdatedKind is the event the runs domain appends to the outbox each
// time a run changes state, its creation included.
//
// ref: decision 0015 § "One event model"
const (
	UpdatedKind    = "run_updated"
	UpdatedVersion = 1
)

// Updated is an UpdatedKind event's document, the notification policy's
// run_updated input: snake_case, with timestamps in nanoseconds since the
// Unix epoch. Username is nil when the server moved the run, and Note when
// the move carries none. Timer names the run.timers rule that moved the
// run, and is absent when none did.
//
// ref: report 19 § "Input documents"
// ref: decision 0008 § "Timeouts and timers"
type Updated struct {
	State    kernel.RunState `json:"state"`
	Username *string         `json:"username"`
	Note     *string         `json:"note"`
	Timer    Timer           `json:"timer,omitempty"`
	Run      UpdatedRun      `json:"run"`
	Stack    UpdatedStack    `json:"stack"`
}

// Timer is a run.timers rule, by its key under run.timers. The shorter
// rules notify; TimerMaxAge, which ends a run a month after its creation,
// never does.
//
// ref: report 08 § "Timers"
type Timer string

// The run.timers rules.
const (
	TimerPickupProposed Timer = "pickup_proposed"
	TimerPickupBlocking Timer = "pickup_blocking"
	TimerUnconfirmed    Timer = "unconfirmed"
	TimerConfirmed      Timer = "confirmed"
	TimerMaxAge         Timer = "max_age"
)

// UpdatedRun is the run an Updated event is about; TriggeredBy is nil
// when no login or trigger is recorded.
//
//nolint:tagliatelle // the names the notification policy's input sets
type UpdatedRun struct {
	ID             kernel.ULID     `json:"id"`
	Type           kernel.RunType  `json:"type"`
	State          kernel.RunState `json:"state"`
	Commit         UpdatedCommit   `json:"commit"`
	CreatedAt      int64           `json:"created_at"`
	UpdatedAt      int64           `json:"updated_at"`
	DriftDetection bool            `json:"drift_detection"`
	TriggeredBy    *string         `json:"triggered_by"`
}

// UpdatedCommit is the commit the run works on.
//
//nolint:tagliatelle // the names the notification policy's input sets
type UpdatedCommit struct {
	Author    string `json:"author"`
	Hash      string `json:"hash"`
	Message   string `json:"message"`
	URL       string `json:"url"`
	Tag       string `json:"tag"`
	CreatedAt int64  `json:"created_at"`
}

// UpdatedStack is the run's stack: ID is its slug, as the policy input
// keeps it.
type UpdatedStack struct {
	ID   string      `json:"id"`
	ULID kernel.ULID `json:"ulid"`
	Name string      `json:"name"`
}

// UpdatedSchema is the JSON Schema of UpdatedVersion's documents. A later
// field of the policy input joins as an optional property, so documents
// of this version stay valid.
var UpdatedSchema = []byte(`{
	"type": "object",
	"required": ["state", "username", "note", "run", "stack"],
	"properties": {
		"state": {"type": "string", "minLength": 1},
		"username": {"type": ["string", "null"]},
		"note": {"type": ["string", "null"]},
		"timer": {"enum": ["pickup_proposed", "pickup_blocking", "unconfirmed", "confirmed",
			"max_age"]},
		"run": {
			"type": "object",
			"required": ["id", "type", "state", "commit", "created_at", "updated_at",
				"drift_detection", "triggered_by"],
			"properties": {
				"id": {"type": "string", "minLength": 26, "maxLength": 26},
				"type": {"enum": ["PROPOSED", "TRACKED", "TASK", "TESTING", "DESTROY"]},
				"state": {"type": "string", "minLength": 1},
				"commit": {
					"type": "object",
					"required": ["author", "hash", "message", "url", "tag", "created_at"],
					"properties": {
						"author": {"type": "string"},
						"hash": {"type": "string"},
						"message": {"type": "string"},
						"url": {"type": "string"},
						"tag": {"type": "string"},
						"created_at": {"type": "integer"}
					}
				},
				"created_at": {"type": "integer"},
				"updated_at": {"type": "integer"},
				"drift_detection": {"type": "boolean"},
				"triggered_by": {"type": ["string", "null"]}
			}
		},
		"stack": {
			"type": "object",
			"required": ["id", "ulid", "name"],
			"properties": {
				"id": {"type": "string", "minLength": 1},
				"ulid": {"type": "string", "minLength": 26, "maxLength": 26},
				"name": {"type": "string"}
			}
		}
	}
}`)
