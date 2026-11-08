package acceptance

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/cucumber/godog"
)

// oauthSteps are an MCP client of infraweave's OAuth authorization server:
// it registers, authorizes, redeems and refreshes over plain HTTP, and the
// scenario's browser consents through the API.
//
// ref: decision 0006
type oauthSteps struct {
	o *orgSteps
	// client is the registered client's ID, and verifier the PKCE
	// verifier of its latest authorization request.
	client   string
	verifier string
	state    string
	// request is the authorization request the consent page names, and
	// location where the last authorization or decision sent the
	// browser.
	request  string
	location *url.URL
	// code is the authorization code the last approval sent.
	code string
	// access and refresh are the client's current tokens; spent is the
	// refresh token the last refresh replaced.
	access  string
	refresh string
	spent   string
	scope   string
	expires float64
	// answer is the last protocol answer's status and JSON body.
	status int
	body   map[string]any
}

// noRedirects sends one request and stops, so a step sees each 302.
var noRedirects = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}}

func (s *oauthSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the client reads the authorization server metadata through ([AB])$`, s.metadata)
	sc.Step(
		`^the metadata names the endpoints, the code flow with S256, public clients and the scopes "([^"]+)"$`,
		s.metadataIs,
	)
	sc.Step(`^the client registers the redirect URIs? "([^"]+)" through ([AB])$`, s.registerClient)
	sc.Step(
		`^the registration answers invalid_redirect_uri naming "([^"]+)"$`,
		s.registrationRefused,
	)
	sc.Step(
		`^the client asks through ([AB]) to authorize "([^"]*)" for "([^"]+)" with (an S256|a plain) challenge, redirecting to "([^"]+)"$`,
		s.authorize,
	)
	sc.Step(`^it is sent to the console's consent page for the request$`, s.toConsent)
	sc.Step(`^it is sent to "([^"]+)" with the error "([a-z_]+)"$`, s.toError)
	sc.Step(`^the browser reads the request through ([AB])$`, s.readRequest)
	sc.Step(`^the request names the redirect host "([^"]+)" and the scopes "([^"]+)"$`, s.requestIs)
	sc.Step(
		`^the browser (approves|denies) the request(?: for the (home|sibling) account)? through ([AB])$`,
		s.decide,
	)
	sc.Step(`^the browser is sent to "([^"]+)" with a code and the client's state$`, s.withCode)
	sc.Step(`^the client redeems the code through ([AB])$`, s.redeem)
	sc.Step(`^the client redeems the code again through ([AB])$`, s.redeem)
	sc.Step(
		`^it holds an access token for "([^"]+)" with the scope "([^"]+)" for an hour, and a refresh token$`,
		s.holds,
	)
	sc.Step(`^the client refreshes through ([AB])$`, s.refreshWith(func(s *oauthSteps) string {
		return s.refresh
	}))
	sc.Step(`^the client refreshes with the spent refresh token through ([AB])$`,
		s.refreshWith(func(s *oauthSteps) string { return s.spent }))
	sc.Step(`^it holds a new refresh token and a new access token$`, s.rotated)
	sc.Step(`^the token endpoint answers "([a-z_]+)"$`, s.tokenRefused)
	sc.Step(
		`^the home account's sessions through ([AB]) with token "(\w+)" (list|leave out) the grant$`,
		s.sessionsList,
	)
}

// base is the named replica's URL.
func (s *oauthSteps) base(replica string) string { return s.o.b.base(replica) }

// send sends a protocol request and records its answer.
func (s *oauthSteps) send(ctx context.Context, req *http.Request) error {
	resp, err := noRedirects.Do(req.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("%s %s: %w", req.Method, req.URL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read the answer: %w", err)
	}
	s.status, s.body, s.location = resp.StatusCode, nil, nil
	if loc := resp.Header.Get("Location"); loc != "" {
		if s.location, err = url.Parse(loc); err != nil {
			return fmt.Errorf("%w: Location %q: %w", errStep, loc, err)
		}
	}
	if len(raw) > 0 && strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(raw, &s.body); err != nil {
			return fmt.Errorf("%w: %d %s: %w", errStep, resp.StatusCode, raw, err)
		}
	}
	return nil
}

func (s *oauthSteps) metadata(ctx context.Context, replica string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		s.base(replica)+"/.well-known/oauth-authorization-server", nil)
	if err != nil {
		return err //nolint:wrapcheck // a constant request
	}
	return s.send(ctx, req)
}

