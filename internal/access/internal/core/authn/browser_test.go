package authn_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/authn"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/breakglass"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/loopback"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

const (
	bgLogin    = "admin"
	bgPassword = "correct horse battery staple"
)

// credential is bgPassword's hash, made once: each costs 600,000 PBKDF2
// iterations.
var credential = sync.OnceValue(func() breakglass.Hash {
	h, err := breakglass.New(bgPassword)
	if err != nil {
		panic(err)
	}
	return h
})

// withOrg gives w the organization slug, with the break-glass credential
// when cred is true.
func (w *world) withOrg(slug string, cred bool) organization.Organization {
	o := organization.Organization{ID: kernel.NewOrganizationID(), Slug: kernel.Slug(slug)}
	oc := orgCred{org: o}
	if cred {
		oc.cred = &breakglass.Credential{Login: bgLogin, Hash: credential()}
	}
	w.f.orgs[o.Slug] = oc
	return o
}

func TestUnitSignInBreakGlass(t *testing.T) {
	w := newWorld(t)
	org := w.withOrg("acme", true)
	w.withOrg("bare", false)
	b, err := w.s.SignInBreakGlass(t.Context(), "acme", bgLogin, bgPassword, "192.0.2.7")
	if err != nil {
		t.Fatal(err)
	}
	ss := w.f.sessions[b.Session.ID]
	if ss.Channel != session.ChannelWeb || ss.ActorKind != kernel.PrincipalBreakGlass ||
		ss.ActorID != org.ID.ULID || ss.OrganizationID != org.ID || !ss.AccountID.IsZero() ||
		!ss.MemberID.IsZero() || !ss.Admin || ss.Login != bgLogin || ss.IPAddress != "192.0.2.7" ||
		!ss.ExpiresAt.Equal(t0.Add(maxAge)) || !ss.IdleExpiresAt.Equal(t0.Add(idle)) {
		t.Errorf("session = %+v", ss)
	}
	if string(ss.SecretHash) != string(session.HashSecret(b.Secret)) {
		t.Error("the session does not store its cookie's hash")
	}
	p, got, err := w.s.AuthenticateBrowser(t.Context(), b.Secret, true)
	if err != nil || p.Kind != kernel.PrincipalBreakGlass || got.ID != ss.ID {
		t.Errorf("on an organization route = %+v, %v", p, err)
	}
	if _, _, err := w.s.AuthenticateBrowser(
		t.Context(), b.Secret, false,
	); !errors.Is(err, authn.ErrUnauthenticated) {
		t.Errorf("on an account route = %v", err)
	}

	// A slice, not a map, so the attempts reach the trail in the order the
	// check below expects.
	for _, c := range []struct{ name, slug, login, password string }{
		{"wrong password", "acme", bgLogin, "incorrect horse battery staple"},
		{"wrong login", "acme", "root", bgPassword},
		{"no credential", "bare", bgLogin, bgPassword},
		{"no organization", "initech", bgLogin, bgPassword},
		{"not a slug at all", "Not A Slug!", bgLogin, bgPassword},
	} {
		if _, err := w.s.SignInBreakGlass(
			t.Context(), c.slug, c.login, c.password, "",
		); err != authn.ErrUnauthenticated { //nolint:errorlint // the identical error
			t.Errorf("%s = %v", c.name, err)
		}
	}
	// The organizations that exist record each attempt; the others have
	// no trail to record it on.
	results := make([]string, 0, len(w.f.trail))
	for _, e := range w.f.trail {
		var body struct {
			Login  string `json:"login"`
			Result string `json:"result"`
		}
		_ = json.Unmarshal(e.Body, &body)
		if e.ActorKind != kernel.PrincipalBreakGlass || e.Action != authn.ActionBreakGlassSignIn ||
			e.EventType != "NONE" || e.AffectedResource.Type != "ORGANIZATION" {
			t.Errorf("trail entry = %+v", e)
		}
		results = append(results, e.AffectedResource.ID+" "+body.Login+" "+body.Result)
	}
	want := "acme admin success,acme admin refused,acme root refused,bare admin refused"
	if strings.Join(results, ",") != want {
		t.Errorf("trail = %q, want %q", results, want)
	}

	for _, op := range []string{"BreakGlassCredential", "SignIn"} {
		w.f.fail = op
		if _, err := w.s.SignInBreakGlass(
			t.Context(), "acme", bgLogin, bgPassword, "",
		); !errors.Is(err, errDown) {
			t.Errorf("%s down = %v", op, err)
		}
	}
	w.f.fail = "RefuseSignIn"
	if _, err := w.s.SignInBreakGlass(
		t.Context(),
		"acme",
		bgLogin,
		"nope",
		"",
	); !errors.Is(
		err,
		errDown,
	) {
		t.Errorf("RefuseSignIn down = %v", err)
	}
}

