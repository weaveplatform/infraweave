package api

import (
	"context"
	"errors"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// PolicyLockPrefix separates a policy's caller key from every human/API actor.
// A policy key can only release or reuse a lock in this namespace.
const PolicyLockPrefix = "policy:"

var (
	// ErrPolicyLockInput refuses incomplete account or stack ownership.
	ErrPolicyLockInput = errors.New("stacks: invalid push policy lock input")
	// ErrPolicyLockConflict refuses replacing a different policy or human lock.
	ErrPolicyLockConflict = errors.New("stacks: another owner holds the stack lock")
	// ErrPolicyLockTransaction requires an atomic run-creation transaction.
	ErrPolicyLockTransaction = errors.New("stacks: push policy locks require a transaction")
)

// PolicyLocks changes only locks owned by an exact push-policy key. The caller
// holds account/stack ownership and invokes it only after creating a new run,
// before committing that run. A failure rolls back the caller's transaction.
type PolicyLocks interface {
	UpdatePushLock(context.Context, kernel.AccountID, kernel.ULID, string, string) error
}
