package api

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// PolicyRole retains the immutable role ID and its current public description.
// Slug is the public role ID; it must not be replaced with the immutable ULID.
type PolicyRole struct {
	ID   kernel.ULID `json:"id"`
	Slug string      `json:"slug"`
	Name string      `json:"name"`
}

// StackRoleReader reads distinct roles directly bound to the account's stack,
// in immutable role-ID order, joining the caller's transaction. It grants no
// permission and does not infer roles from implicit space access.
type StackRoleReader interface {
	StackRoles(context.Context, kernel.AccountID, kernel.ULID) ([]PolicyRole, error)
}
