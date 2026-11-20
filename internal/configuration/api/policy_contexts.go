package api

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// PolicyContextReader reads the effective attached context public IDs in
// environment resolution order. It joins the caller's transaction and opens
// no variable or mounted-file values. A missing stack returns ErrNoStack.
type PolicyContextReader interface {
	PolicyContextSlugs(context.Context, kernel.AccountID, kernel.ULID) ([]string, error)
}
