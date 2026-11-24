package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// ssoSteps sign the scenario's browser in to the home organization through
// its OIDC providers, with infraweave as the relying party: each provider
// is an issuer of the scenario's own, the in-process issuer, whose faults
// a step chooses, or an issuer in the shared navikt container, whose
// sign-in form a step fills in. The servers trust both through
// auth.oidc_api_keys.ca_file, which the OIDC adapter's client reads.
type ssoSteps struct {
	w *harness.World

	local *deps.Issuer
	mock  *deps.OAuth2Mock
	ps    map[string]*ssoProvider

	// start is the last start's answer; callback the URL the provider
	// sent the browser back to.
	start    harness.Response
	callback *url.URL
	// logoutToken is the last back-channel logout token sent.
	logoutToken string
}

// ssoProvider is a provider of the home organization and its issuer.
type ssoProvider struct {
	id, client, issuer string
	local              bool
	// next is the identity navikt's form signs in next.
	next deps.Login
}

// ssoCallback is every provider's redirect URI at the servers.
const ssoCallback = harness.PublicURL + "/auth/oidc/callback"

func (s *ssoSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^two infraweave servers, A and B, on one database, trusting the scenario's OIDC issuers$`, s.servers)
	sc.Step(`^the home organization has the OIDC provider "(\w+)" at the (in-process|navikt) issuer, (enabled with the admin login "[^"]+"|disabled)$`, s.provider)
	sc.Step(`^the provider "(\w+)" takes the subject from the claim "(\w+)"$`, s.subjectClaim)
	sc.Step(`^the provider "(\w+)" signs in "([^"]+)" next with the claims '([^']*)'$`, s.signsIn)
	sc.Step(`^the provider "(\w+)" signs in "([^"]+)" next with the fault "([a-z0-9-]+)"$`, s.signsInFaulty)
	sc.Step(`^the browser starts signing in through the provider "(\w+)" at ([AB])(?: with "([^"]*)")?$`, s.startAt)
	sc.Step(`^the provider sends the browser back to ([AB])$`, s.backTo)
	sc.Step(`^the browser signs in through the provider "(\w+)" at ([AB])$`, s.signIn)
	sc.Step(`^the browser brings the callback to ([AB]) again$`, s.again)
	sc.Step(`^another browser brings the callback to ([AB])$`, s.otherBrowser)
	sc.Step(`^the sign-in waits past its 10 minutes$`, s.expire)
	sc.Step(`^the sign-in sends the browser to "([^"]*)"$`, s.sentTo)
	sc.Step(`^the sign-in answers (\d+) with the code "([a-z-]+)"$`, s.refusedWith)
	sc.Step(`^"GET ([^"]+)" through ([AB]) in the browser sends it to "([^"]*)"$`, s.page)
	sc.Step(`^the browser's session is "([^"]+)"'s, signed in through "(\w+)" with the teams "([^"]*)"$`, s.sessionIs)
	sc.Step(`^no session was signed in through "(\w+)"$`, s.noSession)
	sc.Step(`^"([^"]+)" is a (pending|deactivated) member of the home account$`, s.memberOf)
	sc.Step(`^the member "([^"]+)" is (active|pending|deactivated) with (\d+) identit(?:y|ies)$`, s.memberIs)
	sc.Step(`^the member "([^"]+)" is deactivated$`, s.deactivate)
	sc.Step(`^the provider "(\w+)" is enabled with the admin login "([^"]+)"$`, s.enabled)
	sc.Step(`^the provider "(\w+)"'s issuer rotates its key$`, s.rotate)
	sc.Step(`^the provider "(\w+)"'s JWKS was read (\d+) times?$`, s.fetches)
	sc.Step(`^the browser logs out through ([AB]) and is sent on to the provider "(\w+)"$`, s.logout)
	sc.Step(`^the provider "(\w+)" logs out "([^"]+)"'s (session|subject) through ([AB])$`, s.backchannel)
	sc.Step(`^replaying the logout token through ([AB]) answers 400$`, s.replay)
	sc.Step(`^starting a sign-in through the provider "(\w+)" at ([AB]) eventually answers 403 with the code "([a-z-]+)"$`, s.eventuallyRefused)
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if s.local != nil {
			s.local.Close()
		}
		return ctx, err
	})
}

