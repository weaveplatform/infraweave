package api

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// SelectionRequest identifies one account-owned policy target without input or
// source. The same type and target combinations are supported by Evaluator.
type SelectionRequest struct {
	Account kernel.AccountID
	Type    Type
	Target  Target
}

// Selection supplies an effective-presence hint for optional provider
// preparation. It opens a short transaction when the caller owns none, returns
// no bodies, invokes no runtime and writes no evaluation evidence. The eventual
// workflow must select again through Evaluator under its authoritative fences.
type Selection interface {
	HasPolicies(context.Context, SelectionRequest) (bool, error)
}
