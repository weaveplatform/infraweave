package pg_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// TestAccAPIKeyStore creates a secret and an OIDC key, reads, lists and
// updates them, and resets, disables and deletes the secret key, checking
// that each revoking write revokes and announces the key's sessions and
// no other's, and that another account reaches none of them.
func TestAccAPIKeyStore(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "pied-piper")
	y := seed(t, db, "endframe")
	l := listen(t, db)
	now := time.Now().UTC().Truncate(time.Microsecond)

	h, _ := apikey.Hash(apikey.NewSecret())
	secret := apikey.Key{
		AccountID: x.acct, ID: kernel.NewULID(), Name: "deploy", Description: "CI",
		Type: apikey.TypeSecret, SecretHash: h, Teams: []string{"ops"}, Machine: true,
		Creator: "alice", ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	}
	oidc := apikey.Key{
		AccountID: x.acct, ID: kernel.NewULID(), Name: "gha", Type: apikey.TypeOIDC,
		OIDC: apikey.OIDC{
			Issuer: "https://idp.test", ClientID: "infraweave", SubjectExpression: "repo:.*",
			ClaimMapping: []apikey.Mapping{{Name: "roles", Value: apikey.TeamsClaim}},
		},
		Creator: "alice", CreatedAt: now,
	}
	for _, k := range []apikey.Key{secret, oidc} {
		if err := x.s.CreateAPIKey(ctx, k); err != nil {
			t.Fatal(err)
		}
	}
	got, err := x.s.AccountAPIKey(ctx, x.acct, secret.ID)
	if err != nil || got.OrganizationID != x.org || got.Name != "deploy" ||
		got.Description != "CI" || !got.Machine || got.Creator != "alice" ||
		!got.ExpiresAt.Equal(secret.ExpiresAt) || !got.CreatedAt.Equal(now) ||
		!got.Matches("") == got.Matches("") ||
		len(got.OIDC.ClaimMapping) != 0 ||
		!slices.Equal(got.Teams, []string{"ops"}) {
		t.Errorf("AccountAPIKey = %+v, %v", got, err)
	}
	if got, err := x.s.AccountAPIKey(ctx, x.acct, oidc.ID); err != nil ||
		got.OIDC.Issuer != "https://idp.test" || got.OIDC.SubjectExpression != "repo:.*" ||
		!slices.Equal(got.OIDC.ClaimMapping, oidc.OIDC.ClaimMapping) || got.SecretHash != nil {
		t.Errorf("the OIDC key = %+v, %v", got, err)
	}
	if _, err := y.s.AccountAPIKey(ctx, y.acct, secret.ID); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("another account's read: %v", err)
	}

	// x's list holds its seeded key and the two new ones, page by page.
	page1, err := x.s.AccountAPIKeys(ctx, x.acct, kernel.ULID{}, 2)
	if err != nil || len(page1) != 2 {
		t.Fatalf("first page = %v, %v", page1, err)
	}
	page2, err := x.s.AccountAPIKeys(ctx, x.acct, page1[1].ID, 2)
	if err != nil || len(page2) != 1 || page2[0].ID.String() <= page1[1].ID.String() {
		t.Errorf("second page = %v, %v", page2, err)
	}
	if theirs, _ := y.s.AccountAPIKeys(
		ctx,
		y.acct,
		kernel.ULID{},
		10,
	); len(theirs) != 1 ||
		theirs[0].ID != y.key {
		t.Errorf("another account lists %v", theirs)
	}

	name, teams := "renamed", []string{}
	m := []apikey.Mapping{{Name: "cognito:groups", Value: apikey.TeamsClaim}}
	if got, err := x.s.UpdateAPIKey(ctx, x.acct, oidc.ID, kernel.Expect{}, apikey.Change{
		Name: &name, Teams: &teams, ClaimMapping: &m,
	}); err != nil || got.Name != "renamed" || len(got.Teams) != 0 ||
		!slices.Equal(got.OIDC.ClaimMapping, m) {
		t.Errorf("UpdateAPIKey = %+v, %v", got, err)
	}
	if got, err := x.s.UpdateAPIKey(
		ctx,
		x.acct,
		oidc.ID,
		kernel.Expect{},
		apikey.Change{},
	); err != nil ||
		got.Name != "renamed" ||
		!slices.Equal(got.OIDC.ClaimMapping, m) {
		t.Errorf("an empty update changed %+v, %v", got, err)
	}
	if _, err := y.s.UpdateAPIKey(
		ctx,
		y.acct,
		oidc.ID,
		kernel.Expect{},
		apikey.Change{Name: &name},
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("another account's update: %v", err)
	}

	// The secret key's sessions are revoked by each revoking write; the
	// seeded key's session is not.
	later := now.Add(time.Hour)
	sessionOf := func(key kernel.ULID) kernel.ULID {
		ss := x.keySession(later)
		ss.ActorID = key
		if err := x.s.CreateSession(ctx, ss); err != nil {
			t.Fatal(err)
		}
		return ss.ID
	}
	bystander := sessionOf(x.key)
	h2, _ := apikey.Hash(apikey.NewSecret())
	s1 := sessionOf(secret.ID)
	reset, ids, err := x.s.ResetAPIKey(ctx, x.acct, secret.ID, h2)
	if err != nil || !slices.Equal(reset.SecretHash, h2) || !slices.Equal(ids, []kernel.ULID{s1}) {
		t.Errorf("ResetAPIKey = %+v, %v, %v", reset, ids, err)
	}
	if got := messages(t, l); !slices.Equal(got, keys(s1)) {
		t.Errorf("reset sent %v, want %v", got, keys(s1))
	}

	s2 := sessionOf(secret.ID)
	off, ids, err := x.s.DisableAPIKey(ctx, x.acct, secret.ID, apikey.DisabledByAdmin)
	if err != nil || off.DisabledAt.IsZero() || off.DisabledReason != apikey.DisabledByAdmin ||
		!slices.Equal(ids, []kernel.ULID{s2}) {
		t.Errorf("DisableAPIKey = %+v, %v, %v", off, ids, err)
	}
	again, _, err := x.s.DisableAPIKey(ctx, x.acct, secret.ID, "SCHEME")
	if err != nil || !again.DisabledAt.Equal(off.DisabledAt) ||
		again.DisabledReason != apikey.DisabledByAdmin {
		t.Errorf("disabling twice = %+v, %v", again, err)
	}

	s3 := sessionOf(secret.ID)
	gone, ids, err := x.s.DeleteAPIKey(ctx, x.acct, secret.ID, kernel.Expect{})
	if err != nil || gone.DeletedAt.IsZero() || !slices.Equal(ids, []kernel.ULID{s3}) {
		t.Errorf("DeleteAPIKey = %+v, %v, %v", gone, ids, err)
	}
	messages(t, l)
	if ss, err := x.s.Session(ctx, bystander); err != nil || !ss.RevokedAt.IsZero() {
		t.Errorf("another key's session = %+v, %v", ss, err)
	}
	for name, err := range map[string]error{
		"read": func() error { _, err := x.s.AccountAPIKey(ctx, x.acct, secret.ID); return err }(),
		"update": func() error {
			_, err := x.s.UpdateAPIKey(ctx, x.acct, secret.ID, kernel.Expect{}, apikey.Change{})
			return err
		}(),
		"reset":  func() error { _, _, err := x.s.ResetAPIKey(ctx, x.acct, secret.ID, h2); return err }(),
		"delete": func() error { _, _, err := x.s.DeleteAPIKey(ctx, x.acct, secret.ID, kernel.Expect{}); return err }(),
		"theirs": func() error { _, _, err := y.s.DeleteAPIKey(ctx, y.acct, oidc.ID, kernel.Expect{}); return err }(),
	} {
		if !errors.Is(err, port.ErrNotFound) {
			t.Errorf("%s of a deleted or foreign key: %v", name, err)
		}
	}
	if k, err := x.s.APIKey(ctx, secret.ID); err != nil || k.DeletedAt.IsZero() {
		t.Errorf("the exchange's read of a deleted key = %+v, %v", k, err)
	}
	if list, _ := x.s.AccountAPIKeys(ctx, x.acct, kernel.ULID{}, 10); len(list) != 2 {
		t.Errorf("the list after a delete has %d keys, want 2", len(list))
	}

	if on, err := x.s.NonAdminKeyManagers(ctx, x.acct); err != nil || on {
		t.Errorf("non-admin managers before the setting = %t, %v", on, err)
	}
	if err := x.s.SetNonAdminManagers(ctx, x.acct, true); err != nil {
		t.Fatal(err)
	}
	if on, err := x.s.NonAdminKeyManagers(ctx, x.acct); err != nil || !on {
		t.Errorf("non-admin managers = %t, %v", on, err)
	}
	if _, err := x.s.NonAdminKeyManagers(
		ctx,
		kernel.NewAccountID(),
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("non-admin managers of no account: %v", err)
	}
	if err := x.s.SetNonAdminManagers(
		ctx,
		kernel.NewAccountID(),
		true,
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("a missing account: %v", err)
	}
}

