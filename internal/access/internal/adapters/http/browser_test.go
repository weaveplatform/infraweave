package http

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authn"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/breakglass"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/loopback"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

const bgPassword = "correct horse battery staple"

// credential is bgPassword's hash, made once: each costs 600,000 PBKDF2
// iterations.
var credential = sync.OnceValue(func() breakglass.Hash {
	h, err := breakglass.New(bgPassword)
	if err != nil {
		panic(err)
	}
	return h
})

func TestUnitCookies(t *testing.T) {
	ss := session.Session{CreatedAt: t0, ExpiresAt: t0.Add(maxAge)}
	set, err := http.ParseSetCookie(sessionCookie(authn.Browser{Secret: "s3cret", Session: ss}))
	if err != nil {
		t.Fatal(err)
	}
	if set.Name != "__Host-infraweave_session" || set.Value != "s3cret" || set.Path != "/" ||
		set.Domain != "" || !set.HttpOnly || !set.Secure || set.SameSite != http.SameSiteLaxMode ||
		set.MaxAge != int(maxAge.Seconds()) {
		t.Errorf("session cookie = %+v", set)
	}
	cleared, err := http.ParseSetCookie(clearedCookie())
	if err != nil {
		t.Fatal(err)
	}
	attrs := cleared.Path == "/" && cleared.HttpOnly && cleared.Secure &&
		cleared.SameSite == http.SameSiteLaxMode
	if cleared.Name != set.Name || cleared.Value != "" || cleared.MaxAge >= 0 || !attrs {
		t.Errorf("cleared cookie = %+v", cleared)
	}
}

func TestUnitOrigin(t *testing.T) {
	for in, want := range map[string]string{
		"http://infraweave.test":            "http://infraweave.test",
		"https://iw.example.com:8443/base/": "https://iw.example.com:8443",
		"/relative":                         "",
		"::not a url":                       "",
	} {
		if got := Origin(in); got != want {
			t.Errorf("Origin(%q) = %q, want %q", in, got, want)
		}
	}
}

// member gives w a member's browser session in acct, admitted to admits,
// and answers it with its cookie value.
func (w *world) member(
	acct kernel.AccountID,
	admits ...session.Admitted,
) (session.Session, string) {
	secret, hash := session.NewSecret()
	ss := session.Session{
		ID: kernel.NewULID(), OrganizationID: kernel.NewOrganizationID(), AccountID: acct,
		Channel: session.ChannelWeb, ActorKind: kernel.PrincipalUser, ActorID: kernel.NewULID(),
		MemberID: kernel.NewMemberID(), Login: "alice", Name: "Alice", CreatedAt: t0,
		ExpiresAt: t0.Add(maxAge), IdleExpiresAt: t0.Add(idle), SecretHash: hash,
	}
	w.st.sessions[ss.ID] = ss
	w.st.admits[ss.MemberID] = admits
	return ss, secret
}

