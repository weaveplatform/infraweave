package api

import (
	"context"
	"errors"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ErrCommentInput refuses an invalid notification comment before enqueueing.
var ErrCommentInput = errors.New("invalid VCS pull request comment")

// ErrCommentNotFound is a target pull request absent from the account.
var ErrCommentNotFound = errors.New("VCS pull request comment target not found")

// PullRequestComment addresses a recorded VCS pull request by its account-bound
// local identity. DedupKey is stable across attempts and updates. Body is plain
// Markdown; VCS appends its recovery marker. Policy eligibility belongs to the
// caller, which has already decided access to this account's target.
type PullRequestComment struct {
	PullRequest kernel.ULID
	DedupKey    string
	Body        string
}

// Comments queues a notification comment update in its caller's transaction.
// Native effects run only in VCS jobs. Plan comments use VCS's own run-based
// keys and never edit another run's plan.
type Comments interface {
	Upsert(context.Context, kernel.AccountID, PullRequestComment) error
}
