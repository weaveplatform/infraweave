package authn

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/breakglass"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/loopback"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// The break-glass sign-in's trail entry (decision 0015): it changes
// nothing, so its event type is NONE, and it affects the organization.
const (
	ActionBreakGlassSignIn = "organization.break_glass_sign_in"
	eventNone              = "NONE"
	resourceOrganization   = "ORGANIZATION"
)

// touchEvery is how stale a browser session's idle end may get before a
// use moves it: one write a minute per session rather than one a request,
// against an idle limit of hours.
const touchEvery = time.Minute

var (
	// ErrNotBrowser reports a CLI token asked for by a credential that is
	// not a member's browser session.
	ErrNotBrowser = errors.New("not a browser session")
	// ErrNotAdmitted reports a switch to an account that does not admit
	// the caller, or by a caller who is not a member.
	ErrNotAdmitted = errors.New("not admitted")
)

// Browser is a browser session and the cookie value that names it, which
// is answered once, at sign-in or switch, and never stored.
type Browser struct {
	Secret  string
	Session session.Session
}

// secretIndex maps a cookie's hash to its session. A secret names one
// session for good, so an entry never goes stale; the session's liveness
// is the cache's to judge.
type secretIndex struct {
	mu  sync.Mutex
	ids map[string]kernel.ULID
}

// maxSecrets bounds the index; past it, the index starts again empty.
const maxSecrets = 1 << 14

func (x *secretIndex) get(hash []byte) (kernel.ULID, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	id, ok := x.ids[hex.EncodeToString(hash)]
	return id, ok
}

func (x *secretIndex) put(hash []byte, id kernel.ULID) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if len(x.ids) >= maxSecrets {
		x.ids = map[string]kernel.ULID{}
	}
	x.ids[hex.EncodeToString(hash)] = id
}

// AuthenticateBrowser is the principal a browser session's cookie value
// acts as on a route of reach, and the session. A cookie that names no session, or a session
// that is not live or not a browser session, is ErrUnauthenticated. A use
// moves the session's idle end at most once a minute.
func (s *Service) AuthenticateBrowser(
	ctx context.Context,
	secret string,
	reach Reach,
) (kernel.Principal, session.Session, error) {
	hash := session.HashSecret(secret)
	id, ok := s.secrets.get(hash)
	if !ok {
		ss, err := s.d.Sessions.SessionBySecret(ctx, hash)
		if errors.Is(err, port.ErrNotFound) {
			return kernel.Principal{}, session.Session{}, ErrUnauthenticated
		}
		if err != nil {
			return kernel.Principal{}, session.Session{}, err //nolint:wrapcheck // the store names the read
		}
		id = ss.ID
		s.secrets.put(hash, id)
	}
	ss, err := s.cache.Get(ctx, id)
	if errors.Is(err, port.ErrNotFound) {
		return kernel.Principal{}, session.Session{}, ErrUnauthenticated
	}
	if err != nil {
		return kernel.Principal{}, session.Session{}, err //nolint:wrapcheck // as above
	}
	now := s.d.Now()
	if ss.Channel != session.ChannelWeb || !ss.Live(now) || !reaches(reach)(ss) {
		return kernel.Principal{}, session.Session{}, ErrUnauthenticated
	}
	if now.Sub(ss.IdleExpiresAt.Add(-s.d.Idle)) >= touchEvery {
		if err := s.d.Sessions.TouchSession(ctx, ss.ID, now.Add(s.d.Idle)); err != nil {
			return kernel.Principal{}, session.Session{}, err //nolint:wrapcheck // as above
		}
		// The next use reads the moved idle end.
		s.cache.Drop(ss.ID)
	}
	return ss.Principal(), ss, nil
}

// decoy is a hash a refused sign-in verifies against, so one without a
// credential to check costs what a wrong password does.
var decoy = sync.OnceValue(func() breakglass.Hash {
	h, _ := breakglass.New("decoy")
	return h
})