// TestUnitOriginRule is decision 0006's table: a cookie authenticates a
// safe method whatever its Origin, and a write only with the public URL's.
func TestUnitOriginRule(t *testing.T) {
	w := newWorld(t)
	_, secret := w.member(w.acct)
	auth := authn.New(authn.Deps{
		Sessions: w.st, Tokens: w.st, Idle: idle, MaxAge: maxAge,
		Now: func() time.Time { return t0 },
	})
	h := NewAuthenticator(auth, publicURL+"/").Middleware(
		func(*http.Request) accessapi.Access { return accessapi.Account },
	)(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if !from(r.Context()).browser {
			t.Error("a cookie's request is not marked a browser's")
		}
		rw.WriteHeader(http.StatusNoContent)
	}))
	for _, method := range []string{
		http.MethodGet, http.MethodHead, http.MethodOptions,
		http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
	} {
		safe := method == http.MethodGet || method == http.MethodHead ||
			method == http.MethodOptions
		for origin, ok := range map[string]bool{
			"":                          safe,
			"https://evil.example":      safe,
			"http://infraweave.test.ev": safe,
			"https://infraweave.test":   safe,
			publicURL:                   true,
		} {
			req := httptest.NewRequestWithContext(t.Context(), method, "/x", nil)
			req.AddCookie(&http.Cookie{Name: session.CookieName, Value: secret})
			if origin != "" {
				req.Header.Set("Origin", origin)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if (rec.Code == http.StatusNoContent) != ok {
				t.Errorf("%s with Origin %q: %d, want ok %t", method, origin, rec.Code, ok)
			}
			if !ok {
				wantUnauthenticated(t, rec, true)
			}
		}
	}
	// No public URL admits no cookie write at all; an empty cookie is none.
	h = NewAuthenticator(auth, "").Middleware(
		func(*http.Request) accessapi.Access { return accessapi.Account },
	)(http.NotFoundHandler())
	for _, c := range []struct{ method, value string }{
		{http.MethodPost, secret}, {http.MethodGet, ""},
	} {
		req := httptest.NewRequestWithContext(t.Context(), c.method, "/x", nil)
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: c.value})
		req.Header.Set("Origin", "")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %q with no public URL: %d", c.method, c.value, rec.Code)
		}
	}
}

// withBreakGlass gives w the organization slug with the credential login.
func (w *world) withBreakGlass(slug, login string) organization.Organization {
	o := organization.Organization{ID: kernel.NewOrganizationID(), Slug: kernel.Slug(slug)}
	w.st.orgs[o.Slug] = o
	w.st.creds[o.Slug] = &breakglass.Credential{Login: login, Hash: credential()}
	return o
}

// signIn posts the login form with origin, and answers the response.
func (w *world) signIn(
	t *testing.T,
	slug, login, password, origin string,
) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"login": {login}, "password": {password}}.Encode()
	return w.send(t, http.MethodPost, "/admin-login/"+slug, "", func(req *http.Request) {
		req.Body = formBody(form)
		req.ContentLength = int64(len(form))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
	})
}

