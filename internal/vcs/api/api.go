package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ErrRefNotFound reports a branch, tag or commit a stack's repository
// lacks.
var ErrRefNotFound = errors.New("vcs: ref not found")

// Commit is a commit of a stack's repository as runs and the stack view
// read it. Message is the first line of its message; AuthorLogin is nil
// when the provider knows no login for its author, as raw Git never does.
type Commit struct {
	Hash        string
	Tag         string
	URL         string
	Message     string
	AuthorName  string
	AuthorLogin *string
	Timestamp   time.Time
}

// Sources is what other domains call of the vcs domain to reach a stack's
// source. Each method reads the stack in the transaction its context
// carries; stacksapi.ErrNotFound when the account lacks it. A stack whose
// provider infraweave reads no source of is a *kernel.Invalid naming
// /provider.
type Sources interface {
	// Resolve resolves ref, a branch, a tag or a full commit SHA, on the
	// repository of acct's stack id; ErrRefNotFound when the repository
	// lacks it.
	Resolve(
		ctx context.Context,
		acct kernel.AccountID,
		stack kernel.ULID,
		ref string,
	) (Commit, error)
	// SourceURL is the HTTPS Git URL a worker fetches commit of acct's
	// stack id from, with Credential's answer as Basic auth; the server
	// never proxies a source.
	SourceURL(
		ctx context.Context,
		acct kernel.AccountID,
		stack kernel.ULID,
		commit string,
	) (string, error)
	// Credential is the credential acct's run fetches the source of
	// stack with, minted for the run alone where the provider narrows
	// one; nil for a repository reached with none, as raw Git's is.
	Credential(
		ctx context.Context,
		acct kernel.AccountID,
		stack, run kernel.ULID,
	) (*Credential, error)
	// Release revokes the credentials minted for acct's run, in a job the
	// transaction ctx carries enqueues once per run, so the caller calls
	// it in the transaction that ends the run.
	Release(ctx context.Context, acct kernel.AccountID, run kernel.ULID) error
}

// Credential is a token a run's worker fetches its repository with, over
// git's HTTPS basic auth as Username. It is secret material: the run seals
// it, and its String, GoString and LogValue leave the token out, so a
// log, an error or an event that formats it never carries the token.
//
// ref: decision 0012 § "Source access for runs"
type Credential struct {
	Username string
	Token    []byte
	// ExpiresAt is when a minted token expires; nil for the integration's
	// own.
	ExpiresAt *time.Time
	// Narrowed is true for a token minted to read the run's repository
	// alone, and false for the integration's own token, which reads every
	// repository the integration does; the run's log says which.
	Narrowed bool
}

// String is c without its token.
func (c Credential) String() string {
	return fmt.Sprintf("vcs credential of %s (narrowed %t)", c.Username, c.Narrowed)
}

// GoString is c without its token.
func (c Credential) GoString() string { return c.String() }

// LogValue is c without its token.
func (c Credential) LogValue() slog.Value {
	return slog.GroupValue(slog.String("username", c.Username), slog.Bool("narrowed", c.Narrowed))
}