// member gives w a member's browser session in acct, admitted to the
// accounts admits, and returns the session and its cookie value.
func (w *world) member(
	acct kernel.AccountID,
	admits ...session.Admitted,
) (session.Session, string) {
	secret, hash := session.NewSecret()
	ss := session.Session{
		ID: kernel.NewULID(), OrganizationID: kernel.NewOrganizationID(), AccountID: acct,
		Channel: session.ChannelWeb, ActorKind: kernel.PrincipalUser, ActorID: kernel.NewULID(),
		MemberID: kernel.NewMemberID(), Login: "alice", Name: "Alice", CreatedAt: w.f.now,
		ExpiresAt: w.f.now.Add(maxAge), IdleExpiresAt: w.f.now.Add(idle), SecretHash: hash,
	}
	w.f.sessions[ss.ID] = ss
	w.f.admits[ss.MemberID] = admits
	return ss, secret
}

func TestUnitAuthenticateBrowser(t *testing.T) {
	w := newWorld(t)
	ss, secret := w.member(w.acct)
	p, got, err := w.s.AuthenticateBrowser(t.Context(), secret, false)
	if err != nil || p.SessionID != ss.ID || got.ID != ss.ID || w.f.touches != 0 {
		t.Errorf("AuthenticateBrowser = %+v, %v; %d touches", p, err, w.f.touches)
	}
	// A minute on, a use moves the idle end; within the minute it does not.
	w.f.now = w.f.now.Add(time.Minute)
	if _, _, err := w.s.AuthenticateBrowser(
		t.Context(),
		secret,
		false,
	); err != nil ||
		w.f.touches != 1 {
		t.Errorf("a minute on = %v; %d touches", err, w.f.touches)
	}
	if got := w.f.sessions[ss.ID].IdleExpiresAt; !got.Equal(w.f.now.Add(idle)) {
		t.Errorf("idle end = %s, want %s", got, w.f.now.Add(idle))
	}
	if _, _, err := w.s.AuthenticateBrowser(
		t.Context(),
		secret,
		false,
	); err != nil ||
		w.f.touches != 1 {
		t.Errorf("again within the minute = %v; %d touches", err, w.f.touches)
	}

	sess := func(f func(*session.Session)) string {
		s := ss
		var secret string
		secret, s.SecretHash = session.NewSecret()
		s.ID = kernel.NewULID()
		f(&s)
		w.f.sessions[s.ID] = s
		return secret
	}
	for name, secret := range map[string]string{
		"unknown":         "no-such-cookie",
		"revoked":         sess(func(s *session.Session) { s.RevokedAt = t0 }),
		"a CLI session":   sess(func(s *session.Session) { s.Channel = session.ChannelCLI }),
		"no account here": sess(func(s *session.Session) { s.AccountID = kernel.AccountID{} }),
	} {
		if _, _, err := w.s.AuthenticateBrowser(
			t.Context(), secret, false,
		); err != authn.ErrUnauthenticated { //nolint:errorlint // the identical error
			t.Errorf("%s = %v", name, err)
		}
	}

	// A member no account admits holds a session of no account, which
	// organization routes admit.
	none := sess(func(s *session.Session) { s.AccountID = kernel.AccountID{} })
	if p, _, err := w.s.AuthenticateBrowser(
		t.Context(),
		none,
		true,
	); err != nil ||
		!p.AccountID.IsZero() {
		t.Errorf("no account on an organization route = %+v, %v", p, err)
	}

	for _, op := range []string{"SessionBySecret", "Session", "TouchSession"} {
		w.s.Cache().Flush()
		w.f.fail = op
		secret := sess(func(s *session.Session) { s.IdleExpiresAt = w.f.now.Add(idle - time.Hour) })
		if _, _, err := w.s.AuthenticateBrowser(
			t.Context(),
			secret,
			false,
		); !errors.Is(
			err,
			errDown,
		) {
			t.Errorf("%s down = %v", op, err)
		}
	}
}

