package api

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// PoolMetadata is credential-free metadata of an account's private pool. A
// missing materialized pool does not establish facts about a public pool.
type PoolMetadata struct {
	ID     kernel.ULID `json:"id"`
	Space  kernel.ULID `json:"space"`
	Name   string      `json:"name"`
	Labels []string    `json:"labels"`
	Public bool        `json:"public"`
}

// PoolMetadataReader reads a private pool in the caller's transaction. Missing
// and foreign-account pools return ErrNoPool; operational errors remain errors.
type PoolMetadataReader interface {
	PoolMetadata(context.Context, kernel.AccountID, kernel.ULID) (PoolMetadata, error)
}
