package pg_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/settings"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

func TestAccSettings(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "endframe")
	got, err := x.s.Settings(ctx, x.acct)
	want := settings.Defaults()
	want.LastAccount = true
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("an untouched account's settings = %+v, %v; want %+v", got, err, want)
	}
	response := "a friend"
	for name, err := range map[string]error{
		"email":   x.s.SetSecurityEmail(ctx, x.acct, "sec@example.com"),
		"hosts":   x.s.SetRedirectHosts(ctx, x.acct, []string{"b.example.com", "*.a.example.com"}),
		"survey":  x.s.SetSurvey(ctx, x.acct, settings.Survey{Completed: true, Response: &response}),
		"managed": x.s.SetNonAdminManagers(ctx, x.acct, true),
	} {
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := x.s.SetPersonalAPIKeys(ctx, x.acct, false, false); err != nil {
		t.Fatal(err)
	}
	for i, want := range []bool{true, false, true} {
		if on, err := x.s.ToggleNonHumanKeyManagers(ctx, x.acct); err != nil || on != want {
			t.Fatalf("toggle %d = %t, %v", i, on, err)
		}
		if on, err := x.s.NonHumanKeyManagers(ctx, x.acct); err != nil || on != want {
			t.Fatalf("NonHumanKeyManagers after toggle %d = %t, %v", i, on, err)
		}
	}
	reason := "moving"
	at := time.Now().UTC().Truncate(time.Microsecond)
	if marked, err := x.s.ToggleDeletionMark(ctx, x.acct,
		settings.Mark{At: at, Reason: &reason}); err != nil || !marked {
		t.Fatalf("mark = %t, %v", marked, err)
	}
	got, err = x.s.Settings(ctx, x.acct)
	want = settings.Settings{
		NonHumanKeyManagers: true, NonAdminKeyManagers: true,
		SecurityEmail: "sec@example.com",
		RedirectHosts: []string{"b.example.com", "*.a.example.com"},
		Survey:        settings.Survey{Completed: true, Response: &response},
		Mark:          &settings.Mark{At: at, Reason: &reason},
		LastAccount:   true,
	}
	// The database answers the mark's time in its own zone.
	if err != nil || got.Mark == nil || !got.Mark.At.Equal(at) {
		t.Fatalf("settings = %+v, %v; want the mark at %v", got, err, at)
	}
	got.Mark.At = at
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("settings = %+v; want %+v", got, want)
	}
	if got.Deletable() {
		t.Error("the organization's last account is deletable")
	}
	// Clearing the mark clears its reason, which the table's check needs.
	if marked, err := x.s.ToggleDeletionMark(ctx, x.acct,
		settings.Mark{At: at, Reason: &reason}); err != nil || marked {
		t.Fatalf("unmark = %t, %v", marked, err)
	}
	if got, err := x.s.Settings(ctx, x.acct); err != nil || got.Mark != nil {
		t.Errorf("the mark after clearing it = %+v, %v", got.Mark, err)
	}
	none := kernel.NewAccountID()
	if on, err := x.s.NonHumanKeyManagers(ctx, none); err != nil || on {
		t.Errorf("an account with no settings row lets keys manage keys: %t, %v", on, err)
	}
	for name, err := range map[string]error{
		"settings": func() error { _, err := x.s.Settings(ctx, none); return err }(),
		"email":    x.s.SetSecurityEmail(ctx, none, ""),
		"survey":   x.s.SetSurvey(ctx, none, settings.Survey{}),
		"toggle":   func() error { _, err := x.s.ToggleNonHumanKeyManagers(ctx, none); return err }(),
		"mark": func() error {
			_, err := x.s.ToggleDeletionMark(ctx, none, settings.Mark{At: at})
			return err
		}(),
		"personal": func() error {
			_, err := x.s.SetPersonalAPIKeys(ctx, none, true, false)
			return err
		}(),
	} {
		if !errors.Is(err, port.ErrNotFound) {
			t.Errorf("%s of an unknown account = %v", name, err)
		}
	}
}

