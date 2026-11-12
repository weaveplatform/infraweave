package auth

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// browserSteps are the steps of the browser sessions: the break-glass
// sign-in, the Origin rule, CLI tokens and account switching.
//
// ref: decision 0006
type browserSteps struct {
	w *harness.World
}

func (b *browserSteps) register(sc *godog.ScenarioContext) {
	sc.Step(
		`^two infraweave servers, A and B, on one database, with an operator key and an expensive budget of (\d+)$`,
		b.serversWithBudget,
	)
	sc.Step(
		`^the browser signs in to "([a-z-]+)" as "([^"]+)" with the password "([^"]+)" through ([AB])$`,
		b.signIn,
	)
	sc.Step(
		`^signing in to "([a-z-]+)" as "([^"]+)" with the password "([^"]+)" through ([AB]) (with the public URL's Origin|without an Origin|with another site's Origin) answers a (\d+) problem "([a-z-]+)"$`,
		b.signInRefused,
	)
	sc.Step(`^the browser holds the session cookie, HttpOnly, Secure, SameSite=Lax and Path=/$`,
		b.holdsCookie)
	sc.Step(
		`^the viewer through ([AB]) in the browser is the organization admin "([^"]+)", with no token$`,
		b.viewerAdmin,
	)
	sc.Step(
		`^"([A-Z]+) ([^"]+)" through ([AB]) in the browser (with the public URL's Origin|without an Origin|with another site's Origin) answers a (\d+) problem "([a-z-]+)"$`,
		b.answersProblem,
	)
	sc.Step(
		`^the browser's account switcher through ([AB]) lists (no account|"[^"]+")$`,
		b.switcher,
	)
	sc.Step(`^the browser logs out through ([AB])$`, b.logout)
	sc.Step(`^the browser holds no session cookie$`, b.holdsNone)
	sc.Step(
		`^the browser's ended cookie answers 401 at the viewer through ([AB]) within a second$`,
		b.endedRefused,
	)
	sc.Step(`^the viewer through ([AB]) in the browser answers (\d+)$`, b.viewerAnswers)
	sc.Step(`^the trail of "([a-z-]+)" holds the break-glass sign-ins "([^"]+)"$`, b.trail)
	sc.Step(
		`^the home account's member has a browser session, admitted to (home alone|home and sibling)$`,
		b.memberSession,
	)
	sc.Step(`^a member no account admits has a browser session$`, b.unadmitted)
	sc.Step(`^the browser switches to the (home|sibling) account through ([AB])$`, b.switchTo)
	sc.Step(
		`^switching the browser to the (sibling|stranger) account through ([AB]) answers a (\d+) problem "([a-z-]+)"$`,
		b.switchRefused,
	)
	sc.Step(`^the account read through ([AB]) in the browser is the (home|sibling) account$`,
		b.account)
	sc.Step(
		`^the browser asks through ([AB]) for a CLI token sealed to a new RSA key, as token "(\w+)"$`,
		b.cliToken,
	)
	sc.Step(
		`^token "(\w+)" stands for an INFRAWEAVECTL session whose parent is the browser's, ending when it does$`,
		b.cliSession,
	)
	sc.Step(
		`^asking through ([AB]) in the browser for a CLI token sealed to "([^"]*)" answers a (\d+) problem "([a-z-]+)"$`,
		b.cliTokenRefused,
	)
	sc.Step(
		`^asking through ([AB]) with token "(\w+)" for a CLI token answers a (\d+) problem "([a-z-]+)"$`,
		b.cliTokenWithToken,
	)
}

// serversWithBudget starts the operator key's servers with an expensive
// budget of burst that refills too slowly to matter in a scenario.
func (b *browserSteps) serversWithBudget(ctx context.Context, burst int) error {
	b.w.Settings["auth"] = harness.OperatorAuth()
	rateLimit := map[string]any{"expensive": map[string]any{"rate": 1e-6, "burst": burst}}
	for range 2 {
		if err := b.w.Start(ctx, rateLimit); err != nil {
			return err
		}
	}
	return nil
}

