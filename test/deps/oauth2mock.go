package deps

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// oauth2MockConfig turns on the sign-in form, which the image shows by
// default, and TLS with a certificate the server generates at start, for
// localhost and 127.0.0.1.
const oauth2MockConfig = `{"interactiveLogin":true,"httpServer":{"type":"NettyWrapper","ssl":{}}}`

// OAuth2Mock is navikt's mock-oauth2-server in a container: an OpenID
// Provider whose every path segment is an issuer of its own, with its own
// keys, so each scenario signs in at an issuer no other shares. Its
// authorization endpoint shows a form that takes the subject and a JSON
// object of claims, which SignIn fills in.
type OAuth2Mock struct {
	base string
	cert []byte
	pool *x509.CertPool
}

// OAuth2Mock returns the shared container, starting it on first use.
func (d *Dependencies) OAuth2Mock(ctx context.Context) (*OAuth2Mock, error) {
	d.mockOnce.Do(func() {
		c, err := testcontainers.Run(ctx, ImageOAuth2Mock,
			testcontainers.WithExposedPorts("8080/tcp"),
			testcontainers.WithEnv(map[string]string{"JSON_CONFIG": oauth2MockConfig}),
			testcontainers.WithWaitStrategy(wait.ForHTTP("/default/.well-known/openid-configuration").
				WithPort("8080/tcp").
				// The certificate is what the server answers here; it is
				// trusted only once read.
				WithTLS(true, &tls.Config{InsecureSkipVerify: true}). //nolint:gosec // read before trusted
				WithStartupTimeout(2*time.Minute)),
		)
		if c != nil {
			d.OnClose(func(ctx context.Context) error { return c.Terminate(ctx) })
		}
		if err != nil {
			d.mockErr = fmt.Errorf("@oidc: %w", err)
			return
		}
		base, err := c.PortEndpoint(ctx, "8080/tcp", "https")
		if err != nil {
			d.mockErr = fmt.Errorf("@oidc: %w", err)
			return
		}
		d.mock, d.mockErr = newOAuth2Mock(ctx, base)
	})
	return d.mock, d.mockErr
}

// newOAuth2Mock reads the certificate the server at base presents, which
// it generated at start and signed itself, and trusts it from then on.
func newOAuth2Mock(ctx context.Context, base string) (*OAuth2Mock, error) {
	u, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("@oidc: %w", err)
	}
	dialer := &tls.Dialer{Config: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec // read before trusted
	conn, err := dialer.DialContext(ctx, "tcp", u.Host)
	if err != nil {
		return nil, fmt.Errorf("@oidc: %w", err)
	}
	defer conn.Close()
	tc, ok := conn.(*tls.Conn)
	if !ok || len(tc.ConnectionState().PeerCertificates) == 0 {
		return nil, fmt.Errorf("%w: %s presents no certificate", ErrIssuer, base)
	}
	leaf := tc.ConnectionState().PeerCertificates[0]
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return &OAuth2Mock{
		base: strings.TrimSuffix(base, "/"),
		cert: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw}),
		pool: pool,
	}, nil
}

// Issuer is the identifier of the issuer at path segment name, which is
// also its base URL.
func (m *OAuth2Mock) Issuer(name string) string { return m.base + "/" + name }

// CertPEM is the server's certificate, which a client must trust to reach
// it.
func (m *OAuth2Mock) CertPEM() []byte { return m.cert }

// Client is an HTTP client that trusts the server.
func (m *OAuth2Mock) Client() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // the standard library's
	t.TLSClientConfig = &tls.Config{RootCAs: m.pool, MinVersion: tls.VersionTLS12}
	return &http.Client{Transport: t, Timeout: 30 * time.Second}
}

// Login is what a user types into an issuer's sign-in form: navikt's takes
// the subject and a JSON object of claims. The in-process issuer shows no
// form, and signs in the identity queued with it instead.
type Login struct {
	Subject string
	Claims  map[string]any
}

// maxHops bounds the redirects SignIn follows at the issuer.
const maxHops = 10

// SignIn plays the browser at an issuer: it sends the authorization
// request authorizeURL with client, which must trust the issuer, fills in
// the issuer's sign-in form with login when it shows one, and follows the
// issuer's redirects until one leaves the issuer's host. It answers that
// redirect's target: the relying party's redirect URI with a code or an
// error, and the state.
func SignIn(ctx context.Context, client *http.Client, authorizeURL string, login Login) (*url.URL, error) {
	at, err := url.Parse(authorizeURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrIssuer, err)
	}
	host := at.Host
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, at.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrIssuer, err)
	}
	for range maxHops {
		resp, err := c.Do(req)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrIssuer, req.URL.Redacted(), err)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrIssuer, req.URL.Redacted(), err)
		}
		switch {
		case resp.StatusCode >= 300 && resp.StatusCode < 400:
			next, err := req.URL.Parse(resp.Header.Get("Location"))
			if err != nil {
				return nil, fmt.Errorf("%w: redirect from %s: %w", ErrIssuer, req.URL.Redacted(), err)
			}
			if next.Host != host {
				return next, nil
			}
			req, err = http.NewRequestWithContext(ctx, http.MethodGet, next.String(), nil)
			if err != nil {
				return nil, fmt.Errorf("%w: %w", ErrIssuer, err)
			}
		case resp.StatusCode == http.StatusOK && signInForm(body):
			req, err = loginRequest(ctx, req.URL, login)
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("%w: %s answered %d: %.200s", ErrIssuer, req.URL.Redacted(), resp.StatusCode, body)
		}
	}
	return nil, fmt.Errorf("%w: more than %d redirects at %s", ErrIssuer, maxHops, host)
}

// signInForm reports whether page is navikt's sign-in form, which posts a
// username to the page's own URL.
func signInForm(page []byte) bool {
	s := string(page)
	return strings.Contains(s, "<form") && strings.Contains(s, `name="username"`)
}

// loginRequest posts login to the sign-in form at u.
func loginRequest(ctx context.Context, u *url.URL, login Login) (*http.Request, error) {
	claims := login.Claims
	if claims == nil {
		claims = map[string]any{}
	}
	raw, err := json.Marshal(claims)
	if err != nil {
		return nil, fmt.Errorf("%w: claims: %w", ErrIssuer, err)
	}
	form := url.Values{"username": {login.Subject}, "claims": {string(raw)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrIssuer, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req, nil
}