// TestAccDisablePersonalKeys checks that refusing personal keys with
// disableExistingKeys disables the account's personal keys and revokes
// their sessions, and leaves every other key alone.
func TestAccDisablePersonalKeys(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "bachmanity")
	mine := kernel.NewULID()
	h, _ := apikey.Hash(apikey.NewSecret())
	if _, err := db.Exec(ctx, `INSERT INTO account_users (account_id, organization_id, member_id)
		VALUES ($1, $2, $3)`, x.acct, x.org, x.member); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO api_keys (account_id, id, name, type, secret_hash,
		creator, is_personal, owner_user_id) VALUES ($1, $2, 'mine', 'SECRET', $3, 'alice', true, $4)`,
		x.acct, mine, h, x.member); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(time.Hour)
	personal, shared := x.keySession(at), x.keySession(at)
	personal.ActorID = mine
	if err := x.s.CreateSession(ctx, personal); err != nil {
		t.Fatal(err)
	}
	if err := x.s.CreateSession(ctx, shared); err != nil {
		t.Fatal(err)
	}
	revoked, err := x.s.SetPersonalAPIKeys(ctx, x.acct, false, true)
	if err != nil || len(revoked) != 1 || revoked[0] != personal.ID {
		t.Fatalf("revoked = %v, %v; want the personal key's session", revoked, err)
	}
	k, err := x.s.AccountAPIKey(ctx, x.acct, mine)
	if err != nil || k.DisabledAt.IsZero() || k.DisabledReason != "ACCOUNT" {
		t.Errorf("the personal key = %+v, %v", k, err)
	}
	if k, err := x.s.AccountAPIKey(ctx, x.acct, x.key); err != nil || !k.DisabledAt.IsZero() {
		t.Errorf("the shared key = %+v, %v", k, err)
	}
	if ss, err := x.s.Session(ctx, shared.ID); err != nil || !ss.RevokedAt.IsZero() {
		t.Errorf("the shared key's session = %+v, %v", ss, err)
	}
}

// accountFeatures is the features domain's rows as an account delete
// sees them: it records each delete and whether it ran in a transaction.
type accountFeatures struct {
	entitled
	deleted []kernel.AccountID
	inTx    bool
	err     error
}

func (f *accountFeatures) DeleteAccount(ctx context.Context, acct kernel.AccountID) error {
	f.deleted = append(f.deleted, acct)
	f.inTx = tx.In(ctx)
	return f.err
}

// second adds another account to x's organization and returns it.
func second(t *testing.T, x seeded, slug string) kernel.AccountID {
	t.Helper()
	acct := kernel.NewAccountID()
	if _, err := x.db.Exec(t.Context(), `INSERT INTO accounts (organization_id, id, slug, name)
		VALUES ($1, $2, $3, $3)`, x.org, acct, slug); err != nil {
		t.Fatal(err)
	}
	return acct
}

func deleted(x seeded, acct kernel.AccountID) kernel.OrganizationAuditEntry {
	return kernel.OrganizationAuditEntry{
		OrganizationID:   x.org,
		ActorKind:        kernel.PrincipalUser,
		ActorID:          x.member.String(),
		Action:           settings.ActionDelete,
		EventType:        "DELETE",
		AffectedResource: kernel.AuditResource{Type: settings.ResourceAccount, ID: acct.String()},
	}
}

func TestAccDeleteAccount(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "aviato")
	f := &accountFeatures{}
	s := pg.New(db).WithFeatures(f)
	other := second(t, x, "aviato-two")
	if _, err := db.Exec(ctx, `INSERT INTO account_users (account_id, organization_id, member_id)
		VALUES ($1, $3, $4), ($2, $3, $4)`, x.acct, other, x.org, x.member); err != nil {
		t.Fatal(err)
	}
	// A CLI session of the other account came from a browser session of
	// this one, which the delete takes with it.
	at := time.Now().Add(time.Hour)
	web, key := x.webSession(at), x.keySession(at)
	cli := x.webSession(at)
	cli.AccountID, cli.Channel, cli.ParentID = other, session.ChannelTerraformCLI, web.ID
	for _, err := range []error{
		s.CreateSession(ctx, web), s.CreateSession(ctx, key),
		s.CreateSession(ctx, cli), s.SetSecurityEmail(ctx, x.acct, "sec@example.com"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DeleteAccount(
		ctx,
		x.acct,
		deleted(x, x.acct),
	); !errors.Is(
		err,
		settings.ErrInvalidState,
	) {
		t.Fatalf("deleting an unmarked account = %v", err)
	}
	if _, err := s.ToggleDeletionMark(ctx, x.acct, settings.Mark{At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Settings(ctx, x.acct); err != nil || !got.Deletable() {
		t.Fatalf("a marked account of two: %+v, %v", got, err)
	}
	f.err = errors.New("features down")
	if _, err := s.DeleteAccount(ctx, x.acct, deleted(x, x.acct)); !errors.Is(err, f.err) {
		t.Fatalf("a failed features delete = %v", err)
	}
	if _, err := s.Account(ctx, x.acct); err != nil {
		t.Errorf("the account after a failed features delete = %v", err)
	}
	f.err = nil
	revoked, err := s.DeleteAccount(ctx, x.acct, deleted(x, x.acct))
	if err != nil || len(revoked) != 2 {
		t.Fatalf("delete = %v, %v; want the web and key sessions revoked", revoked, err)
	}
	if len(f.deleted) != 2 || f.deleted[1] != x.acct || !f.inTx {
		t.Errorf("features deletes = %v, in a transaction %t", f.deleted, f.inTx)
	}
	var rows, entries int
	if err := db.QueryRow(
		ctx,
		`SELECT
		(SELECT count(*) FROM accounts WHERE id = $1) + (SELECT count(*) FROM api_keys WHERE account_id = $1)
		+ (SELECT count(*) FROM sessions WHERE account_id = $1)
		+ (SELECT count(*) FROM account_users WHERE account_id = $1)
		+ (SELECT count(*) FROM account_settings WHERE account_id = $1),
		(SELECT count(*) FROM organization_audit_trail_entries
			WHERE organization_id = $2 AND action = 'account.delete' AND affected_id = $3)`,
		x.acct,
		x.org,
		x.acct.String(),
	).Scan(&rows, &entries); err != nil || rows != 0 || entries != 1 {
		t.Errorf("rows left %d, trail entries %d, %v", rows, entries, err)
	}
	if ss, err := s.Session(ctx, cli.ID); err != nil || !ss.ParentID.IsZero() {
		t.Errorf("the other account's CLI session = %+v, %v", ss, err)
	}
	if _, err := s.ToggleDeletionMark(ctx, other, settings.Mark{At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteAccount(
		ctx,
		other,
		deleted(x, other),
	); !errors.Is(
		err,
		settings.ErrInvalidState,
	) {
		t.Errorf("deleting the organization's last account = %v", err)
	}
	if _, err := s.DeleteAccount(
		ctx,
		x.acct,
		deleted(x, x.acct),
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("deleting a deleted account = %v", err)
	}
}

// TestAccDeleteAccountRace deletes both marked accounts of an
// organization at once: the organization's lock lets exactly one go.
func TestAccDeleteAccountRace(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "nucleus")
	accts := []kernel.AccountID{x.acct, second(t, x, "nucleus-two")}
	for _, a := range accts {
		if _, err := x.s.ToggleDeletionMark(ctx, a, settings.Mark{At: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	errs := make([]error, len(accts))
	var wg sync.WaitGroup
	for i, a := range accts {
		wg.Go(func() { _, errs[i] = x.s.DeleteAccount(ctx, a, deleted(x, a)) })
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, settings.ErrInvalidState):
			t.Errorf("a racing delete = %v", err)
		}
	}
	if ok != 1 {
		t.Errorf("%d deletions succeeded, want 1: %v", ok, errs)
	}
}

func TestAccRequestPersonalAPIKeys(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "raviga-two")
	if err := x.s.RequestPersonalAPIKeys(ctx, kernel.AuditEntry{
		AccountID:        x.acct.ULID,
		Actor:            kernel.AuditActor{Username: "alice"},
		Action:           settings.ActionRequestPersonalAPIKeys,
		EventType:        "NONE",
		AffectedResource: kernel.AuditResource{Type: settings.ResourceAccount, ID: x.acct.String()},
	}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM audit_trail_entries
		WHERE account_id = $1 AND action = $2`, x.acct, settings.ActionRequestPersonalAPIKeys).
		Scan(&n); err != nil || n != 1 {
		t.Errorf("trail entries %d, %v", n, err)
	}
	if err := x.s.RequestPersonalAPIKeys(ctx, kernel.AuditEntry{}); err == nil {
		t.Error("an incomplete entry was recorded")
	}
}

