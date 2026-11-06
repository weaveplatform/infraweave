package account

import (
	"errors"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Account is a business entity within an organization and the data
// boundary (decision 0007). Its slug is unique across the installation and
// never changes; its name may. An account starts unclaimed until an
// organization admin names its first account admin, and stays claimed.
type Account struct {
	OrganizationID kernel.OrganizationID
	ID             kernel.AccountID
	Slug           kernel.Slug
	Name           string
	ClaimedAt      time.Time
	CreatedAt      time.Time
}

// Claimed reports whether an organization admin has named the account's
// first account admin.
func (a Account) Claimed() bool { return !a.ClaimedAt.IsZero() }

// New checks a new account of org: its slug and name under field names
// prefixed by prefix, so an account created with its organization names
// firstAccount.slug rather than slug. It gives the account an ID.
func New(org kernel.OrganizationID, prefix, slug, name string) (Account, error) {
	s, serr := organization.ParseSlug(prefix+"slug", slug)
	nerr := organization.CheckName(prefix+"name", name)
	if err := errors.Join(serr, nerr); err != nil {
		return Account{}, err
	}
	return Account{OrganizationID: org, ID: kernel.NewAccountID(), Slug: s, Name: name}, nil
}