func (s *oauthSteps) metadataIs(scopes string) error {
	want := map[string]any{
		"issuer":                                publicURL,
		"authorization_endpoint":                publicURL + "/oauth/authorize",
		"token_endpoint":                        publicURL + "/oauth/token",
		"registration_endpoint":                 publicURL + "/oauth/register",
		"response_types_supported":              []any{"code"},
		"grant_types_supported":                 []any{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []any{"S256"},
		"token_endpoint_auth_methods_supported": []any{"none"},
		"scopes_supported":                      words(scopes),
	}
	got, _ := json.Marshal(s.body)
	exp, _ := json.Marshal(want)
	if s.status != http.StatusOK || string(got) != string(exp) {
		return fmt.Errorf("%w: metadata %d %s, want %s", errStep, s.status, got, exp)
	}
	return nil
}

// words are s's space-separated words as JSON values.
func words(s string) []any {
	var out []any
	for w := range strings.FieldsSeq(s) {
		out = append(out, w)
	}
	return out
}

func (s *oauthSteps) registerClient(ctx context.Context, uris, replica string) error {
	var list []string
	for u := range strings.SplitSeq(uris, ",") {
		list = append(list, strings.TrimSpace(u))
	}
	body, _ := json.Marshal(map[string]any{"redirect_uris": list, "client_name": "Editor"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base(replica)+"/oauth/register",
		strings.NewReader(string(body)))
	if err != nil {
		return err //nolint:wrapcheck // a constant request
	}
	req.Header.Set("Content-Type", "application/json")
	if err := s.send(ctx, req); err != nil {
		return err
	}
	if s.status == http.StatusCreated {
		s.client, _ = s.body["client_id"].(string)
	}
	return nil
}

func (s *oauthSteps) registrationRefused(uri string) error {
	desc, _ := s.body["error_description"].(string)
	if s.status != http.StatusBadRequest || s.body["error"] != "invalid_redirect_uri" ||
		!strings.Contains(desc, uri) {
		return fmt.Errorf("%w: registration answered %d %v", errStep, s.status, s.body)
	}
	return nil
}

// secret is 32 random bytes in unpadded base64url.
func secret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand never fails
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *oauthSteps) authorize(
	ctx context.Context,
	replica, scope, resource, method, redirect string,
) error {
	if s.client == "" {
		return fmt.Errorf("%w: no client registered", errStep)
	}
	s.verifier, s.state = secret(), secret()
	challenge, m := s.verifier, "plain"
	if method == "an S256" {
		sum := sha256.Sum256([]byte(s.verifier))
		challenge, m = base64.RawURLEncoding.EncodeToString(sum[:]), "S256"
	}
	q := url.Values{
		"response_type": {"code"}, "client_id": {s.client}, "redirect_uri": {redirect},
		"scope": {scope}, "state": {s.state}, "code_challenge": {challenge},
		"code_challenge_method": {m}, "resource": {publicURL + resource},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		s.base(replica)+"/oauth/authorize?"+q.Encode(), nil)
	if err != nil {
		return err //nolint:wrapcheck // a constant request
	}
	s.request, s.code = "", ""
	if err := s.send(ctx, req); err != nil {
		return err
	}
	if s.location == nil {
		return nil
	}
	if id, ok := strings.CutPrefix(s.location.String(), publicURL+"/consent/"); ok {
		s.request = id
	}
	return nil
}

func (s *oauthSteps) toConsent() error {
	if s.status != http.StatusFound || s.location == nil ||
		s.location.String() != publicURL+"/consent/"+s.request {
		return fmt.Errorf("%w: authorize answered %d %v, sending to %v", errStep, s.status,
			s.body, s.location)
	}
	return nil
}

func (s *oauthSteps) toError(uri, code string) error {
	if s.status != http.StatusFound || s.location == nil {
		return fmt.Errorf("%w: answered %d %v, last %d %s", errStep, s.status, s.body,
			s.o.a.s.last().status, s.o.a.s.last().body)
	}
	q := s.location.Query()
	at := *s.location
	at.RawQuery = ""
	if at.String() != uri || q.Get("error") != code || q.Get("state") != s.state {
		return fmt.Errorf("%w: sent to %s, want %s with error %s and the state",
			errStep, s.location, uri, code)
	}
	return nil
}

func (s *oauthSteps) readRequest(ctx context.Context, replica string) error {
	_, err := s.o.b.do(ctx, replica, http.MethodGet, "/api/v1/auth/oauth/requests/"+s.request,
		"", nil)
	return err
}

func (s *oauthSteps) requestIs(host, scopes string) error {
	var r struct {
		ID           string   `json:"id"`
		ClientID     string   `json:"clientId"`
		ClientName   string   `json:"clientName"`
		RedirectHost string   `json:"redirectHost"`
		Scopes       []string `json:"scopes"`
	}
	last := s.o.a.s.last()
	if last.status != http.StatusOK || json.Unmarshal(last.body, &r) != nil ||
		r.ID != s.request || r.ClientID != s.client || r.ClientName != "Editor" ||
		r.RedirectHost != host || !slices.Equal(r.Scopes, strings.Split(scopes, ", ")) {
		return fmt.Errorf("%w: the request is %d %s", errStep, last.status, last.body)
	}
	return nil
}

// decide approves or denies the request through the browser, as the
// consent page does, and follows its answer's redirectTo.
func (s *oauthSteps) decide(ctx context.Context, verb, acct, replica string) error {
	path := "/api/v1/auth/oauth/requests/" + s.request + "/deny"
	var body any
	if verb == "approves" {
		id := home.account
		if acct == "sibling" {
			id = sibling.account
		}
		path = strings.TrimSuffix(path, "/deny") + "/approve"
		body = map[string]string{"account": id}
	}
	r, err := s.o.b.do(ctx, replica, http.MethodPost, path, publicURL, body)
	if err != nil && r.status == 0 {
		return err
	}
	s.location = nil
	var d struct {
		RedirectTo string `json:"redirectTo"`
	}
	if r.status == http.StatusOK && json.Unmarshal(r.body, &d) == nil {
		s.status = http.StatusFound
		if s.location, err = url.Parse(d.RedirectTo); err != nil {
			return fmt.Errorf("%w: redirectTo %q: %w", errStep, d.RedirectTo, err)
		}
		if c := s.location.Query().Get("code"); c != "" {
			s.code = c
		}
	}
	return nil
}

func (s *oauthSteps) withCode(uri string) error {
	if s.location == nil {
		return fmt.Errorf("%w: the decision sent the browser nowhere: %s", errStep,
			s.o.a.s.last().body)
	}
	q := s.location.Query()
	at := *s.location
	at.RawQuery = ""
	if at.String() != uri || q.Get("code") == "" || q.Get("state") != s.state {
		return fmt.Errorf("%w: sent to %s, want %s with a code and the state", errStep,
			s.location, uri)
	}
	return nil
}

// token posts form to the token endpoint and keeps the tokens it answers.
func (s *oauthSteps) token(ctx context.Context, replica string, form url.Values) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base(replica)+"/oauth/token",
		strings.NewReader(form.Encode()))
	if err != nil {
		return err //nolint:wrapcheck // a constant request
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := s.send(ctx, req); err != nil {
		return err
	}
	if s.status == http.StatusOK {
		s.spent = s.refresh
		s.access, _ = s.body["access_token"].(string)
		s.refresh, _ = s.body["refresh_token"].(string)
		s.scope, _ = s.body["scope"].(string)
		s.expires, _ = s.body["expires_in"].(float64)
	}
	return nil
}