// servers starts the in-process issuer and the shared navikt container,
// and the scenario's two servers trusting both, with the operator key.
func (s *ssoSteps) servers(ctx context.Context) error {
	w := s.w
	iss, err := deps.StartIssuer()
	if err != nil {
		return err //nolint:wrapcheck // names itself
	}
	s.local = iss
	if s.mock, err = w.Suite.Deps.OAuth2Mock(ctx); err != nil {
		return err //nolint:wrapcheck // names itself
	}
	ca := filepath.Join(w.Dir, "oidc-ca.pem")
	if err := os.WriteFile(ca, append(iss.CertPEM(), s.mock.CertPEM()...), 0o600); err != nil {
		return fmt.Errorf("oidc ca: %w", err)
	}
	auth := harness.OperatorAuth()
	auth["oidc_api_keys"] = map[string]any{"ca_file": ca}
	w.Settings["auth"] = auth
	if err := w.TwoReplicas(ctx); err != nil {
		return err //nolint:wrapcheck // names itself
	}
	db, err := w.Querier(ctx)
	if err != nil {
		return err
	}
	for _, t := range []harness.Tenant{harness.Home, harness.Sibling} {
		if err := t.Seed(ctx, db); err != nil {
			return err //nolint:wrapcheck // names the query
		}
	}
	return nil
}

func (s *ssoSteps) named(name string) (*ssoProvider, error) {
	p, ok := s.ps[name]
	if !ok {
		return nil, fmt.Errorf("%w: no provider %q", harness.ErrStep, name)
	}
	return p, nil
}

