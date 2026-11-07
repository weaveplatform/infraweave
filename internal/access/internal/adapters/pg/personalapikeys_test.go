package pg_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// admit admits member to x's account with the groups its latest sign-in
// reported.
func admit(t *testing.T, x seeded, member kernel.MemberID, groups ...string) {
	t.Helper()
	if _, err := x.db.Exec(t.Context(), `INSERT INTO account_users
		(account_id, organization_id, member_id, latest_groups) VALUES ($1, $2, $3, $4)`,
		x.acct, x.org, member, append([]string{}, groups...)); err != nil {
		t.Fatal(err)
	}
}

// personal is a new personal key of x's account owned by owner.
func personal(x seeded, owner kernel.MemberID) apikey.Key {
	h, _ := apikey.Hash(apikey.NewSecret())
	return apikey.Key{
		OrganizationID: x.org, AccountID: x.acct, ID: kernel.NewULID(), Name: "laptop",
		Type: apikey.TypeSecret, SecretHash: h, Teams: []string{}, Owner: owner,
		Creator: "alice", CreatedAt: time.Now().UTC().Truncate(time.Second),
	}
}

func TestAccPersonalAPIKeys(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "personal")
	admit(t, x, x.member, "dev", "ops")

	k, err := x.s.CreatePersonalAPIKey(ctx, personal(x, x.member))
	if err != nil || k.OwnerLogin != "alice" {
		t.Fatalf("create = %+v, %v", k, err)
	}
	read, err := x.s.APIKey(ctx, k.ID)
	if err != nil || !read.Personal() || read.Owner != x.member || read.OwnerLogin != "alice" ||
		read.Creator != "alice" || len(read.Teams) != 0 {
		t.Fatalf("read = %+v, %v", read, err)
	}
	if ci, err := x.s.APIKey(ctx, x.key); err != nil || ci.Personal() || ci.OwnerLogin != "" {
		t.Errorf("the account's key = %+v, %v", ci, err)
	}
	list, err := x.s.AccountPersonalAPIKeys(ctx, x.acct, kernel.ULID{}, 10)
	if err != nil || len(list) != 1 || list[0].ID != k.ID {
		t.Errorf("personal keys = %+v, %v", list, err)
	}
	if all, err := x.s.AccountAPIKeys(ctx, x.acct, kernel.ULID{}, 10); err != nil ||
		len(all) != 2 {
		t.Errorf("all keys = %d, %v", len(all), err)
	}
	if n, err := x.s.CountPersonalAPIKeys(ctx, x.acct); err != nil || n != 1 {
		t.Errorf("count = %d, %v", n, err)
	}

	o, err := x.s.KeyOwner(ctx, x.acct, x.member)
	if err != nil || o.Login != "alice" || o.Name != "Alice" ||
		!slices.Equal(o.Teams, []string{"dev", "ops"}) {
		t.Errorf("owner = %+v, %v", o, err)
	}
	bob := addMember(t, x, "bob", "active")
	if _, err := x.s.KeyOwner(ctx, x.acct, bob); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("an owner the account does not admit: %v", err)
	}

	if _, _, err := x.s.DisablePersonalAPIKey(ctx, x.acct, x.key,
		apikey.DisabledByAdmin); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("disable the account's key as personal: %v", err)
	}
	off, _, err := x.s.DisablePersonalAPIKey(ctx, x.acct, k.ID, apikey.DisabledByAdmin)
	if err != nil || off.DisabledAt.IsZero() || off.DisabledReason != apikey.DisabledByAdmin {
		t.Errorf("disable = %+v, %v", off, err)
	}
	if n, _ := x.s.CountPersonalAPIKeys(ctx, x.acct); n != 0 {
		t.Errorf("count after disabling = %d", n)
	}
}

func TestAccPersonalAPIKeyRefusals(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "personal-refused")
	admit(t, x, x.member)
	dave := addMember(t, x, "dave", "deactivated")
	admit(t, x, dave)

	for name, owner := range map[string]kernel.MemberID{
		"a member the account does not admit": addMember(t, x, "bob", "active"),
		"a deactivated member":                dave,
		"no member":                           kernel.NewMemberID(),
	} {
		if _, err := x.s.CreatePersonalAPIKey(ctx, personal(x, owner)); !errors.Is(
			err, apikey.ErrNoOwner) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := x.s.SetPersonalAPIKeys(ctx, x.acct, false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.CreatePersonalAPIKey(ctx, personal(x, x.member)); !errors.Is(
		err, apikey.ErrPersonalOff) {
		t.Errorf("with personal keys off: %v", err)
	}
	if _, err := x.s.KeyOwner(ctx, x.acct, dave); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("a deactivated owner: %v", err)
	}
}