// TestAccAPIKeyStoreUnavailable checks that every write and read fails,
// and not as not found, when PostgreSQL cannot be reached.
func TestAccAPIKeyStoreUnavailable(t *testing.T) {
	p := pool(t)
	s := pg.New(p)
	p.Close()
	ctx := context.Background()
	id, acct := kernel.NewULID(), kernel.NewAccountID()
	for name, err := range map[string]error{
		"create":   s.CreateAPIKey(ctx, apikey.Key{ID: id, AccountID: acct, Type: apikey.TypeOIDC}),
		"list":     func() error { _, err := s.AccountAPIKeys(ctx, acct, id, 1); return err }(),
		"read":     func() error { _, err := s.AccountAPIKey(ctx, acct, id); return err }(),
		"update":   func() error { _, err := s.UpdateAPIKey(ctx, acct, id, kernel.Expect{}, apikey.Change{}); return err }(),
		"reset":    func() error { _, _, err := s.ResetAPIKey(ctx, acct, id, nil); return err }(),
		"managers": s.SetNonAdminManagers(ctx, acct, true),
	} {
		if err == nil || errors.Is(err, port.ErrNotFound) {
			t.Errorf("%s on a closed pool = %v", name, err)
		}
	}
}

// TestAccAPIKeyStoreAborted runs a list and a revoking write in a
// transaction a failed statement aborted.
func TestAccAPIKeyStoreAborted(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "bachmanity")
	errAborted := errors.New("aborted")
	err := tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
		_, _ = tx.Q(ctx, db).Exec(ctx, `SELECT 1/0`)
		for name, err := range map[string]error{
			"list":   func() error { _, err := x.s.AccountAPIKeys(ctx, x.acct, kernel.ULID{}, 1); return err }(),
			"delete": func() error { _, _, err := x.s.DeleteAPIKey(ctx, x.acct, x.key, kernel.Expect{}); return err }(),
		} {
			if err == nil || errors.Is(err, port.ErrNotFound) {
				t.Errorf("%s in an aborted transaction = %v", name, err)
			}
		}
		return errAborted
	})
	if !errors.Is(err, errAborted) {
		t.Errorf("tx.Do = %v", err)
	}
}