func TestUnitBreakGlassLogin(t *testing.T) {
	w := newWorld(t)
	w.withBreakGlass("acme", "admin")

	form := w.do(t, http.MethodGet, "/admin-login/acme", "", "")
	if form.Code != http.StatusOK || !strings.Contains(form.Body.String(), `name="password"`) ||
		!strings.HasPrefix(form.Header().Get("Content-Type"), "text/html") ||
		!strings.Contains(form.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") ||
		form.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("form: %d %v", form.Code, form.Header())
	}
	escaped := w.do(t, http.MethodGet, "/admin-login/%3Cscript%3E", "", "")
	if strings.Contains(escaped.Body.String(), "<script>") {
		t.Error("the form does not escape the organization")
	}

	for name, c := range map[string]struct{ login, password, origin string }{
		"no Origin":      {"admin", bgPassword, ""},
		"another Origin": {"admin", bgPassword, "https://evil.example"},
		"wrong password": {"admin", "incorrect horse battery staple", publicURL},
		"wrong login":    {"root", bgPassword, publicURL},
	} {
		rec := w.signIn(t, "acme", c.login, c.password, c.origin)
		wantUnauthenticated(t, rec, false)
		if rec.Header().Get("Set-Cookie") != "" {
			t.Errorf("%s set a cookie", name)
		}
	}
	wantUnauthenticated(t, w.signIn(t, "initech", "admin", bgPassword, publicURL), false)

	rec := w.signIn(t, "acme", "admin", bgPassword, publicURL)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("sign-in: %d %v", rec.Code, rec.Header())
	}
	set, err := http.ParseSetCookie(rec.Header().Get("Set-Cookie"))
	if err != nil || set.Name != session.CookieName || !set.HttpOnly || !set.Secure {
		t.Fatalf("Set-Cookie = %+v, %v", set, err)
	}
	for _, ss := range w.st.sessions {
		if ss.ActorKind == kernel.PrincipalBreakGlass && ss.IPAddress != "192.0.2.9" {
			t.Errorf("the session's address is %q", ss.IPAddress)
		}
	}

	u := decode[api.User](t, w.browse(t, http.MethodGet, "/viewer", set.Value, "", ""))
	if !u.Admin || u.Id != "admin" || u.Jwt != "" || u.ValidUntil != int32(t0.Add(maxAge).Unix()) {
		t.Errorf("viewer = %+v", u)
	}
	wantUnauthenticated(t, w.browse(t, http.MethodGet, "/sessions", set.Value, "", ""), true)
	if as := decode[[]api.AuthAccount](
		t, w.browse(t, http.MethodGet, "/auth/accounts", set.Value, "", ""),
	); len(as) != 0 {
		t.Errorf("a break-glass session's switcher = %+v", as)
	}

	out := w.browse(t, http.MethodPost, "/auth/logout", set.Value, publicURL, "")
	cleared, err := http.ParseSetCookie(out.Header().Get("Set-Cookie"))
	if out.Code != http.StatusOK || err != nil || cleared.MaxAge >= 0 {
		t.Errorf("logout: %d, Set-Cookie %+v, %v", out.Code, cleared, err)
	}
	wantUnauthenticated(t, w.browse(t, http.MethodGet, "/viewer", set.Value, "", ""), true)

	big := w.send(t, http.MethodPost, "/admin-login/acme", "", func(req *http.Request) {
		body := "login=" + strings.Repeat("a", maxForm)
		req.Body, req.ContentLength = formBody(body), int64(len(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", publicURL)
	})
	if big.Code != http.StatusBadRequest {
		t.Errorf("an oversized form: %d", big.Code)
	}
	w.st.down = true
	if rec := w.signIn(
		t,
		"acme",
		"admin",
		bgPassword,
		publicURL,
	); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("a store down: %d %s", rec.Code, rec.Body)
	}
}

// rsaKey is a 2048-bit public key in PEM, the smallest the seal accepts.
var rsaKey = sync.OnceValue(func() string {
	k, err := rsa.GenerateKey(rand.Reader, loopback.MinKeyBits)
	if err != nil {
		panic(err)
	}
	der, _ := x509.MarshalPKIXPublicKey(&k.PublicKey)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
})

func TestUnitCliConfirmationToken(t *testing.T) {
	w := newWorld(t)
	_, secret := w.member(w.acct)
	body := fmt.Sprintf(`{"pubKey":%q}`, rsaKey())
	const path = "/auth/cli-confirmation-tokens"

	rec := w.browse(t, http.MethodPost, path, secret, publicURL, body)
	sealed := decode[string](t, rec)
	if rec.Code != http.StatusCreated || strings.Count(sealed, ".") != 1 {
		t.Errorf("cliConfirmationToken: %d %q", rec.Code, sealed)
	}
	bad := w.browse(t, http.MethodPost, path, secret, publicURL, `{"pubKey":"nope"}`)
	if p := decode[problem.Problem](t, bad); bad.Code != http.StatusUnprocessableEntity ||
		!strings.HasPrefix(p.Detail, "pubKey: ") {
		t.Errorf("a bad key: %d %+v", bad.Code, p)
	}
	if rec := w.do(
		t,
		http.MethodPost,
		path,
		"Bearer "+w.exchange(t).Jwt,
		body,
	); rec.Code != http.StatusForbidden {
		t.Errorf("an API token: %d %s", rec.Code, rec.Body)
	}
	ending, endingSecret := w.member(w.acct)
	ending.ExpiresAt = t0.Add(500 * time.Millisecond)
	w.st.sessions[ending.ID] = ending
	wantUnauthenticated(t, w.browse(t, http.MethodPost, path, endingSecret, publicURL, body), false)

	w.st.down = true
	if rec := w.browse(
		t,
		http.MethodPost,
		path,
		secret,
		publicURL,
		body,
	); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("a store down: %d %s", rec.Code, rec.Body)
	}
}

