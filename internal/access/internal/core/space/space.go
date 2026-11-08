package space

import (
	"fmt"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
)

// Fields are what a space search filters and orders by: id, the slug;
// label, any of its labels; parentSpace, its parent's slug; and space, its
// place in the tree, which keeps a node's spaces and, asked, those below
// it. A search names no order reads by id.
var Fields = []search.Field{
	{Name: "id", Type: search.String, Orderable: true},
	{Name: "name", Type: search.String, Orderable: true},
	{Name: "description", Type: search.String},
	{Name: "label", Type: search.String},
	{Name: "inheritEntities", Type: search.Boolean},
	{Name: "parentSpace", Type: search.String},
	{Name: "space", Type: search.Hierarchy},
}

// LegacySlug is the slug of the space every account starts with below its
// top space. It has no suffix, so imported data and OIDC subjects that
// name legacy find it.
const LegacySlug = "legacy"

// Space is a node of an account's tree. Slug is its ID on the wire and
// never changes. ParentID is zero only for the top space.
type Space struct {
	AccountID       kernel.AccountID
	ID              kernel.ULID
	Slug            string
	Name            string
	Description     string
	Labels          []string
	InheritEntities bool
	ParentID        kernel.ULID
	CreatedAt       time.Time
}

// Top reports whether s is its account's top space.
func (s Space) Top() bool { return s.ParentID.IsZero() }

// Seed is the tree a new account starts with: its top space, whose ID is
// the account's and whose slug and name are the account's, and legacy
// beneath it.
func Seed(a account.Account) (top, legacy Space) {
	top = Space{
		AccountID: a.ID, ID: a.ID.ULID, Slug: a.Slug.String(), Name: a.Name,
		CreatedAt: a.CreatedAt,
	}
	legacy = Space{
		AccountID: a.ID, ID: kernel.NewULID(), Slug: LegacySlug, Name: LegacySlug,
		ParentID: top.ID, CreatedAt: a.CreatedAt,
	}
	return top, legacy
}

// SlugOf is the slug of a space created with name: the name lowercased,
// each run of other characters a hyphen, then a hyphen and the space's
// ULID, the shape report 12 records for Spacelift's space IDs. A name
// with no letter or digit gives the ULID alone.
func SlugOf(name string, id kernel.ULID) string {
	var b strings.Builder
	hyphen := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			if hyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			hyphen = false
			b.WriteRune(r)
		default:
			hyphen = true
		}
	}
	if b.Len() == 0 {
		return id.String()
	}
	return b.String() + "-" + id.String()
}

// Refusal is a tree change refused, naming each reason, which the
// Spaces operations answer as 422 validation-failed.
type Refusal struct{ Reasons []string }

func (r *Refusal) Error() string {
	return "space change refused: " + strings.Join(r.Reasons, "; ")
}

// refuse is a Refusal of one reason.
func refuse(format string, args ...any) *Refusal {
	return &Refusal{Reasons: []string{fmt.Sprintf(format, args...)}}
}