// provider seeds an OIDC provider of the home organization at an issuer of
// its own, its secret sealed as the API seals it, and registers its client
// there.
func (s *ssoSteps) provider(ctx context.Context, name, where, state string) error {
	p := &ssoProvider{id: kernel.NewULID().String(), client: "infraweave-" + name, local: where == "in-process"}
	if p.local {
		p.issuer = s.local.URL()
		s.local.RegisterClient(p.client, "secret-"+name)
	} else {
		p.issuer = s.mock.Issuer(fmt.Sprintf("sso-%d-%s", s.w.Suite.Deps.Serial(), name))
	}
	keys, pool, err := s.w.Keyring(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	sealed, err := keys.SealColumn(ctx, crypt.Column{
		Table: "identity_providers", Name: "secret_sealed", Key: []string{"id"},
	}, []string{harness.UUIDOf(p.id)}, []byte("secret-"+name))
	if err != nil {
		return fmt.Errorf("seal: %w", err)
	}
	settings, err := json.Marshal(map[string]any{"identityProviderHost": p.issuer, "clientId": p.client})
	if err != nil {
		return fmt.Errorf("settings: %w", err)
	}
	enabled, admin := "NULL", "NULL"
	if login, ok := strings.CutPrefix(state, `enabled with the admin login "`); ok {
		enabled, admin = "now()", harness.Literal(strings.TrimSuffix(login, `"`))
	}
	if _, err := s.w.Query(ctx, fmt.Sprintf(`INSERT INTO identity_providers (organization_id, id,
			kind, name, settings, secret_sealed, is_default, enabled_at, admin_login)
		VALUES ('%s', '%s', 'OIDC', %s, %s, '\x%s',
			NOT EXISTS (SELECT 1 FROM identity_providers WHERE kind = 'OIDC'), %s, %s)`,
		harness.UUIDOf(harness.Home.Org), harness.UUIDOf(p.id), harness.Literal(name),
		harness.Literal(string(settings)), hex.EncodeToString(sealed), enabled, admin)); err != nil {
		return err //nolint:wrapcheck // names the query
	}
	if s.ps == nil {
		s.ps = map[string]*ssoProvider{}
	}
	s.ps[name] = p
	s.w.IDs[name] = p.id
	return nil
}

func (s *ssoSteps) subjectClaim(ctx context.Context, name, claim string) error {
	p, err := s.named(name)
	if err != nil {
		return err
	}
	_, err = s.w.Query(ctx, fmt.Sprintf(`UPDATE identity_providers SET settings = settings ||
		jsonb_build_object('claimMapping', jsonb_build_array(jsonb_build_object('name', 'subject', 'value', %s)))
		WHERE id = '%s'`, harness.Literal(claim), harness.UUIDOf(p.id)))
	return err //nolint:wrapcheck // names the query
}

func (s *ssoSteps) signsIn(name, subject, raw string) error {
	p, err := s.named(name)
	if err != nil {
		return err
	}
	var claims map[string]any
	if err := json.Unmarshal([]byte(raw), &claims); err != nil {
		return fmt.Errorf("%w: claims %s: %w", harness.ErrStep, raw, err)
	}
	if p.local {
		s.local.SignInNext(deps.Identity{Subject: subject, Claims: claims})
	}
	p.next = deps.Login{Subject: subject, Claims: claims}
	return nil
}

func (s *ssoSteps) signsInFaulty(name, subject, fault string) error {
	p, err := s.named(name)
	if err != nil {
		return err
	}
	f, ok := deps.Faults[fault]
	if !ok || !p.local {
		return fmt.Errorf("%w: the provider %s has no fault %q", harness.ErrStep, name, fault)
	}
	s.local.SignInNext(deps.Identity{Subject: subject, Faults: []deps.Fault{f}})
	return nil
}

// browse sends GET path through replica in jar and records the answer.
func (s *ssoSteps) browse(ctx context.Context, jar *harness.Jar, replica, path string) (harness.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.w.Base(replica)+path, nil)
	if err != nil {
		return harness.Response{}, fmt.Errorf("%w: %w", harness.ErrStep, err)
	}
	resp, err := jar.Do(req)
	if err != nil {
		return harness.Response{}, fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return harness.Response{}, fmt.Errorf("GET %s: %w", path, err)
	}
	r := harness.Response{Status: resp.StatusCode, Header: resp.Header, Body: body}
	s.w.Sent = append(s.w.Sent, r)
	return r, nil
}

// startAt starts a sign-in through the provider at replica, with the
// query query, and keeps the answer.
func (s *ssoSteps) startAt(ctx context.Context, name, replica, query string) error {
	p, err := s.named(name)
	if err != nil {
		return err
	}
	path := "/auth/login/" + harness.Home.OrgSlug + "/" + p.id
	if query != "" {
		path += "?" + query
	}
	s.start, err = s.browse(ctx, s.w.Jar, replica, path)
	return err
}

// backTo plays the browser at the provider the last start sent it to, and
// brings the provider's answer to replica's callback.
func (s *ssoSteps) backTo(ctx context.Context, replica string) error {
	loc := s.start.Header.Get("Location")
	if s.start.Status != http.StatusFound || loc == "" {
		return fmt.Errorf("%w: the start answered %d: %s", harness.ErrStep, s.start.Status, s.start.Body)
	}
	var p *ssoProvider
	for _, c := range s.ps {
		if strings.HasPrefix(loc, c.issuer) {
			p = c
		}
	}
	if p == nil {
		return fmt.Errorf("%w: the start sent the browser to %s", harness.ErrStep, loc)
	}
	client := s.mock.Client()
	if p.local {
		client = s.local.Client()
	}
	back, err := deps.SignIn(ctx, client, loc, p.next)
	if err != nil {
		return err //nolint:wrapcheck // names itself
	}
	if !strings.HasPrefix(back.String(), ssoCallback+"?") {
		return fmt.Errorf("%w: the provider sent the browser to %s", harness.ErrStep, back)
	}
	s.callback = back
	_, err = s.browse(ctx, s.w.Jar, replica, back.RequestURI())
	return err
}