// TestUnitBrowserLimits runs the idle and absolute limits in virtual time:
// a session used every 11 hours outlives its first idle end but not its
// absolute one, and one left 12 hours ends.
func TestUnitBrowserLimits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.withOrg("acme", true)
		s := authn.New(authn.Deps{
			Sessions: w.f, Tokens: w.f, BreakGlass: w.f, Admission: w.f,
			Idle: idle, MaxAge: maxAge, Now: time.Now,
		})
		sign := func() string {
			w.f.now = time.Now()
			b, err := s.SignInBreakGlass(t.Context(), "acme", bgLogin, bgPassword, "")
			if err != nil {
				t.Fatal(err)
			}
			return b.Secret
		}
		use := func(secret string) error {
			_, _, err := s.AuthenticateBrowser(t.Context(), secret, true)
			return err
		}

		idleOne := sign()
		<-time.After(11 * time.Hour)
		if err := use(idleOne); err != nil {
			t.Fatalf("after 11 hours: %v", err)
		}
		<-time.After(11 * time.Hour)
		if err := use(idleOne); err != nil {
			t.Fatalf("22 hours in, 11 after its last use: %v", err)
		}
		<-time.After(idle)
		if err := use(idleOne); !errors.Is(err, authn.ErrUnauthenticated) {
			t.Errorf("12 hours unused = %v", err)
		}

		busy := sign()
		start := time.Now()
		for time.Since(start)+11*time.Hour < maxAge {
			<-time.After(11 * time.Hour)
			if err := use(busy); err != nil {
				t.Fatalf("%s in: %v", time.Since(start), err)
			}
		}
		<-time.After(maxAge - time.Since(start))
		if err := use(busy); !errors.Is(err, authn.ErrUnauthenticated) {
			t.Errorf("at its absolute end = %v", err)
		}
	})
}

