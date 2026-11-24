package deps

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// oidcKeyID names the issuer's first signing key in its JWKS and its
// tokens; Rotate numbers the keys after it.
const oidcKeyID = "acceptance"

// BackchannelLogoutEvent is the events member that marks a logout token.
//
// ref: OpenID Connect Back-Channel Logout 1.0, section 2.4
const BackchannelLogoutEvent = "http://schemas.openid.net/event/backchannel-logout"

// Issuer is the @oidc dependency run in process: an OpenID Provider on
// loopback, over TLS as infraweave fetches issuers over HTTPS only, on chi.
// It publishes its discovery document and JWKS, mints RS256 tokens with
// whatever claims a scenario chooses, and runs the authorization code flow
// with PKCE for the identities a scenario queues, with the faults each
// chooses. A server trusts it through CertPEM.
//
// Its JWKS carries the signing key's self-signed certificate as x5c and its
// SHA-256 thumbprint as x5t#S256, as Entra ID, Auth0 and GitHub Actions
// publish theirs, so every @oidc scenario parses a key set with certificate
// members; under the certified FIPS mode a parser that digests x5c with
// SHA-1 fails them. It publishes no x5t, which is a SHA-1 digest this
// process cannot compute in that mode.
type Issuer struct {
	srv *httptest.Server
	// unpublished signs the tokens of FaultUnknownKey: the JWKS never
	// carries it.
	unpublished *rsa.PrivateKey

	mu       sync.Mutex
	key      *rsa.PrivateKey
	kid      string
	cert     []byte
	certs    bool
	rotated  int
	clients  map[string]string
	queue    []Identity
	codes    map[string]*grant
	requests []AuthorizeRequest
	logouts  []LogoutRequest
	issued   []IssuedToken
	// fetches counts the JWKS's reads, so a scenario sees a relying party
	// refetch the key set, or not.
	fetches int
}

// Identity is who the issuer signs in at its next authorization request:
// the subject and the claims it adds to the ID token, which override the
// standard ones. A claim's name and presence are the identity's choice, so
// a groups claim under another name, or an ID token without
// email_verified, is a matter of Claims.
type Identity struct {
	Subject string
	Claims  map[string]any
	// AuthTime is when the user authenticated; zero is the time of the
	// authorization request.
	AuthTime time.Time
	// SID is the session the ID token names; empty is a fresh one.
	SID string
	// Faults are what the issuer gets wrong for this sign-in.
	Faults []Fault
}

// Fault is a way the issuer misbehaves for one sign-in, which a relying
// party must refuse or survive.
type Fault string

// The faults a sign-in can choose.
const (
	// FaultWrongNonce answers an ID token whose nonce is not the request's.
	FaultWrongNonce Fault = "wrong-nonce"
	// FaultWrongIssuer answers an ID token from another issuer identifier.
	FaultWrongIssuer Fault = "wrong-issuer"
	// FaultWrongAudience answers an ID token for another client.
	FaultWrongAudience Fault = "wrong-audience"
	// FaultHS256 signs the ID token with HMAC-SHA256 keyed with the
	// issuer's public key in PEM, the algorithm confusion attack.
	FaultHS256 Fault = "hs256"
	// FaultAlgNone answers an unsigned ID token with alg none.
	FaultAlgNone Fault = "alg-none"
	// FaultUnknownKey signs with a key the JWKS never publishes.
	FaultUnknownKey Fault = "unknown-kid"
	// FaultEmptySubject answers an ID token whose sub is empty.
	FaultEmptySubject Fault = "empty-sub"
	// FaultAudiencesWithoutAZP answers an ID token for the client and
	// another audience, without azp.
	FaultAudiencesWithoutAZP Fault = "audiences-without-azp"
	// FaultNoAuthTime answers an ID token without auth_time.
	FaultNoAuthTime Fault = "no-auth-time"
	// FaultOldAuthTime answers an ID token whose auth_time is an hour
	// before the request, as an issuer that ignores max_age=0 does.
	FaultOldAuthTime Fault = "old-auth-time"
	// FaultGroupsOverage replaces the groups claim with Entra ID's
	// overage indication, _claim_names and _claim_sources, which Entra
	// sends for a user in more than 200 groups.
	FaultGroupsOverage Fault = "groups-overage"
	// FaultTokenUnavailable makes the token endpoint answer 503.
	FaultTokenUnavailable Fault = "token-unavailable"
	// FaultSlowToken makes the token endpoint send its headers and then
	// withhold its body for SlowToken, or until the client goes away.
	FaultSlowToken Fault = "slow-token"
)

