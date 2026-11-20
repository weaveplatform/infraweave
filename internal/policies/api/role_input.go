package api

import "github.com/weaveplatform/infraweave/internal/kernel"

// RoleFacts retains an owner's current role description. ID is its immutable
// ULID; the published compatibility id and slug fields both use Slug.
type RoleFacts struct {
	ID   kernel.ULID `json:"id"`
	Slug string      `json:"slug"`
	Name string      `json:"name"`
}
