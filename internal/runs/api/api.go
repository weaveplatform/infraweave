package api

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// RunSources is what a run's handover and its end ask of the vcs domain,
// a back edge internal/app binds after it constructs that domain.
type RunSources interface {
	// Source is where acct's run fetches commit of stack from, with the
	// credential it fetches with, minted for the run alone where the
	// provider narrows one. It joins the transaction ctx carries, so a
	// handover that fails keeps no record of a minted token.
	Source(
		ctx context.Context,
		acct kernel.AccountID,
		stack, run kernel.ULID,
		commit string,
	) (Source, error)
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

// StateGrant is what a run may do with its stack's managed state in the
// run's current state; one with Read, Lock and Write unset grants nothing.
// Account is the run's account whenever some account has the run, which
// the state domain scopes its rows and a stack's slug by, so no part of a
// request's URL chooses the account it reaches.
//
// ref: decision 0010 § "Who may call what"
type StateGrant struct {
	Account kernel.AccountID
	Read    bool
	Lock    StateLock
	Write   bool
}

// StateAccess decides a run token's access to a stack's managed state, a
// forward edge internal/app hands the state domain.
type StateAccess interface {
	// Access answers run's grant on stack's state: the zero StateGrant
	// when no account has the run, and one granting nothing but naming the
	// run's account when it is another stack's or its state grants
	// nothing. It reads the run on every call, so a run that ends or stops
	// loses its access at once, whatever its token's expiry.
	Access(ctx context.Context, run, stack kernel.ULID) (StateGrant, error)
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
// the move carries none.
//
// ref: report 19 § "Input documents"
type Updated struct {
	State    kernel.RunState `json:"state"`
	Username *string         `json:"username"`
	Note     *string         `json:"note"`
	Run      UpdatedRun      `json:"run"`
	Stack    UpdatedStack    `json:"stack"`
}

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