// SignInBreakGlass signs in to the organization slug names with its
// break-glass credential, recording ip as the session's address. The
// session is a browser session of actor kind break_glass, actor the
// organization, an organization admin in no account and of no member. A
// wrong login or password, an organization with no credential and one
// that does not exist are each ErrUnauthenticated; every attempt on an
// organization that exists is on its trail.
func (s *Service) SignInBreakGlass(
	ctx context.Context,
	slug, login, password, ip string,
) (Browser, error) {
	sl, err := organization.ParseSlug("organization", slug)
	if err != nil {
		decoy().Verify(password)
		return Browser{}, ErrUnauthenticated
	}
	org, cred, err := s.d.BreakGlass.BreakGlassCredential(ctx, sl)
	if errors.Is(err, port.ErrNotFound) {
		decoy().Verify(password)
		return Browser{}, ErrUnauthenticated
	}
	if err != nil {
		return Browser{}, err //nolint:wrapcheck // the store names the read
	}
	now := s.d.Now()
	entry := func(result string) kernel.OrganizationAuditEntry {
		body, _ := json.Marshal(
			map[string]string{"login": login, "result": result},
		) //nolint:errchkjson // strings
		return kernel.OrganizationAuditEntry{
			OrganizationID: org.ID,
			ActorKind:      kernel.PrincipalBreakGlass,
			ActorID:        org.ID.String(),
			Action:         ActionBreakGlassSignIn,
			EventType:      eventNone,
			AffectedResource: kernel.AuditResource{
				Type: resourceOrganization,
				ID:   string(org.Slug),
			},
			Body:      body,
			RemoteIP:  ip,
			CreatedAt: now,
		}
	}
	if !matches(cred, login, password) {
		if err := s.d.BreakGlass.RefuseSignIn(ctx, entry("refused")); err != nil {
			return Browser{}, err //nolint:wrapcheck // the store names the write
		}
		return Browser{}, ErrUnauthenticated
	}
	secret, hash := session.NewSecret()
	ss := session.Session{
		ID:             kernel.NewULID(),
		OrganizationID: org.ID,
		Channel:        session.ChannelWeb,
		ActorKind:      kernel.PrincipalBreakGlass,
		ActorID:        org.ID.ULID,
		Login:          cred.Login,
		Name:           cred.Login,
		Admin:          true,
		IPAddress:      ip,
		CreatedAt:      now,
		ExpiresAt:      now.Add(s.d.MaxAge),
		IdleExpiresAt:  now.Add(s.d.Idle),
		SecretHash:     hash,
	}
	if err := s.d.BreakGlass.SignIn(ctx, ss, entry("success")); err != nil {
		return Browser{}, err //nolint:wrapcheck // as above
	}
	return Browser{Secret: secret, Session: ss}, nil
}

// matches checks login and password against cred, verifying against the
// decoy when there is no credential, and comparing in constant time.
func matches(cred *breakglass.Credential, login, password string) bool {
	if cred == nil {
		decoy().Verify(password)
		return false
	}
	ok := cred.Hash.Verify(password)
	return subtle.ConstantTimeCompare([]byte(cred.Login), []byte(login)) == 1 && ok
}

// CLIToken issues a CLI token from p's browser session, sealed to the
// CLI's public key pubKey (loopback.Seal). Its session is channel
// INFRAWEAVECTL, of the same member and account, with the browser session
// as its parent and the parent's absolute end, which the token's expiry
// matches; it outlives the parent's logout and account switches. A
// credential that is not a member's browser session is ErrNotBrowser; a
// key Seal refuses is loopback.ErrKey.
func (s *Service) CLIToken(ctx context.Context, p kernel.Principal, pubKey string) (string, error) {
	parent, err := s.cache.Get(ctx, p.SessionID)
	if err != nil {
		return "", err //nolint:wrapcheck // the store names the read
	}
	if parent.Channel != session.ChannelWeb || parent.ActorKind != kernel.PrincipalUser {
		return "", ErrNotBrowser
	}
	now := s.d.Now()
	ttl := remaining(parent.ExpiresAt, now)
	if ttl < time.Second {
		return "", ErrUnauthenticated
	}
	child := parent
	child.ID, child.Channel, child.ParentID = kernel.NewULID(), session.ChannelCLI, parent.ID
	child.CreatedAt, child.IdleExpiresAt, child.SecretHash = now, time.Time{}, nil
	raw, claims, err := s.d.Tokens.Issue(ctx, token.Request{
		Kind:     token.KindUser,
		Subject:  parent.Login,
		Session:  child.ID,
		Audience: s.d.Audience,
		TTL:      ttl,
	})
	if err != nil {
		return "", fmt.Errorf("issue a CLI token: %w", err)
	}
	child.ExpiresAt = claims.Expiry
	sealed, err := loopback.Seal(pubKey, raw)
	if err != nil {
		return "", err //nolint:wrapcheck // names the key's fault
	}
	if err := s.d.Sessions.CreateSession(ctx, child); err != nil {
		return "", err //nolint:wrapcheck // the store names the write
	}
	return sealed, nil
}

