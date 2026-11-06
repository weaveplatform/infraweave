package account

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestUnitNew(t *testing.T) {
	org := kernel.NewOrganizationID()
	a, err := New(org, "", "acme", "Acme")
	if err != nil || a.ID.IsZero() || a.OrganizationID != org || a.Slug != "acme" || a.Claimed() {
		t.Errorf("New = %+v, %v", a, err)
	}
	a.ClaimedAt = time.Now()
	if !a.Claimed() {
		t.Error("an account with ClaimedAt is not claimed")
	}
	// The prefix names the fields of an account created with its
	// organization.
	_, err = New(org, "firstAccount.", "root", " ")
	if !errors.Is(err, organization.ErrInvalid) ||
		!strings.Contains(err.Error(), "firstAccount.slug: ") ||
		!strings.Contains(err.Error(), "firstAccount.name: ") {
		t.Errorf("New(root, blank) = %v", err)
	}
}
