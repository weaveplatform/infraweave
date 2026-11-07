package pg_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// TestAccBrowserSessions checks the cookie's lookup, the idle end's move,
// a CLI session's parent and EndSession, which ends any session by ID.
func TestAccBrowserSessions(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "gavin")
	l := listen(t, db)
	later := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)

	secret, hash := session.NewSecret()
	web := x.webSession(later)
	web.SecretHash = hash
	if err := x.s.CreateSession(ctx, web); err != nil {
		t.Fatal(err)
	}
	got, err := x.s.SessionBySecret(ctx, session.HashSecret(secret))
	if err != nil || got.ID != web.ID || string(got.SecretHash) != string(hash) ||
		!got.ParentID.IsZero() {
		t.Errorf("SessionBySecret = %+v, %v", got, err)
	}
	other, _ := session.NewSecret()
	if _, err := x.s.SessionBySecret(
		ctx,
		session.HashSecret(other),
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("an unknown secret = %v", err)
	}

	idle := later.Add(time.Hour)
	if err := x.s.TouchSession(ctx, web.ID, idle); err != nil {
		t.Fatal(err)
	}
	if got, err := x.s.Session(ctx, web.ID); err != nil || !got.IdleExpiresAt.Equal(idle) {
		t.Errorf("after a touch = %+v, %v", got, err)
	}

	cli := x.webSession(later)
	cli.Channel, cli.ParentID, cli.IdleExpiresAt = session.ChannelCLI, web.ID, time.Time{}
	if err := x.s.CreateSession(ctx, cli); err != nil {
		t.Fatal(err)
	}
	if got, err := x.s.Session(
		ctx,
		cli.ID,
	); err != nil || got.ParentID != web.ID ||
		got.SecretHash != nil {
		t.Errorf("CLI session = %+v, %v", got, err)
	}

	if got, err := x.s.EndSession(ctx, web.ID); err != nil || got.RevokedAt.IsZero() {
		t.Fatalf("EndSession = %+v, %v", got, err)
	}
	if got := messages(t, l); !slices.Equal(got, keys(web.ID)) {
		t.Errorf("ending sent %v, want %v", got, keys(web.ID))
	}
	if got, err := x.s.Session(ctx, cli.ID); err != nil || !got.Live(time.Now()) {
		t.Errorf("the CLI session after its parent's end = %+v, %v", got, err)
	}
	// A touch leaves an ended session as it is.
	if err := x.s.TouchSession(ctx, web.ID, idle.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, _ := x.s.Session(ctx, web.ID); !got.IdleExpiresAt.Equal(idle) {
		t.Errorf("a touch moved an ended session's idle end to %s", got.IdleExpiresAt)
	}
	if _, err := x.s.EndSession(ctx, kernel.NewULID()); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("ending an unknown session = %v", err)
	}
}