// signIn starts a sign-in at replica and finishes it there, which must
// set the session's cookie and send the browser to the console.
func (s *ssoSteps) signIn(ctx context.Context, name, replica string) error {
	if err := s.startAt(ctx, name, replica, ""); err != nil {
		return err
	}
	if err := s.backTo(ctx, replica); err != nil {
		return err
	}
	return s.sentTo("/")
}

func (s *ssoSteps) again(ctx context.Context, replica string) error {
	if s.callback == nil {
		return fmt.Errorf("%w: no callback yet", harness.ErrStep)
	}
	_, err := s.browse(ctx, s.w.Jar, replica, s.callback.RequestURI())
	return err
}

// otherBrowser brings the callback the provider sent to the scenario's
// browser to replica from a browser holding no cookie.
func (s *ssoSteps) otherBrowser(ctx context.Context, replica string) error {
	loc := s.start.Header.Get("Location")
	var p *ssoProvider
	for _, c := range s.ps {
		if strings.HasPrefix(loc, c.issuer) {
			p = c
		}
	}
	if p == nil || !p.local {
		return fmt.Errorf("%w: the start sent the browser to %q", harness.ErrStep, loc)
	}
	back, err := deps.SignIn(ctx, s.local.Client(), loc, p.next)
	if err != nil {
		return err //nolint:wrapcheck // names itself
	}
	s.callback = back
	_, err = s.browse(ctx, harness.NewJar(), replica, back.RequestURI())
	return err
}

func (s *ssoSteps) expire(ctx context.Context) error {
	_, err := s.w.Query(ctx, `UPDATE sso_requests SET created_at = now() - interval '11 minutes',
		expires_at = now() - interval '1 minute' WHERE consumed_at IS NULL`)
	return err //nolint:wrapcheck // names the query
}

func (s *ssoSteps) sentTo(want string) error {
	r := s.w.Last()
	if r.Status != http.StatusFound || r.Header.Get("Location") != want {
		return fmt.Errorf("%w: answered %d to %q, want %q: %s", harness.ErrStep, r.Status,
			r.Header.Get("Location"), want, r.Body)
	}
	return nil
}

// refusedWith checks that the last answer is a sign-in page refusing with
// code, which sets no session's cookie.
func (s *ssoSteps) refusedWith(status int, code string) error {
	r := s.w.Last()
	if r.Status != status || !strings.Contains(string(r.Body), `<code id="code">`+code+`</code>`) ||
		!strings.HasPrefix(r.Header.Get("Content-Type"), "text/html") {
		return fmt.Errorf("%w: answered %d %s, want %d %s", harness.ErrStep, r.Status, r.Body, status, code)
	}
	for _, c := range r.Header.Values("Set-Cookie") {
		if strings.HasPrefix(c, "__Host-infraweave_session=") {
			return fmt.Errorf("%w: a refusal set %s", harness.ErrStep, c)
		}
	}
	return nil
}

func (s *ssoSteps) page(ctx context.Context, path, replica, want string) error {
	path = s.w.Resolve(path)
	if _, err := s.browse(ctx, s.w.Jar, replica, path); err != nil {
		return err
	}
	return s.sentTo(s.w.Resolve(want))
}

// sessionIs checks the session the browser's cookie names: the login, the
// provider, its original teams, a browser session of actor kind user in no
// account, live.
func (s *ssoSteps) sessionIs(ctx context.Context, login, name, teams string) error {
	p, err := s.named(name)
	if err != nil {
		return err
	}
	value, ok := s.w.Jar.Session()
	if !ok {
		return fmt.Errorf("%w: the browser holds no session", harness.ErrStep)
	}
	h := sha256.Sum256([]byte(value))
	var got, provider, original string
	if err := s.w.Row(ctx, `SELECT login, identity_provider_id::text, array_to_string(original_teams, ',')
		FROM sessions WHERE secret_hash = $1 AND channel = 'WEB' AND actor_kind = 'user'
		AND account_id IS NULL AND original_teams_known AND original_member AND revoked_at IS NULL`,
		[]any{h[:]}, &got, &provider, &original); err != nil {
		return err //nolint:wrapcheck // names the query
	}
	if got != login || provider != harness.UUIDOf(p.id) || original != teams {
		return fmt.Errorf("%w: the session is %s's through %s with teams %q", harness.ErrStep, got, provider, original)
	}
	return nil
}