// origins are the Origin headers a step sends.
var origins = map[string]string{
	"with the public URL's Origin": harness.PublicURL,
	"without an Origin":            "",
	"with another site's Origin":   "https://evil.example",
}

// postForm posts the sign-in form through the jar, with origin when it is
// not empty.
func (b *browserSteps) postForm(
	ctx context.Context,
	replica, slug, login, password, origin string,
) (harness.Response, error) {
	form := url.Values{"login": {login}, "password": {password}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		b.w.Base(replica)+"/admin-login/"+slug, strings.NewReader(form))
	if err != nil {
		return harness.Response{}, fmt.Errorf("sign-in request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := b.w.Jar.Do(req)
	if err != nil {
		return harness.Response{}, fmt.Errorf("sign in: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return harness.Response{}, fmt.Errorf("sign in: %w", err)
	}
	r := harness.Response{
		Status: resp.StatusCode,
		Header: resp.Header,
		Body:   body,
		Err:    sdk.Check(resp, body),
	}
	b.w.Sent = append(b.w.Sent, r)
	return r, nil
}

func (b *browserSteps) signIn(ctx context.Context, slug, login, password, replica string) error {
	r, err := b.postForm(ctx, replica, slug, login, password, harness.PublicURL)
	if err != nil {
		return err
	}
	if _, ok := b.w.Jar.Session(); r.Status != http.StatusSeeOther ||
		r.Header.Get("Location") != "/" ||
		!ok {
		return fmt.Errorf(
			"%w: sign-in answered %d %v %s",
			harness.ErrStep,
			r.Status,
			r.Header,
			r.Body,
		)
	}
	return nil
}

func (b *browserSteps) signInRefused(
	ctx context.Context,
	slug, login, password, replica, origin, code, slug2 string,
) error {
	r, err := b.postForm(ctx, replica, slug, login, password, origins[origin])
	if err != nil {
		return err
	}
	if r.Header.Get("Set-Cookie") != "" || r.Header.Get("WWW-Authenticate") != "" {
		return fmt.Errorf("%w: a refused sign-in set %q, challenged %q", harness.ErrStep,
			r.Header.Get("Set-Cookie"), r.Header.Get("WWW-Authenticate"))
	}
	return harness.IsProblem(r, "signing in to "+slug, code, slug2)
}

func (b *browserSteps) holdsCookie() error {
	c := b.w.Jar.SetCookie()
	if c == nil || c.Value == "" || !c.HttpOnly || !c.Secure ||
		c.SameSite != http.SameSiteLaxMode ||
		c.Path != "/" ||
		c.Domain != "" ||
		c.MaxAge <= 0 {
		return fmt.Errorf("%w: Set-Cookie %+v", harness.ErrStep, c)
	}
	return nil
}

type browserUser struct {
	ID         string `json:"id"`
	Admin      bool   `json:"admin"`
	JWT        string `json:"jwt"`
	ValidUntil int64  `json:"validUntil"`
}

func (b *browserSteps) viewerAdmin(ctx context.Context, replica, login string) error {
	r, err := b.w.BrowserDo(ctx, replica, http.MethodGet, "/api/v1/viewer", "", nil)
	if err != nil {
		return err
	}
	var u browserUser
	if err := json.Unmarshal(r.Body, &u); err != nil || r.Status != http.StatusOK ||
		u.ID != login || !u.Admin || u.JWT != "" || u.ValidUntil == 0 {
		return fmt.Errorf("%w: viewer answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	return nil
}

func (b *browserSteps) answersProblem(
	ctx context.Context,
	method, path, replica, origin, code, slug string,
) error {
	r, err := b.w.BrowserDo(ctx, replica, method, path, origins[origin], nil)
	if err != nil {
		return err
	}
	return harness.IsProblem(r, method+" "+path, code, slug)
}

// switcher checks GET /auth/accounts against want, no account or the
// quoted slugs joined by ", ".
func (b *browserSteps) switcher(ctx context.Context, replica, want string) error {
	r, err := b.w.BrowserDo(ctx, replica, http.MethodGet, "/api/v1/auth/accounts", "", nil)
	if err != nil {
		return err
	}
	var accounts []struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(r.Body, &accounts); err != nil || r.Status != http.StatusOK {
		return fmt.Errorf("%w: authAccounts answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	slugs := make([]string, 0, len(accounts))
	for _, a := range accounts {
		slugs = append(slugs, a.Slug)
	}
	got := `"` + strings.Join(slugs, ", ") + `"`
	if len(slugs) == 0 {
		got = "no account"
	}
	if got != want {
		return fmt.Errorf("%w: the switcher lists %s, want %s", harness.ErrStep, got, want)
	}
	return nil
}

func (b *browserSteps) logout(ctx context.Context, replica string) error {
	r, err := b.w.BrowserDo(
		ctx,
		replica,
		http.MethodPost,
		"/api/v1/auth/logout",
		harness.PublicURL,
		nil,
	)
	if err != nil {
		return err
	}
	if r.Status != http.StatusOK {
		return fmt.Errorf("%w: logout answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	return nil
}

func (b *browserSteps) holdsNone() error {
	if v, ok := b.w.Jar.Session(); ok {
		return fmt.Errorf("%w: the browser still holds %q", harness.ErrStep, v)
	}
	return nil
}

// endedRefused waits at most revocationWithin for the replica to refuse
// the cookie a logout or switch ended.
func (b *browserSteps) endedRefused(ctx context.Context, replica string) error {
	ended := b.w.Jar.EndedSession()
	if ended == "" {
		return fmt.Errorf("%w: no cookie has ended", harness.ErrStep)
	}
	old := harness.NewJar()
	old.Hold(ended)
	c, err := sdk.New(b.w.Base(replica), sdk.WithRetry(sdk.NoRetry), sdk.WithHTTPClient(old))
	if err != nil {
		return fmt.Errorf("sdk: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, revocationWithin)
	defer cancel()
	return deps.Eventually(ctx, "the ended cookie to fail at "+replica,
		func(ctx context.Context) (bool, error) {
			resp, err := c.Do(ctx, http.MethodGet, "/api/v1/viewer", nil)
			if resp == nil {
				return false, err
			}
			r := harness.Response{
				Status: resp.Status,
				Header: resp.Header,
				Body:   resp.Body,
				Err:    err,
			}
			return harness.IsProblem(r, "viewer", "401", "unauthenticated") == nil, nil
		})
}

func (b *browserSteps) viewerAnswers(ctx context.Context, replica string, code int) error {
	r, err := b.w.BrowserDo(ctx, replica, http.MethodGet, "/api/v1/viewer", "", nil)
	if err != nil {
		return err
	}
	if r.Status != code {
		return fmt.Errorf(
			"%w: viewer answered %d %s, want %d",
			harness.ErrStep,
			r.Status,
			r.Body,
			code,
		)
	}
	return nil
}

// trail checks the organization's break-glass sign-ins, oldest first, as
// "<result> <login>" joined by ", ".
func (b *browserSteps) trail(ctx context.Context, slug, want string) error {
	db, err := b.w.Querier(ctx)
	if err != nil {
		return err
	}
	got, err := db.Query(ctx, fmt.Sprintf(`SELECT string_agg(
			(body->>'result') || ' ' || (body->>'login'), ', ' ORDER BY created_at, id)
		FROM organization_audit_trail_entries
		WHERE affected_type = 'ORGANIZATION' AND affected_id = '%s' AND actor_kind = 'break_glass'
			AND action = 'organization.break_glass_sign_in' AND event_type = 'NONE'`, slug))
	if err != nil {
		return err //nolint:wrapcheck // the querier names the query
	}
	if strings.TrimSpace(got) != want {
		return fmt.Errorf("%w: trail of %s holds %q, want %q", harness.ErrStep, slug, got, want)
	}
	return nil
}

// newSecret is a cookie value and its SHA-256 in hex, as a sign-in makes
// them.
func newSecret() (string, string) {
	raw := make([]byte, 32)
	_, _ = rand.Read(raw) // crypto/rand never fails
	v := base64.RawURLEncoding.EncodeToString(raw)
	h := sha256.Sum256([]byte(v))
	return v, hex.EncodeToString(h[:])
}

// memberSession seeds the home organization's member with a browser
// session in the home account, admitted to the accounts named and
// space-admin on home's top space, as the member who claimed it is, and
// has the browser hold its cookie.
func (b *browserSteps) memberSession(ctx context.Context, admitted string) error {
	db, err := b.w.Querier(ctx)
	if err != nil {
		return err
	}
	for _, t := range []harness.Tenant{harness.Home, harness.Sibling} {
		if err := t.Seed(ctx, db); err != nil {
			return err
		}
	}
	accounts := []harness.Tenant{harness.Home}
	if admitted == "home and sibling" {
		accounts = append(accounts, harness.Sibling)
	}
	var rows []string
	for _, t := range accounts {
		rows = append(rows, fmt.Sprintf(
			`('%s', '%s', '%s')`,
			harness.UUIDOf(
				t.Account,
			),
			harness.UUIDOf(harness.Home.Org),
			harness.UUIDOf(harness.MemberID),
		))
	}
	value, hash := newSecret()
	// The session ends in whole seconds, as a CLI token's exp counts them.
	if _, err := db.Query(ctx, fmt.Sprintf(
		`
		INSERT INTO members (organization_id, id, login, name, email, status)
			VALUES ('%[1]s', '%[2]s', 'alice', 'Alice', 'alice@example.com', 'active');
		INSERT INTO account_users (account_id, organization_id, member_id) VALUES %[6]s;
		INSERT INTO sessions (organization_id, account_id, id, channel, actor_kind, actor_id,
				member_id, login, name, email, expires_at, idle_expires_at, secret_hash)
			VALUES ('%[1]s', '%[3]s', '%[4]s', 'WEB', 'user', '%[2]s', '%[2]s', 'alice', 'Alice',
				'alice@example.com', date_trunc('second', now()) + interval '1 day',
				now() + interval '12 hours', '\x%[5]s');
		INSERT INTO role_bindings (account_id, id, actor_kind, actor_id, role_id, space_id, origin,
				created_by)
			SELECT '%[3]s', gen_random_uuid(), 'user', '%[2]s', id, '%[3]s', 'binding', 'acceptance'
			FROM roles WHERE account_id = '%[3]s' AND slug = 'space-admin';%[7]s`,
		harness.UUIDOf(
			harness.Home.Org,
		),
		harness.UUIDOf(harness.MemberID),
		harness.UUIDOf(harness.Home.Account),
		harness.UUIDOf(harness.MemberSession),
		hash,
		strings.Join(rows, ", "),
		harness.AuthzChanged(harness.Home.Account),
	)); err != nil {
		return err //nolint:wrapcheck // the querier names the query
	}
	b.w.Jar.Hold(value)
	return nil
}

// unadmitted seeds a member of the home organization no account admits,
// with a browser session of no account, and has the browser hold it.
func (b *browserSteps) unadmitted(ctx context.Context) error {
	db, err := b.w.Querier(ctx)
	if err != nil {
		return err
	}
	if err := harness.Home.Seed(ctx, db); err != nil {
		return err
	}
	const bob, bobSession = "01KACME0000000000000000008", "01KACME0000000000000000009"
	value, hash := newSecret()
	if _, err := db.Query(ctx, fmt.Sprintf(
		`
		INSERT INTO members (organization_id, id, login, name, email, status)
			VALUES ('%[1]s', '%[2]s', 'bob', 'Bob', 'bob@example.com', 'active');
		INSERT INTO sessions (organization_id, id, channel, actor_kind, actor_id, member_id,
				login, name, email, expires_at, idle_expires_at, secret_hash)
			VALUES ('%[1]s', '%[3]s', 'WEB', 'user', '%[2]s', '%[2]s', 'bob', 'Bob',
				'bob@example.com', now() + interval '1 day', now() + interval '12 hours', '\x%[4]s');`,
		harness.UUIDOf(
			harness.Home.Org,
		),
		harness.UUIDOf(bob),
		harness.UUIDOf(bobSession),
		hash,
	)); err != nil {
		return err //nolint:wrapcheck // the querier names the query
	}
	b.w.Jar.Hold(value)
	return nil
}

func (b *browserSteps) switchTo(ctx context.Context, who, replica string) error {
	before, _ := b.w.Jar.Session()
	r, err := b.w.BrowserDo(
		ctx,
		replica,
		http.MethodPost,
		"/api/v1/auth/session/account",
		harness.PublicURL,
		map[string]string{"account": harness.Tenants[who].Account},
	)
	if err != nil {
		return err
	}
	var u browserUser
	after, ok := b.w.Jar.Session()
	if err := json.Unmarshal(r.Body, &u); err != nil || r.Status != http.StatusOK || u.JWT != "" ||
		!ok || after == before {
		return fmt.Errorf(
			"%w: the switch answered %d %v %s",
			harness.ErrStep,
			r.Status,
			r.Header,
			r.Body,
		)
	}
	return nil
}

func (b *browserSteps) switchRefused(ctx context.Context, who, replica, code, slug string) error {
	r, err := b.w.BrowserDo(
		ctx,
		replica,
		http.MethodPost,
		"/api/v1/auth/session/account",
		harness.PublicURL,
		map[string]string{"account": harness.Tenants[who].Account},
	)
	if err != nil {
		return err
	}
	return harness.IsProblem(r, "switching to "+who, code, slug)
}

func (b *browserSteps) account(ctx context.Context, replica, who string) error {
	r, err := b.w.BrowserDo(ctx, replica, http.MethodGet, "/api/v1/id", "", nil)
	if err != nil {
		return err
	}
	var id string
	if err := json.Unmarshal(r.Body, &id); err != nil || r.Status != http.StatusOK ||
		id != harness.Tenants[who].Account {
		return fmt.Errorf("%w: id answered %d %s, want %s", harness.ErrStep, r.Status, r.Body,
			harness.Tenants[who].Account)
	}
	return nil
}

// cliToken asks for a CLI token sealed to a new RSA key and opens it as
// spacectl's loopback listener would, with the standard library alone.
func (b *browserSteps) cliToken(ctx context.Context, replica, name string) error {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("rsa: %w", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return fmt.Errorf("rsa: %w", err)
	}
	pub := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	r, err := b.w.BrowserDo(
		ctx,
		replica,
		http.MethodPost,
		"/api/v1/auth/cli-confirmation-tokens",
		harness.PublicURL,
		map[string]string{"pubKey": base64.URLEncoding.EncodeToString(pub)},
	)
	if err != nil {
		return err
	}
	var sealed string
	if err := json.Unmarshal(r.Body, &sealed); err != nil || r.Status != http.StatusCreated {
		return fmt.Errorf(
			"%w: cliConfirmationToken answered %d %s",
			harness.ErrStep,
			r.Status,
			r.Body,
		)
	}
	tok, err := openSealed(key, sealed)
	if err != nil {
		return fmt.Errorf("%w: %w", harness.ErrStep, err)
	}
	b.w.Tokens[name] = tok
	return nil
}

// openSealed opens <key>.<token>: an AES-256 key wrapped with RSA-OAEP
// over SHA-512, then the token in AES-CBC with a leading IV and PKCS#7
// padding, each in unpadded base64url.
func openSealed(key *rsa.PrivateKey, sealed string) (string, error) {
	k64, t64, ok := strings.Cut(sealed, ".")
	if !ok {
		return "", fmt.Errorf("sealed token %q has no dot", sealed)
	}
	wrapped, err1 := base64.RawURLEncoding.DecodeString(k64)
	box, err2 := base64.RawURLEncoding.DecodeString(t64)
	if err1 != nil || err2 != nil || len(box) < 2*aes.BlockSize || len(box)%aes.BlockSize != 0 {
		return "", fmt.Errorf("sealed token is not two base64url halves of the right size")
	}
	aesKey, err := rsa.DecryptOAEP(sha512.New(), nil, key, wrapped, nil)
	if err != nil {
		return "", fmt.Errorf("unwrap the key: %w", err)
	}
	block, err := aes.NewCipher(aesKey)
	if err != nil || len(aesKey) != 32 {
		return "", fmt.Errorf("the key is not AES-256: %d bytes", len(aesKey))
	}
	iv, text := box[:aes.BlockSize], box[aes.BlockSize:]
	out := make([]byte, len(text))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, text)
	n := int(out[len(out)-1])
	if n == 0 || n > aes.BlockSize ||
		!bytes.Equal(out[len(out)-n:], bytes.Repeat([]byte{byte(n)}, n)) {
		return "", fmt.Errorf("bad PKCS#7 padding")
	}
	return string(out[:len(out)-n]), nil
}

// cliSession checks the token's session row: channel INFRAWEAVECTL, the
// browser session its parent, the home account, and an exp that is the
// parent's absolute end.
func (b *browserSteps) cliSession(ctx context.Context, name string) error {
	var c harness.APIClaims
	if err := harness.JWTPart(b.w.Tokens[name], 1, &c); err != nil {
		return err
	}
	db, err := b.w.Querier(ctx)
	if err != nil {
		return err
	}
	got, err := db.Query(ctx, fmt.Sprintf(`SELECT c.channel, c.parent_session_id = p.id,
			c.account_id = p.account_id, extract(epoch FROM c.expires_at)::bigint,
			extract(epoch FROM p.expires_at)::bigint
		FROM sessions c JOIN sessions p ON p.id = '%s' WHERE c.id = '%s'`,
		harness.UUIDOf(harness.MemberSession), harness.UUIDOf(c.ID)))
	if err != nil {
		return err //nolint:wrapcheck // the querier names the query
	}
	parts := strings.Split(strings.TrimSpace(got), "|")
	if len(parts) != 5 || parts[0] != "INFRAWEAVECTL" || parts[1] != "t" || parts[2] != "t" ||
		parts[3] != parts[4] || parts[4] != strconv.FormatInt(c.Expiry, 10) || c.Kind != "user" {
		return fmt.Errorf("%w: CLI session %q, claims %+v", harness.ErrStep, got, c)
	}
	return nil
}

func (b *browserSteps) cliTokenRefused(ctx context.Context, replica, key, code, slug string) error {
	r, err := b.w.BrowserDo(
		ctx,
		replica,
		http.MethodPost,
		"/api/v1/auth/cli-confirmation-tokens",
		harness.PublicURL,
		map[string]string{"pubKey": key},
	)
	if err != nil {
		return err
	}
	if err := harness.IsProblem(r, "cliConfirmationToken", code, slug); err != nil {
		return err
	}
	if p, _ := sdk.AsProblem(r.Err); !strings.HasPrefix(p.Detail, "pubKey: ") {
		return fmt.Errorf("%w: the problem does not name pubKey: %q", harness.ErrStep, p.Detail)
	}
	return nil
}

func (b *browserSteps) cliTokenWithToken(
	ctx context.Context,
	replica, name, code, slug string,
) error {
	r, err := b.w.Do(ctx, replica, name, http.MethodPost, "/api/v1/auth/cli-confirmation-tokens",
		map[string]string{"pubKey": "unused"})
	if err != nil {
		return err
	}
	return harness.IsProblem(r, "cliConfirmationToken with token "+name, code, slug)
}
