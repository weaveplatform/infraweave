package api

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// PolicySnapshot is a stack's persisted settings and run projection. Readers
// use its actual state and labels instead of transport placeholder values.
type PolicySnapshot struct {
	Settings Settings          `json:"settings"`
	State    kernel.StackState `json:"state"`
	Labels   []string          `json:"labels"`
}

// PolicySnapshots reads account-owned stack facts in the caller's transaction.
// A workflow holds the stack before evaluation to keep its snapshot stable.
type PolicySnapshots interface {
	PolicySnapshot(context.Context, kernel.AccountID, kernel.ULID) (PolicySnapshot, error)
}
