package pg_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/space"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// TestAccFederationSubject checks that a federation token's subject reads
// the account's slug, its template, empty until set, and a space's
// ancestry from the top space down, and that a space of another account
// or none is not found.
func TestAccFederationSubject(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "pied-piper")
	other := seed(t, db, "hooli")
	tree := treeOf(x.s)
	prod, err := tree.Create(ctx, x.acct, "legacy", space.Space{Name: "Prod"})
	if err != nil {
		t.Fatal(err)
	}
	eu, err := tree.Create(ctx, x.acct, prod.Slug, space.Space{Name: "EU"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := x.s.FederationSubject(ctx, x.acct, eu.ID)
	if err != nil || got.Account != "pied-piper" || got.Template != "" ||
		!slices.Equal(got.Spaces, []string{"pied-piper", "legacy", prod.Slug, eu.Slug}) {
		t.Fatalf("subject = %+v, %v", got, err)
	}
	if err := x.s.SetOIDCSubjectTemplate(ctx, x.acct, "stack|{callerId}"); err != nil {
		t.Fatal(err)
	}
	got, err = x.s.FederationSubject(ctx, x.acct, x.acct.ULID)
	if err != nil || got.Template != "stack|{callerId}" || !slices.Equal(got.Spaces, []string{"pied-piper"}) {
		t.Fatalf("top space's subject = %+v, %v", got, err)
	}
	for name, id := range map[string]struct {
		acct  kernel.AccountID
		space kernel.ULID
	}{
		"another account's space": {other.acct, eu.ID},
		"no space":                {x.acct, kernel.NewULID()},
		"no account":              {kernel.NewAccountID(), eu.ID},
	} {
		if _, err := x.s.FederationSubject(ctx, id.acct, id.space); !errors.Is(err, port.ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