// rsaKey is a 2048-bit public key in PEM, the smallest the seal accepts.
var rsaKey = sync.OnceValue(func() string {
	k, err := rsa.GenerateKey(rand.Reader, loopback.MinKeyBits)
	if err != nil {
		panic(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		panic(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
})

func TestUnitCLIToken(t *testing.T) {
	w := newWorld(t)
	parent, _ := w.member(w.acct)
	// The parent's end in whole seconds, as a token's expiry is.
	parent.ExpiresAt = w.f.now.Add(maxAge - 30*time.Minute)
	w.f.sessions[parent.ID] = parent
	w.f.now = w.f.now.Add(500 * time.Millisecond)
	p := parent.Principal()

	sealed, err := w.s.CLIToken(t.Context(), p, rsaKey())
	if err != nil {
		t.Fatal(err)
	}
	if key, tok, ok := strings.Cut(sealed, "."); !ok || key == "" || tok == "" {
		t.Errorf("sealed = %q, want <key>.<token>", sealed)
	}
	var child session.Session
	for _, s := range w.f.sessions {
		if s.ParentID == parent.ID {
			child = s
		}
	}
	if child.Channel != session.ChannelCLI || child.AccountID != w.acct ||
		child.MemberID != parent.MemberID || !child.ExpiresAt.Equal(parent.ExpiresAt) ||
		!child.IdleExpiresAt.IsZero() || child.SecretHash != nil {
		t.Errorf("CLI session = %+v", child)
	}

	// The CLI token outlives its parent's logout.
	if _, err := w.s.Logout(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	raw := fmt.Sprintf("%s %s %s", token.KindUser, child.ID, audience)
	if _, _, err := w.s.Authenticate(t.Context(), raw, false); err != nil {
		t.Errorf("the CLI token after its parent's logout: %v", err)
	}
	w.f.now = child.ExpiresAt
	if _, _, err := w.s.Authenticate(
		t.Context(),
		raw,
		false,
	); !errors.Is(
		err,
		authn.ErrUnauthenticated,
	) {
		t.Errorf("the CLI token at its parent's end: %v", err)
	}

	w2 := newWorld(t)
	tokenSession, _ := w2.member(w2.acct)
	tokenSession.Channel = session.ChannelCLI
	w2.f.sessions[tokenSession.ID] = tokenSession
	if _, err := w2.s.CLIToken(
		t.Context(),
		tokenSession.Principal(),
		rsaKey(),
	); !errors.Is(
		err,
		authn.ErrNotBrowser,
	) {
		t.Errorf("from a CLI session = %v", err)
	}
	web, _ := w2.member(w2.acct)
	if _, err := w2.s.CLIToken(
		t.Context(),
		web.Principal(),
		"not a key",
	); !errors.Is(
		err,
		loopback.ErrKey,
	) {
		t.Errorf("a bad key = %v", err)
	}
	ending := web
	ending.ID, ending.ExpiresAt = kernel.NewULID(), w2.f.now.Add(500*time.Millisecond)
	w2.f.sessions[ending.ID] = ending
	if _, err := w2.s.CLIToken(
		t.Context(),
		ending.Principal(),
		rsaKey(),
	); !errors.Is(
		err,
		authn.ErrUnauthenticated,
	) {
		t.Errorf("under a second left = %v", err)
	}
	for _, op := range []string{"Issue", "CreateSession", "Session"} {
		w2.s.Cache().Flush()
		w2.f.fail = op
		if _, err := w2.s.CLIToken(
			t.Context(),
			web.Principal(),
			rsaKey(),
		); !errors.Is(
			err,
			errDown,
		) {
			t.Errorf("%s down = %v", op, err)
		}
	}
}

func TestUnitSwitchAccount(t *testing.T) {
	w := newWorld(t)
	home := session.Admitted{ID: w.acct, Slug: "home", Name: "Home"}
	sibling := session.Admitted{ID: kernel.NewAccountID(), Slug: "sibling", Name: "Sibling"}
	cur, secret := w.member(w.acct, home, sibling)
	p := cur.Principal()
	if as, err := w.s.Accounts(t.Context(), p); err != nil || len(as) != 2 {
		t.Errorf("Accounts = %+v, %v", as, err)
	}

	w.f.now = w.f.now.Add(time.Hour)
	sw, err := w.s.SwitchAccount(t.Context(), p, sibling.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sw.Secret == "" || sw.Token != "" || sw.Account != sibling ||
		sw.Session.AccountID != sibling.ID || sw.Session.Channel != session.ChannelWeb ||
		!sw.Session.ExpiresAt.Equal(cur.ExpiresAt) ||
		!sw.Session.IdleExpiresAt.Equal(w.f.now.Add(idle)) {
		t.Errorf("Switched = %+v", sw)
	}
	if w.f.sessions[cur.ID].RevokedAt.IsZero() {
		t.Error("the switch left the old session live")
	}
	if _, _, err := w.s.AuthenticateBrowser(
		t.Context(),
		secret,
		false,
	); !errors.Is(
		err,
		authn.ErrUnauthenticated,
	) {
		t.Errorf("the old cookie = %v", err)
	}
	np, _, err := w.s.AuthenticateBrowser(t.Context(), sw.Secret, false)
	if err != nil || np.AccountID != sibling.ID {
		t.Errorf("the new cookie = %+v, %v", np, err)
	}

	if _, err := w.s.SwitchAccount(
		t.Context(),
		np,
		kernel.NewAccountID(),
	); !errors.Is(
		err,
		authn.ErrNotAdmitted,
	) {
		t.Errorf("to an account that does not admit the member = %v", err)
	}
	bg := kernel.Principal{Kind: kernel.PrincipalBreakGlass, OrganizationID: cur.OrganizationID}
	if _, err := w.s.SwitchAccount(
		t.Context(),
		bg,
		home.ID,
	); !errors.Is(
		err,
		authn.ErrNotAdmitted,
	) {
		t.Errorf("a break-glass session = %v", err)
	}
	if as, err := w.s.Accounts(t.Context(), bg); err != nil || as != nil {
		t.Errorf("Accounts of a break-glass session = %+v, %v", as, err)
	}

	// A token session switches to a new token with the old one's end.
	cli := cur
	cli.ID, cli.Channel, cli.SecretHash, cli.IdleExpiresAt = kernel.NewULID(), session.ChannelCLI, nil, time.Time{}
	cli.ExpiresAt = w.f.now.Add(2 * time.Hour)
	w.f.sessions[cli.ID] = cli
	tsw, err := w.s.SwitchAccount(t.Context(), cli.Principal(), home.ID)
	if err != nil || tsw.Token == "" || tsw.Secret != "" ||
		!tsw.Claims.Expiry.Equal(cli.ExpiresAt) ||
		tsw.Session.Channel != session.ChannelCLI ||
		tsw.Session.AccountID != home.ID {
		t.Errorf("a token switch = %+v, %v", tsw, err)
	}
	if !w.f.sessions[sw.Session.ID].RevokedAt.IsZero() {
		t.Error("a CLI switch ended the browser session")
	}
	short := cli
	short.ID, short.ExpiresAt = kernel.NewULID(), w.f.now.Add(500*time.Millisecond)
	w.f.sessions[short.ID] = short
	if _, err := w.s.SwitchAccount(
		t.Context(),
		short.Principal(),
		home.ID,
	); !errors.Is(
		err,
		authn.ErrUnauthenticated,
	) {
		t.Errorf("a token with under a second left = %v", err)
	}

	for _, op := range []string{"Admits", "Session", "Issue", "CreateSession"} {
		w.s.Cache().Flush()
		w.f.fail = op
		if _, err := w.s.SwitchAccount(
			t.Context(),
			tsw.Session.Principal(),
			sibling.ID,
		); !errors.Is(
			err,
			errDown,
		) {
			t.Errorf("%s down = %v", op, err)
		}
	}
	w.f.fail = "Accounts"
	if _, err := w.s.Accounts(t.Context(), p); !errors.Is(err, errDown) {
		t.Errorf("Accounts down = %v", err)
	}
}