// TestAccBreakGlassStore checks the credential's read, a sign-in's session
// and trail entry in one transaction, and a refusal's entry alone.
func TestAccBreakGlassStore(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	o, a := tenant(t, "erlich", "erlich")
	cred := credential(t, "admin")
	if err := s.CreateOrganization(ctx, o, a, &cred, created(o)); err != nil {
		t.Fatal(err)
	}
	bare, bareAcct := tenant(t, "jared", "jared")
	if err := s.CreateOrganization(ctx, bare, bareAcct, nil, created(bare)); err != nil {
		t.Fatal(err)
	}

	got, c, err := s.BreakGlassCredential(ctx, o.Slug)
	if err != nil || got.ID != o.ID || c == nil || c.Login != "admin" || !c.Hash.Verify("pw") {
		t.Errorf("BreakGlassCredential = %+v, %+v, %v", got, c, err)
	}
	if got, c, err := s.BreakGlassCredential(
		ctx,
		bare.Slug,
	); err != nil || got.ID != bare.ID ||
		c != nil {
		t.Errorf("no credential = %+v, %+v, %v", got, c, err)
	}
	if _, _, err := s.BreakGlassCredential(ctx, "nobody"); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("no organization = %v", err)
	}
	// A hash written around the operator API signs no one in.
	if _, err := db.Exec(ctx, `INSERT INTO organization_break_glass (organization_id, login,
		password_hash) VALUES ($1, 'admin', 'nope')`, bare.ID); err != nil {
		t.Fatal(err)
	}
	if _, c, err := s.BreakGlassCredential(ctx, bare.Slug); err != nil || c != nil {
		t.Errorf("a malformed hash = %+v, %v", c, err)
	}

	entry := func(result string) kernel.OrganizationAuditEntry {
		e := created(o)
		e.ActorKind, e.ActorID = kernel.PrincipalBreakGlass, o.ID.String()
		e.Action, e.EventType = "organization.break_glass_sign_in", "NONE"
		e.Body = []byte(`{"login":"admin","result":"` + result + `"}`)
		return e
	}
	_, hash := session.NewSecret()
	ss := session.Session{
		ID: kernel.NewULID(), OrganizationID: o.ID, Channel: session.ChannelWeb,
		ActorKind: kernel.PrincipalBreakGlass, ActorID: o.ID.ULID, Login: "admin", Name: "admin",
		Admin: true, ExpiresAt: time.Now().Add(time.Hour), IdleExpiresAt: time.Now().Add(time.Hour),
		SecretHash: hash,
	}
	if err := s.SignIn(ctx, ss, entry("success")); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SessionBySecret(
		ctx,
		hash,
	); err != nil || got.ActorKind != kernel.PrincipalBreakGlass ||
		!got.AccountID.IsZero() ||
		!got.MemberID.IsZero() {
		t.Errorf("the break-glass session = %+v, %v", got, err)
	}
	if err := s.RefuseSignIn(ctx, entry("refused")); err != nil {
		t.Fatal(err)
	}
	var results []string
	rows, err := db.Query(ctx, `SELECT body->>'result' FROM organization_audit_trail_entries
		WHERE organization_id = $1 AND actor_kind = 'break_glass' ORDER BY created_at, id`, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			t.Fatal(err)
		}
		results = append(results, r)
	}
	if !slices.Equal(results, []string{"success", "refused"}) {
		t.Errorf("trail = %v", results)
	}

	// A session that cannot be written records no entry either.
	dup := ss
	dup.ID = kernel.NewULID()
	if err := s.SignIn(ctx, dup, entry("success")); err == nil {
		t.Error("a second session with the same secret signed in")
	}
	var n int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM organization_audit_trail_entries
		WHERE organization_id = $1 AND actor_kind = 'break_glass'`, o.ID).Scan(&n); err != nil || n != 2 {
		t.Errorf("entries after a failed sign-in = %d, %v", n, err)
	}
}

// TestAccAdmission checks that account_users rows admit a member, in slug
// order, and that another organization's account admits no one.
func TestAccAdmission(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "monica")
	y := seed(t, db, "laurie")
	sibling := kernel.NewAccountID()
	if _, err := db.Exec(ctx, `INSERT INTO accounts (organization_id, id, slug, name)
		VALUES ($1, $2, 'aardvark', 'Aardvark')`, x.org, sibling); err != nil {
		t.Fatal(err)
	}
	if as, err := x.s.Accounts(ctx, x.org, x.member); err != nil || len(as) != 0 {
		t.Errorf("Accounts with no rows = %+v, %v", as, err)
	}
	for _, acct := range []kernel.AccountID{x.acct, sibling} {
		if _, err := db.Exec(
			ctx,
			`INSERT INTO account_users (account_id, organization_id, member_id)
			VALUES ($1, $2, $3)`,
			acct,
			x.org,
			x.member,
		); err != nil {
			t.Fatal(err)
		}
	}
	as, err := x.s.Accounts(ctx, x.org, x.member)
	if err != nil || len(as) != 2 || as[0].ID != sibling || as[0].Slug != "aardvark" ||
		as[0].Name != "Aardvark" || as[1].ID != x.acct {
		t.Errorf("Accounts = %+v, %v", as, err)
	}
	if a, ok, err := x.s.Admits(
		ctx,
		x.org,
		x.member,
		sibling,
	); err != nil || !ok ||
		a.Slug != "aardvark" {
		t.Errorf("Admits = %+v, %t, %v", a, ok, err)
	}
	for name, c := range map[string]struct {
		org    kernel.OrganizationID
		member kernel.MemberID
		acct   kernel.AccountID
	}{
		"another organization's account": {x.org, x.member, y.acct},
		"another member":                 {x.org, kernel.NewMemberID(), x.acct},
		"through another organization":   {y.org, x.member, x.acct},
	} {
		if _, ok, err := x.s.Admits(ctx, c.org, c.member, c.acct); err != nil || ok {
			t.Errorf("%s = %t, %v", name, ok, err)
		}
	}
}

func TestAccLoginStoreUnavailable(t *testing.T) {
	p := pool(t)
	s := pg.New(p)
	p.Close()
	ctx := context.Background()
	id, org, member := kernel.NewULID(), kernel.NewOrganizationID(), kernel.NewMemberID()
	e := kernel.OrganizationAuditEntry{OrganizationID: org}
	for name, err := range map[string]error{
		"by secret": func() error { _, err := s.SessionBySecret(ctx, []byte("x")); return err }(),
		"touch":     s.TouchSession(ctx, id, time.Now()),
		"end":       func() error { _, err := s.EndSession(ctx, id); return err }(),
		"credential": func() error {
			_, _, err := s.BreakGlassCredential(ctx, "acme")
			return err
		}(),
		"sign in":  s.SignIn(ctx, session.Session{ID: id, OrganizationID: org}, e),
		"refuse":   s.RefuseSignIn(ctx, e),
		"accounts": func() error { _, err := s.Accounts(ctx, org, member); return err }(),
		"admits": func() error {
			_, _, err := s.Admits(ctx, org, member, kernel.NewAccountID())
			return err
		}(),
	} {
		if err == nil || errors.Is(err, port.ErrNotFound) {
			t.Errorf("%s on a closed pool = %v", name, err)
		}
	}
}

// TestAccLoginStoreAborted runs the reads in a transaction a failed
// statement aborted.
func TestAccLoginStoreAborted(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "russ")
	errAborted := errors.New("aborted")
	err := tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
		_, _ = tx.Q(ctx, db).Exec(ctx, `SELECT 1/0`)
		for name, err := range map[string]error{
			"accounts": func() error { _, err := x.s.Accounts(ctx, x.org, x.member); return err }(),
			"end":      func() error { _, err := x.s.EndSession(ctx, kernel.NewULID()); return err }(),
		} {
			if err == nil || errors.Is(err, port.ErrNotFound) {
				t.Errorf("%s in an aborted transaction = %v", name, err)
			}
		}
		return errAborted
	})
	if !errors.Is(err, errAborted) {
		t.Fatal(err)
	}
}
