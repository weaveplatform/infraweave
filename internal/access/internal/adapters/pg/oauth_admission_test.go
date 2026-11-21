package pg_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/oauth"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

func undecidedConsent(t *testing.T, x seeded) oauth.Request {
	t.Helper()
	r, _ := oauthRequest(t, x)
	r.ID, r.Decided = kernel.NewULID(), false
	r.CreatedAt = time.Now().UTC().Truncate(time.Microsecond)
	r.ExpiresAt, r.CodeExpiresAt = r.CreatedAt.Add(time.Minute), r.CreatedAt.Add(time.Minute)
	_, r.CodeHash = session.NewSecret()
	if err := x.s.CreateRequest(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAccOAuthConsentSnapshotsOriginalIdentityAndRefusesReplay(t *testing.T) {
	x := seed(t, pool(t), "oauth-consent-identity")
	source := admissionBrowser(t, x)
	r := undecidedConsent(t, x)
	if err := x.s.Approve(t.Context(), source.Principal(), r, time.Now()); err != nil {
		t.Fatal(err)
	}
	stored, err := x.s.Request(t.Context(), r.ID)
	if err != nil || !stored.Decided || !slices.Equal(stored.CodeHash, r.CodeHash) || stored.MemberID != source.MemberID ||
		stored.AccountID != source.AccountID || stored.OrganizationID != source.OrganizationID ||
		!stored.OriginalTeamsKnown || !slices.Equal(stored.OriginalTeams, source.OriginalTeams) ||
		!reflect.DeepEqual(stored.OriginalMember, source.OriginalMember) || stored.IdentityProviderID != source.IdentityProviderID ||
		!reflect.DeepEqual(stored.IDPSubject, source.IDPSubject) {
		t.Fatalf("consent lost its authenticated original identity: %+v, %v", stored, err)
	}
	_, r.CodeHash = session.NewSecret()
	if err := x.s.Approve(t.Context(), source.Principal(), r, time.Now()); !errors.Is(err, port.ErrNotFound) {
		t.Fatalf("consent issued another code on replay: %v", err)
	}
	after, err := x.s.Request(t.Context(), r.ID)
	if err != nil || !reflect.DeepEqual(stored, after) {
		t.Fatalf("replayed consent replaced its original code or identity: %v", err)
	}
}

func TestAccOAuthConsentFencesRequestAndSource(t *testing.T) {
	for _, name := range []string{
		"missing account", "wrong actor", "foreign organization", "withdrawn admission", "revoked source",
		"unknown source", "missing request", "expired request", "redirect withdrawn",
	} {
		t.Run(name, func(t *testing.T) {
			x := seed(t, pool(t), "oauth-consent-fence")
			source := admissionBrowser(t, x)
			r := undecidedConsent(t, x)
			p, decision := source.Principal(), r
			want := oauth.ErrNotAdmitted
			switch name {
			case "missing account":
				decision.AccountID = kernel.NewAccountID()
			case "wrong actor":
				p.ActorID = kernel.NewULID()
			case "foreign organization":
				p.OrganizationID = kernel.NewOrganizationID()
			case "withdrawn admission":
				if _, err := x.db.Exec(t.Context(), `DELETE FROM account_users WHERE account_id=$1 AND member_id=$2`, x.acct, x.member); err != nil {
					t.Fatal(err)
				}
			case "revoked source":
				if _, err := x.s.EndSession(t.Context(), source.ID); err != nil {
					t.Fatal(err)
				}
			case "unknown source":
				p.SessionID = kernel.NewULID()
			case "missing request":
				decision.ID, want = kernel.NewULID(), port.ErrNotFound
			case "expired request":
				if _, err := x.db.Exec(t.Context(), `UPDATE oauth_requests SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, r.ID); err != nil {
					t.Fatal(err)
				}
				want = port.ErrNotFound
			case "redirect withdrawn":
				if _, err := x.db.Exec(t.Context(), `UPDATE oauth_requests SET redirect_uri='https://editor.example.com/cb' WHERE id=$1`, r.ID); err != nil {
					t.Fatal(err)
				}
				want = oauth.ErrRedirectHost
			}
			before, err := x.s.Request(t.Context(), r.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := x.s.Approve(t.Context(), p, decision, time.Now()); !errors.Is(err, want) {
				t.Fatalf("consent refusal = %v, want %v", err, want)
			}
			after, err := x.s.Request(t.Context(), r.ID)
			if err != nil || after.Decided || len(after.CodeHash) != 0 || !reflect.DeepEqual(before, after) {
				t.Fatalf("refused consent changed its request or issued a code: %v", err)
			}
			var sessions, grants int
			if err := x.db.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM sessions), (SELECT count(*) FROM session_grants)`).
				Scan(&sessions, &grants); err != nil || sessions != 1 ||
				grants != 0 {
				t.Fatalf("refused consent installed sessions/grants: %d/%d, %v", sessions, grants, err)
			}
		})
	}
}

func TestAccAccountEntryFailsClosedWithoutLoginRuntime(t *testing.T) {
	x := seed(t, pool(t), "entry-runtime-unavailable")
	source := admissionBrowser(t, x)
	r := undecidedConsent(t, x)
	if _, err := x.db.Exec(t.Context(), `UPDATE accounts SET authorization_scheme=$2 WHERE id=$1`, x.acct, accessapi.LoginPolicies); err != nil {
		t.Fatal(err)
	}
	// This is a storage wiring-failure control, not a substitute for the
	// public LOGIN programs that exercise the actual policy owner and OPA.
	candidate := memberEntry(source)
	_, enterErr := x.s.Enter(t.Context(), candidate, nil, source.ID)
	consentErr := x.s.Approve(t.Context(), source.Principal(), r, time.Now())
	for operation, err := range map[string]error{"entry": enterErr, "consent": consentErr} {
		if denied, ok := errors.AsType[*session.LoginDenied](err); !ok || denied.Message != "Account entry evaluation is unavailable." {
			t.Fatalf("%s with no LOGIN runtime = %v, want safe refusal", operation, err)
		}
	}
	if accounts, err := x.s.AdmittedAccounts(t.Context(), source); err != nil || len(accounts) != 0 {
		t.Fatalf("unconfigured LOGIN runtime admitted an account: %+v, %v", accounts, err)
	}
	if _, err := x.s.Session(t.Context(), candidate.ID); !errors.Is(err, port.ErrNotFound) {
		t.Fatalf("unconfigured LOGIN runtime persisted a session: %v", err)
	}
	stored, err := x.s.Request(t.Context(), r.ID)
	current, sourceErr := x.s.Session(t.Context(), source.ID)
	if err != nil || sourceErr != nil || stored.Decided || len(stored.CodeHash) != 0 || !current.Live(time.Now()) {
		t.Fatalf("unconfigured LOGIN runtime changed consent or its source: %v/%v", err, sourceErr)
	}
}

func TestAccOAuthConsentExpiresWhileAccountFenceIsHeld(t *testing.T) {
	x := seed(t, pool(t), "oauth-consent-expiry-fence")
	admit(t, x, x.member)
	ctx := t.Context()
	ss := x.webSession(time.Now().Add(time.Hour))
	if err := x.s.CreateSession(ctx, ss); err != nil {
		t.Fatal(err)
	}
	r, _ := oauthRequest(t, x)
	r.ID = kernel.NewULID()
	now := time.Now()
	r.ExpiresAt = now.Add(500 * time.Millisecond)
	if err := x.s.CreateRequest(ctx, r); err != nil {
		t.Fatal(err)
	}
	_, r.CodeHash = session.NewSecret()
	r.CodeExpiresAt = now.Add(time.Minute)
	holder, err := x.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	var account kernel.AccountID
	if err := holder.QueryRow(ctx, `SELECT id FROM accounts WHERE id=$1 FOR NO KEY UPDATE`, x.acct).Scan(&account); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- x.s.Approve(ctx, ss.Principal(), r, now) }()
	// The request's wall-clock deadline elapses while the account lock is
	// unavailable; neither its admission nor its code can be committed late.
	select {
	case err := <-done:
		t.Fatalf("consent did not wait for the account fence: %v", err)
	case <-time.After(time.Until(r.ExpiresAt) + 20*time.Millisecond):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, port.ErrNotFound) {
		t.Fatalf("expired consent issued a code after waiting: %v", err)
	}
	got, err := x.s.Request(ctx, r.ID)
	if err != nil || got.Decided || len(got.CodeHash) != 0 {
		t.Fatalf("expired request was consumed: decided=%t codeStored=%t err=%v", got.Decided, len(got.CodeHash) > 0, err)
	}
}