// Faults are every fault a sign-in can choose, by name.
var Faults = map[string]Fault{}

func init() {
	for _, f := range []Fault{
		FaultWrongNonce, FaultWrongIssuer, FaultWrongAudience, FaultHS256, FaultAlgNone,
		FaultUnknownKey, FaultEmptySubject, FaultAudiencesWithoutAZP, FaultNoAuthTime,
		FaultOldAuthTime, FaultGroupsOverage, FaultTokenUnavailable, FaultSlowToken,
	} {
		Faults[string(f)] = f
	}
}

// SlowToken is how long FaultSlowToken withholds the token response's
// body: longer than any relying party waits for the token endpoint.
const SlowToken = time.Minute

// AuthorizeRequest is an authorization request as the issuer received it.
type AuthorizeRequest struct {
	ClientID, RedirectURI, Scope, State, Nonce string
	CodeChallenge, CodeChallengeMethod         string
	Prompt                                     string
	// MaxAge is the max_age parameter, or -1 when the request has none or
	// it is not a number.
	MaxAge int
}

// LogoutRequest is a request to the issuer's end_session_endpoint.
type LogoutRequest struct {
	IDTokenHint, ClientID, PostLogoutRedirectURI, State string
}

// IssuedToken is an ID token the token endpoint answered.
type IssuedToken struct {
	ClientID, Subject, SID, Nonce, Token string
}

// grant is an authorization code's request, identity and time.
type grant struct {
	req      AuthorizeRequest
	identity Identity
	at       time.Time
}

// ErrIssuer reports a call to or from an issuer that failed.
var ErrIssuer = errors.New("oidc issuer")

// StartIssuer starts an issuer with a fresh 2048-bit key.
func StartIssuer() (*Issuer, error) {
	unpublished, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("oidc key: %w", err)
	}
	iss := &Issuer{
		unpublished: unpublished,
		certs:       true,
		clients:     map[string]string{},
		codes:       map[string]*grant{},
	}
	if err := iss.newKey(oidcKeyID); err != nil {
		return nil, err
	}
	r := chi.NewRouter()
	r.Get("/.well-known/openid-configuration", iss.discovery)
	r.Get("/jwks", iss.jwks)
	r.Get("/authorize", iss.authorize)
	r.Post("/token", iss.token)
	r.Get("/logout", iss.logout)
	iss.srv = httptest.NewTLSServer(r)
	return iss, nil
}

// newKey makes a fresh key named kid the one the issuer signs with and
// publishes.
func (i *Issuer) newKey(kid string) error {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("oidc key: %w", err)
	}
	cert, err := selfSigned(key)
	if err != nil {
		return err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.key, i.kid, i.cert = key, kid, cert
	return nil
}

// Rotate replaces the signing key with a fresh one under a new kid. The
// JWKS publishes only the new key, so a token the old key signed no longer
// verifies, and a relying party that cached the old JWKS must fetch it
// again for the new kid.
func (i *Issuer) Rotate() error {
	i.mu.Lock()
	i.rotated++
	kid := oidcKeyID + "-" + strconv.Itoa(i.rotated)
	i.mu.Unlock()
	return i.newKey(kid)
}

// KeyID is the kid of the key the issuer signs with.
func (i *Issuer) KeyID() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.kid
}

// PublishCertificates chooses whether the JWKS carries x5c and x5t#S256
// beside the key, as it does from the start.
func (i *Issuer) PublishCertificates(on bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.certs = on
}

// RegisterClient admits a client to the authorization code flow with its
// secret, which the token endpoint takes as HTTP Basic or in the form.
func (i *Issuer) RegisterClient(id, secret string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.clients[id] = secret
}

// SignInNext queues id as who the next authorization request signs in.
// Requests take the queued identities in order; one with none queued
// answers login_required.
func (i *Issuer) SignInNext(id Identity) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.queue = append(i.queue, id)
}

// Requests are the authorization requests the issuer received, in order.
func (i *Issuer) Requests() []AuthorizeRequest {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]AuthorizeRequest(nil), i.requests...)
}

// Logouts are the requests to the end_session_endpoint, in order.
func (i *Issuer) Logouts() []LogoutRequest {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]LogoutRequest(nil), i.logouts...)
}

// JWKSFetches is how many times the JWKS was read.
func (i *Issuer) JWKSFetches() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.fetches
}

