package pg_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
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
