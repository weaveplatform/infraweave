package api

import (
	"context"
	"errors"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ErrNotFound reports a stack the account lacks.
var ErrNotFound = errors.New("stacks: not found")

// Stack is a stack as other domains read it: its ULID, which every other
// domain holds, its slug, which a reslug changes, and the space it lives
// in.
type Stack struct {
	ID      kernel.ULID
	Account kernel.AccountID
	Slug    string
	Space   kernel.ULID
}

// Stacks is what other domains call of the stacks domain. Each method
// joins the transaction its context carries.
type Stacks interface {
	// Get reads acct's stack id; ErrNotFound when acct lacks it.
	Get(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (Stack, error)
}
