package pg_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/oauth"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

func admissionBrowser(t *testing.T, x seeded) session.Session {
	t.Helper()
	admit(t, x, x.member)
	ss := x.webSession(time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour))
	member, subject := true, "local-session-fixture"
	ss.OriginalMember, ss.IDPSubject = &member, &subject
	ss.OriginalTeamsKnown, ss.OriginalTeams = true, []string{"original-team"}
	ss.Teams, ss.IdentityProviderID = []string{"effective-team"}, kernel.NewULID()
	if err := x.s.CreateSession(t.Context(), ss); err != nil {
		t.Fatal(err)
	}
	return ss
}

func memberEntry(source session.Session) session.Session {
	ss := source
	ss.ID, ss.Channel, ss.CreatedAt = kernel.NewULID(), session.ChannelCLI, time.Now()
	ss.ExpiresAt, ss.IdleExpiresAt = source.ExpiresAt.Add(-time.Minute), time.Time{}
	ss.Login, ss.Teams, ss.Admin = "untrusted-candidate", []string{"untrusted-team"}, true
	ss.OriginalMember, ss.OriginalTeams, ss.IDPSubject = nil, nil, nil
	return ss
}

func TestAccAccountEntryFencesIdentityAndSourceLifetime(t *testing.T) {
	for _, name := range []string{
		"missing account", "wrong actor", "wrong organization", "deactivated member", "withdrawn admission",
		"zero source", "unknown source", "revoked source", "expired source", "different member source", "extended lifetime",
	} {
		t.Run(name, func(t *testing.T) {
			x := seed(t, pool(t), "entry-fence")
			ctx := t.Context()
			source := admissionBrowser(t, x)
			candidate, sourceID := memberEntry(source), source.ID
			want := port.ErrCredentialChanged
			write := func(query string, args ...any) {
				t.Helper()
				if _, err := x.db.Exec(ctx, query, args...); err != nil {
					t.Fatal(err)
				}
			}
			switch name {
			case "missing account":
				candidate.AccountID, want = kernel.NewAccountID(), session.ErrNotAdmitted
			case "wrong actor":
				candidate.ActorID, want = kernel.NewULID(), session.ErrNotAdmitted
			case "wrong organization":
				candidate.OrganizationID, want = kernel.NewOrganizationID(), session.ErrNotAdmitted
			case "deactivated member":
				write(`UPDATE members SET status='deactivated' WHERE id=$1`, x.member)
				want = session.ErrNotAdmitted
			case "withdrawn admission":
				write(`DELETE FROM account_users WHERE account_id=$1 AND member_id=$2`, x.acct, x.member)
				want = session.ErrNotAdmitted
			case "zero source":
				sourceID = kernel.ULID{}
			case "unknown source":
				sourceID = kernel.NewULID()
			case "revoked source":
				if _, err := x.s.EndSession(ctx, source.ID); err != nil {
					t.Fatal(err)
				}
			case "expired source":
				write(`UPDATE sessions SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, source.ID)
			case "different member source":
				other := kernel.NewMemberID()
				write(`INSERT INTO members (organization_id,id,login,name,email,status)
					VALUES ($1,$2,'bob','Bob','bob@example.com','active')`, x.org, other)
				admit(t, x, other)
				otherSource := source
				otherSource.ID, otherSource.MemberID, otherSource.ActorID = kernel.NewULID(), other, other.ULID
				if err := x.s.CreateSession(ctx, otherSource); err != nil {
					t.Fatal(err)
				}
				sourceID = otherSource.ID
			case "extended lifetime":
				candidate.ExpiresAt = source.ExpiresAt.Add(time.Hour)
			}
			before, err := x.s.Session(ctx, source.ID)
			if err != nil {
				t.Fatal(err)
			}
			got, err := x.s.Enter(ctx, candidate, nil, sourceID)
			if !errors.Is(err, want) || !got.ID.IsZero() {
				t.Fatalf("refused entry returned session %s, error %v; want %v", got.ID, err, want)
			}
			if _, err := x.s.Session(ctx, candidate.ID); !errors.Is(err, port.ErrNotFound) {
				t.Fatalf("refused candidate acquired a persisted session: %v", err)
			}
			after, err := x.s.Session(ctx, source.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("refused entry changed its source session: %v", err)
			}
			var grants int
			if err := x.db.QueryRow(ctx, `SELECT count(*) FROM session_grants WHERE session_id=$1`, candidate.ID).Scan(&grants); err != nil || grants != 0 {
				t.Fatalf("refused entry installed grants: %d, %v", grants, err)
			}
		})
	}
}

func TestAccAccountEntryInsertFailureRollsBackRevocation(t *testing.T) {
	x := seed(t, pool(t), "entry-insert-rollback")
	source := admissionBrowser(t, x)
	before, err := x.s.Session(t.Context(), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	listener := listen(t, x.db)
	// The same-channel replacement revokes its predecessor before the real
	// duplicate primary key refuses insertion. Enter must roll both back.
	_, err = x.s.Enter(t.Context(), source, nil, source.ID)
	if failure, ok := errors.AsType[*pgconn.PgError](err); !ok || failure.Code != "23505" {
		t.Fatalf("duplicate session insertion = %v, want a PostgreSQL unique violation", err)
	}
	after, err := x.s.Session(t.Context(), source.ID)
	if err != nil || !reflect.DeepEqual(before, after) || !after.Live(time.Now()) {
		t.Fatalf("failed replacement revoked or changed its predecessor: %v", err)
	}
	if got := messages(t, listener); len(got) != 0 {
		t.Fatalf("rolled-back replacement emitted session invalidations: %v", got)
	}
}

func TestAccAccountEntryListsWithoutMutationAndSwitchesWithinOrganization(t *testing.T) {
	x := seed(t, pool(t), "entry-account-list")
	source := admissionBrowser(t, x)
	other := newAccount(t, x, "other-entry-account")
	if err := x.s.CreateAccount(t.Context(), other, x.member, byAdmin(x, orgadmin.ActionAccountCreate)); err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(t.Context(), `DELETE FROM account_users WHERE account_id=$1 AND member_id=$2`, other.ID, x.member); err != nil {
		t.Fatal(err)
	}
	before, err := x.s.Session(t.Context(), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := x.s.AdmittedAccounts(t.Context(), source)
	if err != nil || len(accounts) != 1 || accounts[0].ID != x.acct {
		t.Fatalf("account list includes an unadmitted account: %+v, %v", accounts, err)
	}
	var sessions, grants int
	err = x.db.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM sessions), (SELECT count(*) FROM session_grants)`).Scan(&sessions, &grants)
	after, readErr := x.s.Session(t.Context(), source.ID)
	if err != nil || readErr != nil || sessions != 1 || grants != 0 || !reflect.DeepEqual(before, after) {
		t.Fatalf("account discovery changed sessions/grants: %d/%d, %v/%v", sessions, grants, err, readErr)
	}
	// Admission to the target, rather than equality with the source account,
	// decides an account switch inside the same organization.
	if _, err := x.db.Exec(t.Context(), `INSERT INTO account_users (account_id,organization_id,member_id)
		VALUES ($1,$2,$3)`, other.ID, x.org, x.member); err != nil {
		t.Fatal(err)
	}
	candidate := memberEntry(source)
	candidate.AccountID = other.ID
	entered, err := x.s.Enter(t.Context(), candidate, nil, source.ID)
	if err != nil || entered.AccountID != other.ID || entered.Login != source.Login || entered.Admin ||
		!slices.Equal(entered.Teams, source.OriginalTeams) || !reflect.DeepEqual(entered.OriginalMember, source.OriginalMember) ||
		!entered.OriginalTeamsKnown || !slices.Equal(entered.OriginalTeams, source.OriginalTeams) ||
		entered.IdentityProviderID != source.IdentityProviderID || !reflect.DeepEqual(entered.IDPSubject, source.IDPSubject) {
		t.Fatalf("same-organization switch lost its original identity: %+v, %v", entered, err)
	}
	stale := source
	stale.ExpiresAt = time.Now().Add(-time.Second)
	if got, err := x.s.AdmittedAccounts(t.Context(), stale); !errors.Is(err, port.ErrCredentialChanged) || len(got) != 0 {
		t.Fatalf("expired identity discovered account entries: %+v, %v", got, err)
	}
	if _, err := x.s.EndSession(t.Context(), source.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := x.s.AdmittedAccounts(t.Context(), source); !errors.Is(err, port.ErrCredentialChanged) || len(got) != 0 {
		t.Fatalf("revoked source returned a partial account list: %+v, %v", got, err)
	}
}

func TestAccAccountAdmissionPostgresOutagesLeaveNoPartialState(t *testing.T) {
	for _, operation := range []string{"enter", "accounts", "consent"} {
		t.Run(operation, func(t *testing.T) {
			x := seed(t, pool(t), "entry-postgres-outage")
			source := admissionBrowser(t, x)
			candidate := memberEntry(source)
			request := undecidedConsent(t, x)
			var partial bool
			err := tx.Do(t.Context(), x.db, tx.Options{}, func(ctx context.Context) error {
				var pid int32
				if err := tx.Q(ctx, x.db).QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
					return err
				}
				if _, err := x.db.Exec(ctx, `SELECT pg_terminate_backend($1)`, pid); err != nil {
					return err
				}
				switch operation {
				case "enter":
					got, err := x.s.Enter(ctx, candidate, nil, source.ID)
					partial = !got.ID.IsZero()
					return err
				case "accounts":
					got, err := x.s.AdmittedAccounts(ctx, source)
					partial = len(got) != 0
					return err
				default:
					return x.s.Approve(ctx, source.Principal(), request, time.Now())
				}
			})
			if !errors.Is(err, kernel.ErrUnavailable) || partial {
				t.Fatalf("terminated admission backend returned partial=%t, error=%v; want unavailable", partial, err)
			}
			if _, err := x.s.Session(t.Context(), candidate.ID); !errors.Is(err, port.ErrNotFound) {
				t.Fatalf("outage left a partial admitted session: %v", err)
			}
			stored, err := x.s.Request(t.Context(), request.ID)
			current, sourceErr := x.s.Session(t.Context(), source.ID)
			if err != nil || sourceErr != nil || stored.Decided || len(stored.CodeHash) != 0 || !current.Live(time.Now()) {
				t.Fatalf("outage consumed consent or ended its source: %v/%v", err, sourceErr)
			}
		})
	}
}

// TestAccAccountEntryRecordsGroups checks that a member session's entry
// writes the groups its sign-in reported as the entered account's
// latest_groups, in the entry's transaction: another account keeps its
// own, an unchanged list keeps the admission's version, a sign-in that
// reported none writes none, unknown groups write nothing, and a failed
// entry writes nothing.
func TestAccAccountEntryRecordsGroups(t *testing.T) {
	x := seed(t, pool(t), "entry-groups")
	ctx := t.Context()
	admit(t, x, x.member, "stale")
	other := newAccount(t, x, "other-groups-account")
	if err := x.s.CreateAccount(ctx, other, x.member, byAdmin(x, orgadmin.ActionAccountCreate)); err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(ctx, `UPDATE account_users SET latest_groups='{elsewhere}'
		WHERE account_id=$1 AND member_id=$2`, other.ID, x.member); err != nil {
		t.Fatal(err)
	}
	groups := func(acct kernel.AccountID) ([]string, int64) {
		t.Helper()
		var g []string
		var v int64
		if err := x.db.QueryRow(ctx, `SELECT latest_groups, version FROM account_users
			WHERE account_id=$1 AND member_id=$2`, acct, x.member).Scan(&g, &v); err != nil {
			t.Fatal(err)
		}
		return g, v
	}
	source := func(known bool, teams ...string) session.Session {
		t.Helper()
		ss := x.webSession(time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour))
		member := true
		ss.OriginalMember, ss.OriginalTeamsKnown, ss.OriginalTeams = &member, known, nil
		if known {
			ss.OriginalTeams = append([]string{}, teams...)
		}
		if err := x.s.CreateSession(ctx, ss); err != nil {
			t.Fatal(err)
		}
		return ss
	}
	enter := func(src session.Session) session.Session {
		t.Helper()
		got, err := x.s.Enter(ctx, memberEntry(src), nil, src.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	_, before := groups(x.acct)
	entered := enter(source(true, "platform", "Ops"))
	got, v := groups(x.acct)
	if !slices.Equal(got, []string{"platform", "Ops"}) || v != before+1 ||
		!slices.Equal(entered.Teams, []string{"platform", "Ops"}) {
		t.Errorf("entry with groups wrote %v at version %d (from %d), teams %v", got, v, before, entered.Teams)
	}
	if g, _ := groups(other.ID); !slices.Equal(g, []string{"elsewhere"}) {
		t.Errorf("an account not entered has the groups %v", g)
	}

	enter(source(true, "platform", "Ops"))
	if g, again := groups(x.acct); again != v || !slices.Equal(g, []string{"platform", "Ops"}) {
		t.Errorf("unchanged groups moved the version from %d to %d", v, again)
	}

	enter(source(false))
	if g, unknown := groups(x.acct); unknown != v || !slices.Equal(g, []string{"platform", "Ops"}) {
		t.Errorf("unknown groups wrote %v at version %d", g, unknown)
	}

	// The candidate's own ID is the source's, so the insertion fails after
	// the groups are written; the transaction takes both back.
	failing := source(true, "rolled-back")
	if _, err := x.s.Enter(ctx, failing, nil, failing.ID); err == nil {
		t.Fatal("a duplicate session entered")
	}
	if g, after := groups(x.acct); after != v || !slices.Equal(g, []string{"platform", "Ops"}) {
		t.Errorf("a failed entry wrote %v at version %d", g, after)
	}

	enter(source(true))
	if g, _ := groups(x.acct); g == nil || len(g) != 0 {
		t.Errorf("a sign-in with no groups left %v", g)
	}
}

// groupAdmission is a seeded organization whose member no account admits
// yet, with the seats check f and a way to give the account a managed
// user group, bound or not, and a source session of the member whose
// sign-in reported known teams, or unknown ones.
type groupAdmission struct {
	seeded
	t *testing.T
	f *seats
}

func newGroupAdmission(t *testing.T, slug string) groupAdmission {
	t.Helper()
	x := seed(t, pool(t), slug)
	f := &seats{}
	x.s.WithFeatures(f)
	return groupAdmission{seeded: x, t: t, f: f}
}

// group creates the account's group name, bound to space-admin on the top
// space when bound, and returns its id.
func (g groupAdmission) group(name string, bound bool) kernel.ULID {
	g.t.Helper()
	id := kernel.NewULID()
	if _, err := g.db.Exec(g.t.Context(), `INSERT INTO managed_user_groups (account_id, id, name, created_at)
		VALUES ($1, $2, $3, now())`, g.acct, id, name); err != nil {
		g.t.Fatal(err)
	}
	if bound {
		if _, err := g.db.Exec(g.t.Context(), `INSERT INTO role_bindings
			(account_id, id, actor_kind, actor_id, role_id, space_id, origin, created_by)
			SELECT $1, gen_random_uuid(), 'group', $2, id, $1, 'binding', 'test'
			FROM roles WHERE account_id = $1 AND slug = 'space-admin'`, g.acct, id); err != nil {
			g.t.Fatal(err)
		}
	}
	return id
}

func (g groupAdmission) source(known bool, teams ...string) session.Session {
	g.t.Helper()
	ss := g.webSession(time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour))
	member := true
	ss.OriginalMember, ss.OriginalTeamsKnown = &member, known
	if known {
		ss.OriginalTeams = append([]string{}, teams...)
	}
	if err := g.s.CreateSession(g.t.Context(), ss); err != nil {
		g.t.Fatal(err)
	}
	return ss
}

// admission is the member's admission to the account: whether it exists,
// its latest_groups and version.
func (g groupAdmission) admission() (bool, []string, int64) {
	g.t.Helper()
	var groups []string
	var version int64
	err := g.db.QueryRow(g.t.Context(), `SELECT latest_groups, version FROM account_users
		WHERE account_id = $1 AND member_id = $2`, g.acct, g.member).Scan(&groups, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil, 0
	}
	if err != nil {
		g.t.Fatal(err)
	}
	return true, groups, version
}

// listed reports whether the switcher's listing for src holds the
// account.
func (g groupAdmission) listed(src session.Session) bool {
	g.t.Helper()
	accounts, err := g.s.AdmittedAccounts(g.t.Context(), src)
	if err != nil {
		g.t.Fatal(err)
	}
	return slices.ContainsFunc(accounts, func(a session.Admitted) bool { return a.ID == g.acct })
}

func (g groupAdmission) authzVersion() int64 {
	g.t.Helper()
	var v int64
	if err := g.db.QueryRow(g.t.Context(), `SELECT authz_version FROM accounts WHERE id = $1`,
		g.acct).Scan(&v); err != nil {
		g.t.Fatal(err)
	}
	return v
}

// TestAccAccountEntryAdmitsThroughGroups checks that, under MANAGED_USERS, a
// member no account_users row admits enters an account whose managed user
// group named exactly as one of its sign-in's groups holds a binding: the
// switcher lists the account without writing, and the entry creates the
// admission with those groups after the seats check, in its transaction,
// moving the account's authz_version. The admission then outlives the
// group's last binding.
func TestAccAccountEntryAdmitsThroughGroups(t *testing.T) {
	g := newGroupAdmission(t, "entry-group-admission")
	ctx := t.Context()
	ops := g.group("ops", true)
	g.group("idle", false)
	src := g.source(true, "idle", "ops")

	if !g.listed(src) {
		t.Fatal("the switcher leaves out an account a bound group admits")
	}
	if held, _, _ := g.admission(); held || g.f.calls != 0 {
		t.Fatalf("the switcher's listing admitted the member (%t) or checked a seat (%d)", held, g.f.calls)
	}
	authz := g.authzVersion()
	entered, err := g.s.Enter(ctx, memberEntry(src), nil, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	held, groups, version := g.admission()
	if !held || !slices.Equal(groups, []string{"idle", "ops"}) || version != 1 ||
		!slices.Equal(entered.Teams, []string{"idle", "ops"}) {
		t.Errorf("the entry's admission is %t %v at version %d, teams %v", held, groups, version, entered.Teams)
	}
	if g.f.calls != 1 || !g.f.inTx || g.f.usage.Organization != 0 {
		t.Errorf("seat check: %d calls, in a transaction %t, usage %+v", g.f.calls, g.f.inTx, g.f.usage)
	}
	if after := g.authzVersion(); after != authz+1 {
		t.Errorf("the admission moved authz_version from %d to %d", authz, after)
	}

	// The admission stands without the binding that created it, and admits
	// a session whose groups are unknown.
	if _, err := g.db.Exec(ctx, `DELETE FROM role_bindings WHERE actor_id = $1`, ops); err != nil {
		t.Fatal(err)
	}
	unknown := g.source(false)
	if _, err := g.s.Enter(ctx, memberEntry(unknown), nil, unknown.ID); err != nil || g.f.calls != 1 {
		t.Errorf("an admitted member's entry with unknown groups: %v, %d seat checks", err, g.f.calls)
	}
}

// TestAccAccountEntryRefusesWithoutBoundGroup checks that a member no
// account_users row admits is refused, and not listed, when no group of the
// account named exactly as one of its sign-in's groups holds a binding, or
// when its groups are unknown, and that nothing is written.
func TestAccAccountEntryRefusesWithoutBoundGroup(t *testing.T) {
	for name, tc := range map[string]struct {
		bound, known, elsewhere bool
		group                   string
		teams                   []string
	}{
		"group without binding":          {known: true, group: "ops", teams: []string{"ops"}},
		"no group of that exact name":    {bound: true, known: true, group: "Ops", teams: []string{"ops"}},
		"no groups reported":             {bound: true, known: true, group: "ops"},
		"unknown groups":                 {bound: true, group: "ops"},
		"a bound group of other account": {bound: true, known: true, elsewhere: true, group: "ops", teams: []string{"ops"}},
	} {
		t.Run(name, func(t *testing.T) {
			g := newGroupAdmission(t, "entry-group-refused")
			holder := g
			if tc.elsewhere {
				other := newAccount(t, g.seeded, "entry-group-refused-other")
				if err := g.s.CreateAccount(t.Context(), other, g.member, byAdmin(g.seeded, orgadmin.ActionAccountCreate)); err != nil {
					t.Fatal(err)
				}
				holder.acct, g.f.calls = other.ID, 0
			}
			holder.group(tc.group, tc.bound)
			src := g.source(tc.known, tc.teams...)
			if g.listed(src) {
				t.Error("the switcher lists an account that does not admit the member")
			}
			authz := g.authzVersion()
			if _, err := g.s.Enter(t.Context(), memberEntry(src), nil, src.ID); !errors.Is(err, session.ErrNotAdmitted) {
				t.Errorf("entry = %v, want not admitted", err)
			}
			if held, _, _ := g.admission(); held || g.f.calls != 0 || g.authzVersion() != authz {
				t.Errorf("a refused entry admitted the member (%t), checked %d seats or moved authz_version", held, g.f.calls)
			}
		})
	}
}

// TestAccAccountEntryThroughGroupsChecksSeat checks that an admission
// through the account's groups counts the member's seat once however many
// accounts admit it, and that a full seats limit refuses the entry with the
// limit's error, creating no admission and no session.
func TestAccAccountEntryThroughGroupsChecksSeat(t *testing.T) {
	g := newGroupAdmission(t, "entry-group-seat")
	ctx := t.Context()
	g.group("ops", true)
	other := newAccount(t, g.seeded, "entry-group-seat-other")
	if err := g.s.CreateAccount(ctx, other, g.member, byAdmin(g.seeded, orgadmin.ActionAccountCreate)); err != nil {
		t.Fatal(err)
	}
	full := &featuresapi.LimitError{Limit: featuresapi.Seats, Scope: featuresapi.ScopeAccount, Value: 1}
	g.f.calls, g.f.refuse = 0, full
	src := g.source(true, "ops")
	candidate := memberEntry(src)
	_, err := g.s.Enter(ctx, candidate, nil, src.ID)
	if le, ok := errors.AsType[*featuresapi.LimitError](err); !ok || le != full {
		t.Fatalf("entry beyond the seats limit = %v", err)
	}
	// The member holds its seat through the other account, so the
	// organization's count does not grow with this admission.
	if g.f.calls != 1 || g.f.usage.Organization != 0 {
		t.Errorf("seat check: %d calls, usage %+v", g.f.calls, g.f.usage)
	}
	if held, _, _ := g.admission(); held {
		t.Error("a refused entry left an admission")
	}
	if _, err := g.s.Session(ctx, candidate.ID); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("a refused entry left its session: %v", err)
	}
	if !g.listed(src) {
		t.Error("the switcher leaves out an account a bound group admits, with its seat unchecked")
	}
}

// TestAccOAuthConsentAdmitsThroughGroups checks that consenting to an
// authorization request for an account that admits the member only
// through a bound group creates the admission the code's redemption reads,
// and that no bound group refuses the consent.
func TestAccOAuthConsentAdmitsThroughGroups(t *testing.T) {
	g := newGroupAdmission(t, "consent-group-admission")
	ctx := t.Context()
	unbound := g.source(true, "ops")
	if err := g.s.Approve(ctx, unbound.Principal(), undecidedConsent(t, g.seeded), time.Now()); !errors.Is(err, oauth.ErrNotAdmitted) {
		t.Fatalf("consent without a bound group = %v", err)
	}
	g.group("ops", true)
	r := undecidedConsent(t, g.seeded)
	if err := g.s.Approve(ctx, unbound.Principal(), r, time.Now()); err != nil {
		t.Fatal(err)
	}
	if held, groups, _ := g.admission(); !held || !slices.Equal(groups, []string{"ops"}) {
		t.Errorf("consent's admission is %t %v", held, groups)
	}
	stored, err := g.s.Request(ctx, r.ID)
	if err != nil || !stored.Decided || stored.MemberID != g.member {
		t.Errorf("consent stored %+v, %v", stored, err)
	}
}