func (s *ssoSteps) noSession(ctx context.Context, name string) error {
	p, err := s.named(name)
	if err != nil {
		return err
	}
	n, err := s.w.Number(ctx, fmt.Sprintf(`SELECT count(*) FROM sessions WHERE identity_provider_id = '%s'`,
		harness.UUIDOf(p.id)))
	if err != nil {
		return err //nolint:wrapcheck // names the query
	}
	if n != 0 {
		return fmt.Errorf("%w: %d sessions signed in through %s", harness.ErrStep, n, name)
	}
	return nil
}

// memberOf seeds a member of the home organization under login, admitted
// to the home account.
func (s *ssoSteps) memberOf(ctx context.Context, login, status string) error {
	_, err := s.w.Query(ctx, fmt.Sprintf(`WITH m AS (
			INSERT INTO members (organization_id, id, login, name, email, status)
			VALUES ('%[1]s', gen_random_uuid(), %[2]s, %[2]s, '', '%[3]s') RETURNING id)
		INSERT INTO account_users (account_id, organization_id, member_id)
			SELECT '%[4]s', '%[1]s', id FROM m`,
		harness.UUIDOf(harness.Home.Org), harness.Literal(login), status, harness.UUIDOf(harness.Home.Account)))
	return err //nolint:wrapcheck // names the query
}

func (s *ssoSteps) deactivate(ctx context.Context, login string) error {
	_, err := s.w.Query(ctx, fmt.Sprintf(`UPDATE members SET status = 'deactivated'
		WHERE organization_id = '%s' AND login = %s`, harness.UUIDOf(harness.Home.Org), harness.Literal(login)))
	return err //nolint:wrapcheck // names the query
}

func (s *ssoSteps) memberIs(ctx context.Context, login, status string, identities int) error {
	var got string
	var n int
	if err := s.w.Row(ctx, `SELECT m.status, (SELECT count(*) FROM member_identities i
			WHERE i.organization_id = m.organization_id AND i.member_id = m.id)
		FROM members m WHERE m.organization_id = $1 AND m.login = $2`,
		[]any{harness.UUIDOf(harness.Home.Org), login}, &got, &n); err != nil {
		return err //nolint:wrapcheck // names the query
	}
	if got != status || n != identities {
		return fmt.Errorf("%w: %s is %s with %d identities", harness.ErrStep, login, got, n)
	}
	return nil
}

func (s *ssoSteps) enabled(ctx context.Context, name, login string) error {
	p, err := s.named(name)
	if err != nil {
		return err
	}
	var admin string
	if err := s.w.Row(ctx, `SELECT admin_login FROM identity_providers WHERE id = $1 AND enabled_at IS NOT NULL`,
		[]any{harness.UUIDOf(p.id)}, &admin); err != nil {
		return err //nolint:wrapcheck // names the query
	}
	if admin != login {
		return fmt.Errorf("%w: the admin login is %q", harness.ErrStep, admin)
	}
	return nil
}

func (s *ssoSteps) rotate(name string) error {
	if p, err := s.named(name); err != nil || !p.local {
		return fmt.Errorf("%w: %s is not the in-process issuer's: %w", harness.ErrStep, name, err)
	}
	return s.local.Rotate() //nolint:wrapcheck // names itself
}

func (s *ssoSteps) fetches(name string, want int) error {
	if p, err := s.named(name); err != nil || !p.local {
		return fmt.Errorf("%w: %s is not the in-process issuer's: %w", harness.ErrStep, name, err)
	}
	if got := s.local.JWKSFetches(); got != want {
		return fmt.Errorf("%w: the JWKS was read %d times, want %d", harness.ErrStep, got, want)
	}
	return nil
}

