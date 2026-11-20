package api

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// PolicySpace is current credential-free space metadata. Slug is the public
// policy ID; ID retains the immutable owner key used by domain references.
type PolicySpace struct {
	ID     kernel.ULID `json:"id"`
	Slug   string      `json:"slug"`
	Name   string      `json:"name"`
	Labels []string    `json:"labels"`
}

// PolicySpaceReader reads an account-owned space in the caller's transaction.
// Missing and foreign-account spaces return ErrNoSpace.
type PolicySpaceReader interface {
	PolicySpace(context.Context, kernel.AccountID, kernel.ULID) (PolicySpace, error)
}