// Accounts are the accounts that admit p's member, evaluated now; none for
// a principal that is not a member.
func (s *Service) Accounts(ctx context.Context, p kernel.Principal) ([]session.Admitted, error) {
	if p.Kind != kernel.PrincipalUser || p.MemberID.IsZero() {
		return nil, nil
	}
	return s.d.Admission.Accounts(ctx, p.OrganizationID, p.MemberID) //nolint:wrapcheck // as above
}

// Switched is the session a switch replaced p's with: a browser session's
// new cookie value, or a token session's new token.
type Switched struct {
	Session session.Session
	Account session.Admitted
	Secret  string
	Token   string
	Claims  token.Claims
}

// SwitchAccount replaces p's session with one for the account acct, in
// the same channel and with the same absolute end, when acct admits p's
// member; the old session ends. A caller who is not a member, or an
// account that does not admit them, is ErrNotAdmitted.
func (s *Service) SwitchAccount(
	ctx context.Context,
	p kernel.Principal,
	acct kernel.AccountID,
) (Switched, error) {
	if p.Kind != kernel.PrincipalUser || p.MemberID.IsZero() {
		return Switched{}, ErrNotAdmitted
	}
	a, ok, err := s.d.Admission.Admits(ctx, p.OrganizationID, p.MemberID, acct)
	if err != nil {
		return Switched{}, err //nolint:wrapcheck // the store names the read
	}
	if !ok {
		return Switched{}, ErrNotAdmitted
	}
	cur, err := s.cache.Get(ctx, p.SessionID)
	if err != nil {
		return Switched{}, err //nolint:wrapcheck // as above
	}
	now := s.d.Now()
	next := cur
	next.ID, next.AccountID, next.CreatedAt = kernel.NewULID(), a.ID, now
	out := Switched{Account: a}
	if cur.Channel == session.ChannelWeb {
		out.Secret, next.SecretHash = session.NewSecret()
		next.IdleExpiresAt = now.Add(s.d.Idle)
	} else {
		ttl := remaining(cur.ExpiresAt, now)
		if ttl < time.Second {
			return Switched{}, ErrUnauthenticated
		}
		out.Token, out.Claims, err = s.d.Tokens.Issue(ctx, token.Request{
			Kind: token.KindUser, Subject: cur.Login, Session: next.ID,
			Audience: s.d.Audience, TTL: ttl,
		})
		if err != nil {
			return Switched{}, fmt.Errorf("issue the switched token: %w", err)
		}
		next.ExpiresAt = out.Claims.Expiry
	}
	// CreateSession ends the member's live sessions in the channel, the
	// current one among them, in the same transaction.
	if err := s.d.Sessions.CreateSession(ctx, next); err != nil {
		return Switched{}, err //nolint:wrapcheck // the store names the write
	}
	s.cache.Drop(cur.ID)
	out.Session = next
	return out, nil
}

// remaining is the TTL of a token that ends with a session ending at end:
// Issue counts exp from now's whole second, so counting from it too ends
// the token at end itself, or at the second before when end has a
// fraction, never after.
func remaining(end, now time.Time) time.Duration {
	return end.Sub(now.Truncate(time.Second)).Truncate(time.Second)
}
