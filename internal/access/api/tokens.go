package api

import (
	"context"
	"errors"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// The worker protocol's audiences: a worker session token's and a run
// token's. Neither is the API's, so the API treats both as no credential.
//
// ref: decision 0006 § "Token kinds"
const (
	WorkerAudience = "infraweave-worker"
	RunnerAudience = "infraweave-runner"
)

// ErrInvalidToken is every token Tokens refuses, whichever part failed.
var ErrInvalidToken = errors.New("invalid token")

// Token is a minted bearer token and when it expires.
type Token struct {
	Raw     string
	Expires time.Time
}

// InRunRequest names the run an in-run API token acts for: its account,
// the run, and the slug of its stack or module, which with the run's ULID
// makes the token's sub.
type InRunRequest struct {
	Account kernel.AccountID
	Run     kernel.ULID
	Slug    string
}

// Tokens mints and verifies the tokens the worker protocol hands out. A
// worker session token names a worker and lives auth.tokens.worker.ttl; a
// run token names a run and lives auth.tokens.run.ttl, and its holder is
// accepted only while the caller finds the run in a worker state. An
// in-run API token is an API token of a session with actor kind run,
// living auth.tokens.run_api.ttl, which authenticates only while
// Runs.RunAccess finds its run live.
//
// ref: decision 0006 § "Token kinds", § "In-run API token"
type Tokens interface {
	WorkerToken(ctx context.Context, worker kernel.ULID) (Token, error)
	// VerifyWorker is the worker a worker session token names; any other
	// token is ErrInvalidToken.
	VerifyWorker(ctx context.Context, raw string) (kernel.ULID, error)
	RunToken(ctx context.Context, run kernel.ULID) (Token, error)
	// VerifyRun is the run a run token names; any other token is
	// ErrInvalidToken.
	VerifyRun(ctx context.Context, raw string) (kernel.ULID, error)
	InRunToken(ctx context.Context, r InRunRequest) (Token, error)
}

// RunAccess is what authenticating an in-run API token needs of its run:
// its stack, the space the stack lives in, and whether the run is in a
// state whose token authenticates.
type RunAccess struct {
	Stack kernel.ULID
	Space kernel.ULID
	Live  bool
}

// Runs is the back edge an in-run API token's authentication reads its
// run through, which the runs domain implements. RunAccess answers
// ErrNoSubject for a run acct does not have.
//
// ref: decision 0006 § "In-run API token"
type Runs interface {
	RunAccess(ctx context.Context, acct kernel.AccountID, run kernel.ULID) (RunAccess, error)
}
