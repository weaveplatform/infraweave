package api

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// PolicyIdentity is live policy metadata, without its source or decisions.
// Historical receipts retain their own name/version snapshot after deletion.
type PolicyIdentity struct {
	ID      kernel.ULID
	Name    string
	Type    Type
	Engine  string
	Version int64
}

// PolicyIdentities is an account-qualified server projection. Absent IDs are
// deleted or belong to another account; reads never join another domain's SQL.
type PolicyIdentities interface {
	Identities(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID]PolicyIdentity, error)
}