func TestAccSettingsUnavailable(t *testing.T) {
	p := pool(t)
	x := seed(t, p, "closed-settings")
	p.Close()
	ctx := context.Background()
	for name, err := range map[string]error{
		"settings": func() error { _, err := x.s.Settings(ctx, x.acct); return err }(),
		"email":    x.s.SetSecurityEmail(ctx, x.acct, ""),
		"toggle":   func() error { _, err := x.s.ToggleNonHumanKeyManagers(ctx, x.acct); return err }(),
		"managers": func() error { _, err := x.s.NonHumanKeyManagers(ctx, x.acct); return err }(),
		"request":  x.s.RequestPersonalAPIKeys(ctx, kernel.AuditEntry{}),
		"mark": func() error {
			_, err := x.s.ToggleDeletionMark(ctx, x.acct, settings.Mark{At: time.Now()})
			return err
		}(),
		"personal": func() error {
			_, err := x.s.SetPersonalAPIKeys(ctx, x.acct, false, true)
			return err
		}(),
		"delete": func() error {
			_, err := x.s.DeleteAccount(ctx, x.acct, deleted(x, x.acct))
			return err
		}(),
	} {
		if err == nil || errors.Is(err, port.ErrNotFound) {
			t.Errorf("%s on a closed pool = %v", name, err)
		}
	}
}