func TestUnitSwitchAccount(t *testing.T) {
	w := newWorld(t)
	home := session.Admitted{ID: w.acct, Slug: "home", Name: "Home"}
	sibling := session.Admitted{ID: kernel.NewAccountID(), Slug: "sibling", Name: "Sibling"}
	cur, secret := w.member(w.acct, home, sibling)

	as := decode[[]api.AuthAccount](
		t,
		w.browse(t, http.MethodGet, "/auth/accounts", secret, "", ""),
	)
	if len(as) != 2 ||
		as[1] != (api.AuthAccount{Id: sibling.ID.String(), Slug: "sibling", Name: "Sibling"}) {
		t.Errorf("authAccounts = %+v", as)
	}

	const path = "/auth/session/account"
	to := func(acct string) string { return fmt.Sprintf(`{"account":%q}`, acct) }
	rec := w.browse(t, http.MethodPost, path, secret, publicURL, to(sibling.ID.String()))
	u := decode[api.User](t, rec)
	set, err := http.ParseSetCookie(rec.Header().Get("Set-Cookie"))
	if rec.Code != http.StatusOK || u.Jwt != "" || err != nil || set.Value == secret ||
		set.MaxAge != int(cur.ExpiresAt.Sub(t0).Seconds()) {
		t.Fatalf("switch: %d %+v, Set-Cookie %+v, %v", rec.Code, u, set, err)
	}
	wantUnauthenticated(t, w.browse(t, http.MethodGet, "/viewer", secret, "", ""), true)
	if rec := w.browse(t, http.MethodGet, "/viewer", set.Value, "", ""); rec.Code != http.StatusOK {
		t.Errorf("the new cookie: %d", rec.Code)
	}
	for _, acct := range []string{"nope", kernel.NewAccountID().String()} {
		if rec := w.browse(
			t,
			http.MethodPost,
			path,
			set.Value,
			publicURL,
			to(acct),
		); rec.Code != http.StatusNotFound {
			t.Errorf("to %s: %d %s", acct, rec.Code, rec.Body)
		}
	}

	// A token session's switch answers a new token and sets no cookie.
	cli := cur
	cli.ID, cli.Channel, cli.SecretHash = kernel.NewULID(), session.ChannelCLI, nil
	cli.ExpiresAt, cli.IdleExpiresAt = t0.Add(time.Hour), time.Time{}
	w.st.sessions[cli.ID] = cli
	bearer := fmt.Sprintf("Bearer user %s %s", cli.ID, audience)
	trec := w.do(t, http.MethodPost, path, bearer, to(home.ID.String()))
	if tu := decode[api.User](t, trec); trec.Code != http.StatusOK || tu.Jwt == "" ||
		trec.Header().Get("Set-Cookie") != "" {
		t.Errorf("a token switch: %d %+v", trec.Code, tu)
	}
	short := cli
	short.ID, short.ExpiresAt = kernel.NewULID(), t0.Add(500*time.Millisecond)
	w.st.sessions[short.ID] = short
	wantUnauthenticated(t, w.do(t, http.MethodPost, path,
		fmt.Sprintf("Bearer user %s %s", short.ID, audience), to(home.ID.String())), false)

	w.st.down = true
	for _, r := range [][2]string{{http.MethodGet, "/auth/accounts"}, {http.MethodPost, path}} {
		if rec := w.browse(
			t,
			r[0],
			r[1],
			set.Value,
			publicURL,
			to(home.ID.String()),
		); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s with a store down: %d %s", r[0], r[1], rec.Code, rec.Body)
		}
	}
}

// formBody is s as a request body.
func formBody(s string) io.ReadCloser { return io.NopCloser(strings.NewReader(s)) }
