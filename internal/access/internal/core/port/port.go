package port

import (
	"context"
	"errors"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ErrNotFound reports a row that does not exist.
var ErrNotFound = errors.New("not found")

// ErrSlugTaken reports a slug another organization, or another account of
// any organization, already holds; the error names which.
var ErrSlugTaken = errors.New("slug taken")

// Tenancy stores organizations and accounts.
type Tenancy interface {
	// CreateOrganization records org and its first account in one
	// transaction, or neither.
	CreateOrganization(
		ctx context.Context,
		org organization.Organization,
		first account.Account,
	) error
	Organization(ctx context.Context, id kernel.OrganizationID) (organization.Organization, error)
	Account(ctx context.Context, id kernel.AccountID) (account.Account, error)
}