func (s *oauthSteps) redeem(ctx context.Context, replica string) error {
	if s.code == "" {
		return fmt.Errorf("%w: no approval sent a code: %d %s", errStep, s.o.a.s.last().status,
			s.o.a.s.last().body)
	}
	return s.token(ctx, replica, url.Values{
		"grant_type": {"authorization_code"}, "client_id": {s.client},
		"code": {s.code}, "code_verifier": {s.verifier},
	})
}

func (s *oauthSteps) refreshWith(
	token func(*oauthSteps) string,
) func(context.Context, string) error {
	return func(ctx context.Context, replica string) error {
		return s.token(ctx, replica, url.Values{
			"grant_type": {"refresh_token"}, "client_id": {s.client},
			"refresh_token": {token(s)},
		})
	}
}

// claims are the payload of the access token, a JWT.
func (s *oauthSteps) claims() (map[string]any, error) {
	parts := strings.Split(s.access, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: the access token %q is not a JWT", errStep, s.access)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	var c map[string]any
	if err != nil || json.Unmarshal(raw, &c) != nil {
		return nil, fmt.Errorf("%w: the access token's payload: %w", errStep, err)
	}
	return c, nil
}

func (s *oauthSteps) holds(resource, scope string) error {
	if s.status != http.StatusOK || s.refresh == "" || s.body["token_type"] != "Bearer" ||
		s.expires != 3600 || s.scope != scope {
		return fmt.Errorf("%w: the token endpoint answered %d %v", errStep, s.status, s.body)
	}
	c, err := s.claims()
	if err != nil {
		return err
	}
	if c["aud"] != publicURL+resource || c["iw_kind"] != "oauth" || c["scope"] != scope ||
		c["sub"] != "alice" || c["exp"].(float64)-c["iat"].(float64) != 3600 {
		return fmt.Errorf("%w: the access token's claims are %v", errStep, c)
	}
	return nil
}

func (s *oauthSteps) rotated() error {
	if s.status != http.StatusOK || s.refresh == "" || s.refresh == s.spent || s.access == "" {
		return fmt.Errorf("%w: the refresh answered %d %v", errStep, s.status, s.body)
	}
	return nil
}

func (s *oauthSteps) tokenRefused(code string) error {
	if s.status != http.StatusBadRequest || s.body["error"] != code {
		return fmt.Errorf("%w: the token endpoint answered %d %v, want %s", errStep, s.status,
			s.body, code)
	}
	return nil
}

// sessionsList checks whether the home account's sessions name the grant,
// the access token's jti.
func (s *oauthSteps) sessionsList(ctx context.Context, replica, name, verb string) error {
	c, err := s.claims()
	if err != nil {
		return err
	}
	listed, err := s.o.a.list(ctx, replica, name)
	if err != nil {
		return err
	}
	found := slices.ContainsFunc(listed, func(l listedSession) bool { return l.ID == c["jti"] })
	if found != (verb == "list") {
		return fmt.Errorf("%w: the sessions %s the grant %v: %v", errStep,
			map[bool]string{true: "list", false: "leave out"}[found], c["jti"], listed)
	}
	return nil
}