// Issued are the ID tokens the token endpoint answered, in order.
func (i *Issuer) Issued() []IssuedToken {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]IssuedToken(nil), i.issued...)
}

// selfSigned is the DER of a self-signed certificate for key, the x5c an
// issuer publishes beside the key.
func selfSigned(key *rsa.PrivateKey) ([]byte, error) {
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "acceptance issuer"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("oidc certificate: %w", err)
	}
	return der, nil
}

// URL is the issuer identifier, which is also its base URL.
func (i *Issuer) URL() string { return i.srv.URL }

// CertPEM is the issuer's certificate, which a client must trust to reach
// it.
func (i *Issuer) CertPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: i.srv.Certificate().Raw})
}

// Client is an HTTP client that trusts the issuer.
func (i *Issuer) Client() *http.Client { return i.srv.Client() }

// Close stops the issuer.
func (i *Issuer) Close() { i.srv.Close() }

func (i *Issuer) discovery(w http.ResponseWriter, _ *http.Request) {
	u := i.URL()
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                u,
		"authorization_endpoint":                u + "/authorize",
		"token_endpoint":                        u + "/token",
		"end_session_endpoint":                  u + "/logout",
		"jwks_uri":                              u + "/jwks",
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
		"scopes_supported":                      []string{"openid", "profile", "email"},
		"backchannel_logout_supported":          true,
		"backchannel_logout_session_supported":  true,
	})
}

func (i *Issuer) jwks(w http.ResponseWriter, _ *http.Request) {
	i.mu.Lock()
	key, kid, cert, certs := i.key, i.kid, i.cert, i.certs
	i.fetches++
	i.mu.Unlock()
	jwk := map[string]any{
		"kty": "RSA",
		"use": "sig",
		"alg": "RS256",
		"kid": kid,
		"n":   b64(key.N.Bytes()),
		"e":   b64(big.NewInt(int64(key.E)).Bytes()),
	}
	if certs {
		thumb := sha256.Sum256(cert)
		jwk["x5c"] = []string{base64.StdEncoding.EncodeToString(cert)}
		jwk["x5t#S256"] = b64(thumb[:])
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": []map[string]any{jwk}})
}

// authorize answers an authorization request for the next queued identity
// with a code, redirecting to the client's redirect_uri. A request whose
// client or redirect_uri it cannot trust answers 400 itself; any other
// error goes back to the redirect_uri.
//
// ref: RFC 6749, section 4.1.2.1
func (i *Issuer) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := AuthorizeRequest{
		ClientID: q.Get("client_id"), RedirectURI: q.Get("redirect_uri"), Scope: q.Get("scope"),
		State: q.Get("state"), Nonce: q.Get("nonce"), CodeChallenge: q.Get("code_challenge"),
		CodeChallengeMethod: q.Get("code_challenge_method"), Prompt: q.Get("prompt"), MaxAge: -1,
	}
	if n, err := strconv.Atoi(q.Get("max_age")); err == nil {
		req.MaxAge = n
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.requests = append(i.requests, req)
	redirect, err := url.Parse(req.RedirectURI)
	if _, ok := i.clients[req.ClientID]; !ok || err != nil || !redirect.IsAbs() {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid_request", "error_description": "unknown client or redirect_uri",
		})
		return
	}
	back := func(params ...string) {
		v := redirect.Query()
		for k := 0; k+1 < len(params); k += 2 {
			v.Set(params[k], params[k+1])
		}
		if req.State != "" {
			v.Set("state", req.State)
		}
		redirect.RawQuery = v.Encode()
		http.Redirect(w, r, redirect.String(), http.StatusFound)
	}
	switch {
	case q.Get("response_type") != "code":
		back("error", "unsupported_response_type")
		return
	case req.CodeChallengeMethod != "S256" || req.CodeChallenge == "":
		back("error", "invalid_request", "error_description", "PKCE with S256 is required")
		return
	case len(i.queue) == 0:
		back("error", "login_required")
		return
	}
	id := i.queue[0]
	i.queue = i.queue[1:]
	code := randomToken()
	i.codes[code] = &grant{req: req, identity: id, at: time.Now()}
	back("code", code)
}