// logout logs the browser out through replica, which must answer the
// provider's end-session URL with the ID token it signed in with, and
// follows it to the provider.
func (s *ssoSteps) logout(ctx context.Context, replica, name string) error {
	p, err := s.named(name)
	if err != nil {
		return err
	}
	r, err := s.w.BrowserDo(ctx, replica, http.MethodPost, "/api/v1/auth/logout", harness.PublicURL, nil)
	if err != nil {
		return err //nolint:wrapcheck // names itself
	}
	var v struct {
		LogoutURL string `json:"logoutUrl"`
	}
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &v) != nil {
		return fmt.Errorf("%w: logout answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	u, err := url.Parse(v.LogoutURL)
	issued := s.local.Issued()
	if err != nil || !strings.HasPrefix(v.LogoutURL, p.issuer+"/logout?") || len(issued) == 0 ||
		u.Query().Get("id_token_hint") != issued[len(issued)-1].Token ||
		u.Query().Get("client_id") != p.client || u.Query().Get("post_logout_redirect_uri") != harness.PublicURL {
		return fmt.Errorf("%w: logoutUrl is %q", harness.ErrStep, v.LogoutURL)
	}
	// The browser stops at the provider, whose answer sends it on to the
	// public URL, which no test resolves.
	at := *s.local.Client()
	at.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.LogoutURL, nil)
	if err != nil {
		return fmt.Errorf("%w: %w", harness.ErrStep, err)
	}
	resp, err := at.Do(req)
	if err != nil {
		return fmt.Errorf("end session: %w", err)
	}
	_ = resp.Body.Close()
	if got := s.local.Logouts(); len(got) == 0 || got[len(got)-1].PostLogoutRedirectURI != harness.PublicURL {
		return fmt.Errorf("%w: the provider received %+v", harness.ErrStep, got)
	}
	return nil
}

// backchannel has the provider post a logout token naming login's last
// sign-in's session, or its subject alone, to replica, which must accept
// it.
func (s *ssoSteps) backchannel(ctx context.Context, name, subject, by, replica string) error {
	p, err := s.named(name)
	if err != nil {
		return err
	}
	var sid string
	for _, t := range s.local.Issued() {
		if t.Subject == subject {
			sid = t.SID
		}
	}
	sub := ""
	if by == "subject" {
		sub, sid = subject, ""
	}
	if s.logoutToken, err = s.local.LogoutToken(p.client, sub, sid); err != nil {
		return err //nolint:wrapcheck // names itself
	}
	status, err := deps.SendLogout(ctx, http.DefaultClient, s.w.Base(replica)+"/auth/oidc/backchannel-logout",
		s.logoutToken)
	if err != nil {
		return err //nolint:wrapcheck // names itself
	}
	if status != http.StatusOK {
		return fmt.Errorf("%w: the back-channel logout answered %d", harness.ErrStep, status)
	}
	return nil
}

func (s *ssoSteps) replay(ctx context.Context, replica string) error {
	status, err := deps.SendLogout(ctx, http.DefaultClient, s.w.Base(replica)+"/auth/oidc/backchannel-logout",
		s.logoutToken)
	if err != nil {
		return err //nolint:wrapcheck // names itself
	}
	if status != http.StatusBadRequest {
		return fmt.Errorf("%w: the replay answered %d", harness.ErrStep, status)
	}
	return nil
}

// eventuallyRefused starts sign-ins until the replica refuses with code,
// as a replica applies an entitlement change on its notification.
func (s *ssoSteps) eventuallyRefused(ctx context.Context, name, replica, code string) error {
	return deps.Eventually(ctx, "a start through "+name+" to answer "+code,
		func(ctx context.Context) (bool, error) {
			if err := s.startAt(ctx, name, replica, ""); err != nil {
				return false, err
			}
			return s.refusedWith(http.StatusForbidden, code) == nil, nil
		})
}