// token exchanges an authorization code once, for the client it was issued
// to, with the redirect_uri and PKCE verifier of its request.
func (i *Issuer) token(w http.ResponseWriter, r *http.Request) {
	refuse := func(status int, code, why string) {
		writeJSON(w, status, map[string]string{"error": code, "error_description": why})
	}
	if err := r.ParseForm(); err != nil {
		refuse(http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	id, secret, basic := r.BasicAuth()
	if basic {
		// ref: RFC 6749, section 2.3.1
		id, _ = url.QueryUnescape(id)
		secret, _ = url.QueryUnescape(secret)
	} else {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	code := r.PostForm.Get("code")
	i.mu.Lock()
	want, known := i.clients[id]
	g := i.codes[code]
	delete(i.codes, code)
	i.mu.Unlock()
	switch {
	case !known || subtle.ConstantTimeCompare([]byte(secret), []byte(want)) != 1:
		refuse(http.StatusUnauthorized, "invalid_client", "unknown client or wrong secret")
		return
	case r.PostForm.Get("grant_type") != "authorization_code":
		refuse(http.StatusBadRequest, "unsupported_grant_type", r.PostForm.Get("grant_type"))
		return
	case g == nil || g.req.ClientID != id:
		refuse(http.StatusBadRequest, "invalid_grant", "unknown or already used code")
		return
	case r.PostForm.Get("redirect_uri") != g.req.RedirectURI:
		refuse(http.StatusBadRequest, "invalid_grant", "redirect_uri is not the request's")
		return
	case !verifierMatches(r.PostForm.Get("code_verifier"), g.req.CodeChallenge):
		refuse(http.StatusBadRequest, "invalid_grant", "code_verifier does not match the code_challenge")
		return
	case slices.Contains(g.identity.Faults, FaultTokenUnavailable):
		refuse(http.StatusServiceUnavailable, "temporarily_unavailable", "the token endpoint is down")
		return
	}
	idToken, err := i.idToken(g)
	if err != nil {
		refuse(http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	body, err := json.Marshal(map[string]any{
		"access_token": randomToken(),
		"token_type":   "Bearer",
		"expires_in":   3600,
		"id_token":     idToken,
	})
	if err != nil {
		refuse(http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if slices.Contains(g.identity.Faults, FaultSlowToken) {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(SlowToken):
		}
	}
	_, _ = w.Write(body)
}

// idToken is g's ID token: the standard claims, then the identity's, then
// its faults.
func (i *Issuer) idToken(g *grant) (string, error) {
	id := g.identity
	now := time.Now()
	authTime := id.AuthTime
	if authTime.IsZero() {
		authTime = g.at
	}
	sid := id.SID
	if sid == "" {
		sid = randomToken()
	}
	claims := map[string]any{
		"iss":       i.URL(),
		"sub":       id.Subject,
		"aud":       g.req.ClientID,
		"iat":       now.Unix(),
		"exp":       now.Add(time.Hour).Unix(),
		"auth_time": authTime.Unix(),
		"sid":       sid,
	}
	if g.req.Nonce != "" {
		claims["nonce"] = g.req.Nonce
	}
	for k, v := range id.Claims {
		claims[k] = v
	}
	alg, kid, key := "RS256", "", (*rsa.PrivateKey)(nil)
	for _, f := range id.Faults {
		switch f {
		case FaultWrongNonce:
			claims["nonce"] = "not-" + g.req.Nonce
		case FaultWrongIssuer:
			claims["iss"] = i.URL() + "/another"
		case FaultWrongAudience:
			claims["aud"] = "another-client"
		case FaultEmptySubject:
			claims["sub"] = ""
		case FaultAudiencesWithoutAZP:
			claims["aud"] = []string{g.req.ClientID, "another-audience"}
			delete(claims, "azp")
		case FaultNoAuthTime:
			delete(claims, "auth_time")
		case FaultOldAuthTime:
			claims["auth_time"] = g.at.Add(-time.Hour).Unix()
		case FaultGroupsOverage:
			delete(claims, "groups")
			claims["_claim_names"] = map[string]string{"groups": "src1"}
			claims["_claim_sources"] = map[string]any{"src1": map[string]string{
				"endpoint": "https://graph.microsoft.com/v1.0/users/" + id.Subject + "/getMemberObjects",
			}}
		case FaultHS256:
			alg = "HS256"
		case FaultAlgNone:
			alg = "none"
		case FaultUnknownKey:
			kid, key = "unpublished", i.unpublished
		case FaultTokenUnavailable, FaultSlowToken:
		}
	}
	tok, err := i.sign(alg, kid, key, "JWT", claims)
	if err != nil {
		return "", err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.issued = append(i.issued, IssuedToken{
		ClientID: g.req.ClientID, Subject: id.Subject, SID: sid, Nonce: g.req.Nonce, Token: tok,
	})
	return tok, nil
}

// logout answers RP-initiated logout: it records the request and redirects
// to post_logout_redirect_uri with the state, or answers 200 without one.
//
// ref: OpenID Connect RP-Initiated Logout 1.0, section 2
func (i *Issuer) logout(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := LogoutRequest{
		IDTokenHint: q.Get("id_token_hint"), ClientID: q.Get("client_id"),
		PostLogoutRedirectURI: q.Get("post_logout_redirect_uri"), State: q.Get("state"),
	}
	i.mu.Lock()
	i.logouts = append(i.logouts, req)
	i.mu.Unlock()
	back, err := url.Parse(req.PostLogoutRedirectURI)
	if req.PostLogoutRedirectURI == "" || err != nil || !back.IsAbs() {
		w.WriteHeader(http.StatusOK)
		return
	}
	if req.State != "" {
		v := back.Query()
		v.Set("state", req.State)
		back.RawQuery = v.Encode()
	}
	http.Redirect(w, r, back.String(), http.StatusFound)
}

// LogoutToken is a signed back-channel logout token for audience naming
// sub, sid or both, with a fresh jti.
//
// ref: OpenID Connect Back-Channel Logout 1.0, section 2.4
func (i *Issuer) LogoutToken(audience, sub, sid string) (string, error) {
	now := time.Now()
	claims := map[string]any{
		"iss":    i.URL(),
		"aud":    audience,
		"iat":    now.Unix(),
		"exp":    now.Add(2 * time.Minute).Unix(),
		"jti":    randomToken(),
		"events": map[string]any{BackchannelLogoutEvent: map[string]any{}},
	}
	if sub != "" {
		claims["sub"] = sub
	}
	if sid != "" {
		claims["sid"] = sid
	}
	return i.sign("RS256", "", nil, "logout+jwt", claims)
}

// SendLogout posts token to a relying party's back-channel logout URI with
// client, as an issuer does when the user's session at it ends, and
// answers the relying party's status.
func SendLogout(ctx context.Context, client *http.Client, uri, token string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uri,
		strings.NewReader(url.Values{"logout_token": {token}}.Encode()))
	if err != nil {
		return 0, fmt.Errorf("%w: back-channel logout to %s: %w", ErrIssuer, uri, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%w: back-channel logout to %s: %w", ErrIssuer, uri, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// Mint signs a token with the standard claims (iss, iat, exp an hour on)
// and claims, which override them.
func (i *Issuer) Mint(claims map[string]any) (string, error) {
	now := time.Now()
	all := map[string]any{"iss": i.URL(), "iat": now.Unix(), "exp": now.Add(time.Hour).Unix()}
	for k, v := range claims {
		all[k] = v
	}
	return i.sign("RS256", "", nil, "JWT", all)
}

// sign serializes claims as a JWS with alg: RS256 with key under kid, or
// with the issuer's current key when key is nil; HS256 keyed with the
// current key's public key in PEM; or none, unsigned.
func (i *Issuer) sign(alg, kid string, key *rsa.PrivateKey, typ string, claims map[string]any) (string, error) {
	i.mu.Lock()
	current := i.key
	if key == nil {
		key, kid = i.key, i.kid
	}
	i.mu.Unlock()
	header, err := json.Marshal(map[string]string{"alg": alg, "typ": typ, "kid": kid})
	if err != nil {
		return "", fmt.Errorf("oidc header: %w", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("oidc claims: %w", err)
	}
	signing := b64(header) + "." + b64(payload)
	switch alg {
	case "none":
		return signing + ".", nil
	case "HS256":
		pub, err := x509.MarshalPKIXPublicKey(&current.PublicKey)
		if err != nil {
			return "", fmt.Errorf("oidc public key: %w", err)
		}
		mac := hmac.New(sha256.New, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub}))
		mac.Write([]byte(signing))
		return signing + "." + b64(mac.Sum(nil)), nil
	}
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("oidc sign: %w", err)
	}
	return signing + "." + b64(sig), nil
}

// verifierMatches reports whether verifier is the S256 PKCE verifier of
// challenge.
//
// ref: RFC 7636, section 4.6
func verifierMatches(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	return verifier != "" && subtle.ConstantTimeCompare([]byte(b64(sum[:])), []byte(challenge)) == 1
}

// randomToken is 32 random bytes in base64url, for codes, session IDs and
// token IDs.
func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return b64(b)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
