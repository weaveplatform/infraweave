package acceptance

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/pkg/sdk"
)

// revocationWithin is how soon a revocation through one replica reaches
// another, by its infraweave_invalidate message (decision 0006).
const revocationWithin = time.Second

// authSteps are the steps of decision 0006's exchange and sessions.
type authSteps struct {
	s *servedSteps
	// tokens are the scenario's named API tokens.
	tokens map[string]string
	// refusals are the answers of the refused exchanges.
	refusals []response
}

func (a *authSteps) register(sc *godog.ScenarioContext) {
	sc.Step(
		`^the (home|sibling|stranger) key is exchanged through ([AB]) for token "(\w+)"$`,
		a.exchange,
	)
	sc.Step(
		`^token "(\w+)" carries the claims of an API token of the (home|sibling|stranger) key$`,
		a.claims,
	)
	sc.Step(
		`^the viewer through ([AB]) with token "(\w+)" is the (home|sibling|stranger) key$`,
		a.viewer,
	)
	sc.Step(
		`^exchanging (the home key with a wrong secret|an unknown key|the home key once it is disabled|the home key once it has expired|the home key once it is deleted) answers a 401 problem "unauthenticated"$`,
		a.refused,
	)
	sc.Step(`^every refused exchange answered the same problem$`, a.sameProblem)
	sc.Step(`^the home account has a member's browser session$`, a.memberSession)
	sc.Step(
		`^the sessions listed through ([AB]) with token "(\w+)" are (\d+), its own current, the member's browser session among them$`,
		a.listedWithMember,
	)
	sc.Step(
		`^the sessions listed through ([AB]) with token "(\w+)" are its own alone$`,
		a.listedAlone,
	)
	sc.Step(
		`^token "(\w+)"'s session is deleted through ([AB]) with token "(\w+)"$`,
		a.deleteSession,
	)
	sc.Step(
		`^deleting token "(\w+)"'s session through ([AB]) with token "(\w+)" answers a (\d+) problem "([a-z-]+)"$`,
		a.deleteRefused,
	)
	sc.Step(`^every session but token "(\w+)"'s is deleted through ([AB])$`, a.deleteOthers)
	sc.Step(`^token "(\w+)" logs out through ([AB])$`, a.logout)
	sc.Step(`^the viewer through ([AB]) with token "(\w+)" answers (\d+)$`, a.viewerAnswers)
	sc.Step(
		`^"([A-Z]+) ([^"]+)" through ([AB]) with token "(\w+)" answers a (\d+) problem "([a-z-]+)"$`,
		a.answersProblem,
	)
	sc.Step(
		`^exchanging the home key through ([AB]) answers a (\d+) problem "([a-z-]+)"$`,
		a.exchangeProblem,
	)
	sc.Step(
		`^the viewer through ([AB]) with token "(\w+)" answers 401 within a second$`,
		a.viewerRevoked,
	)
}

// client is a client of the replica's API listener holding token, or no
// token when token is empty.
func (a *authSteps) client(replica, token string) (*sdk.Client, error) {
	i := 0
	if replica == "B" {
		i = 1
	}
	opts := []sdk.Option{sdk.WithRetry(sdk.NoRetry)}
	if token != "" {
		opts = append(opts, sdk.WithToken(token))
	}
	c, err := sdk.New("http://"+a.s.w.servers[i].API, opts...)
	if err != nil {
		return nil, fmt.Errorf("sdk: %w", err)
	}
	return c, nil
}

// do sends a request through replica with the named token.
func (a *authSteps) do(
	ctx context.Context,
	replica, name, method, path string,
	body any,
) (response, error) {
	tok, ok := a.tokens[name]
	if !ok && name != "" {
		return response{}, fmt.Errorf("%w: no token %q", errStep, name)
	}
	c, err := a.client(replica, tok)
	if err != nil {
		return response{}, err
	}
	resp, err := c.Do(ctx, method, path, body)
	if resp == nil {
		return response{}, err
	}
	r := response{status: resp.Status, header: resp.Header, body: resp.Body, err: err}
	a.s.sent = append(a.s.sent, r)
	return r, nil
}

func (a *authSteps) exchange(ctx context.Context, who, replica, name string) error {
	t := tenants[who]
	db, err := a.s.w.querier(ctx)
	if err != nil {
		return err
	}
	if err := t.seed(ctx, db); err != nil {
		return err
	}
	c, err := a.client(replica, "")
	if err != nil {
		return err
	}
	tok, err := t.exchange(ctx, c)
	if err != nil {
		return err
	}
	a.tokens[name] = tok
	return nil
}

// jwtPart decodes part i of a compact JWT: 0 the header, 1 the claims.
func jwtPart(token string, i int, v any) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return fmt.Errorf("%w: %d parts in the token", errStep, len(parts))
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[i])
	if err != nil {
		return fmt.Errorf("%w: token part %d: %w", errStep, i, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%w: token part %d: %w", errStep, i, err)
	}
	return nil
}

// audience is aud, which RFC 7519 lets a token carry as one string or an
// array.
type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*a = audience{one}
		return nil
	}
	return json.Unmarshal(b, (*[]string)(a)) //nolint:wrapcheck // the claims' decode names it
}

type apiClaims struct {
	Issuer   string   `json:"iss"`
	Subject  string   `json:"sub"`
	Audience audience `json:"aud"`
	ID       string   `json:"jti"`
	Kind     string   `json:"iw_kind"` //nolint:tagliatelle // decision 0006's claim name
	IssuedAt int64    `json:"iat"`
	Expiry   int64    `json:"exp"`
}

// session is the session a named token stands for, its jti.
func (a *authSteps) session(name string) (string, error) {
	var c apiClaims
	if err := jwtPart(a.tokens[name], 1, &c); err != nil {
		return "", err
	}
	return c.ID, nil
}

// claims checks #167's table for an API token: ES256 under a key ID,
// iss the public URL, aud the API's base URL, sub api::<key>, iw_kind
// api_key, a ULID jti, and a life of auth.tokens.api.ttl.
func (a *authSteps) claims(name, who string) error {
	var h struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := jwtPart(a.tokens[name], 0, &h); err != nil {
		return err
	}
	var c apiClaims
	if err := jwtPart(a.tokens[name], 1, &c); err != nil {
		return err
	}
	_, jtiErr := kernel.ParseULID(c.ID)
	want := apiClaims{
		Issuer:   publicURL,
		Subject:  "api::" + tenants[who].key,
		Audience: audience{publicURL + "/api/v1"},
		ID:       c.ID,
		Kind:     "api_key",
		IssuedAt: c.IssuedAt,
		Expiry:   c.IssuedAt + int64((10*time.Hour)/time.Second),
	}
	if h.Alg != "ES256" || h.Kid == "" || jtiErr != nil || !equalClaims(c, want) {
		return fmt.Errorf("%w: header %+v and claims %+v, want ES256 and %+v", errStep, h, c, want)
	}
	return nil
}

func equalClaims(a, b apiClaims) bool {
	return a.Issuer == b.Issuer && a.Subject == b.Subject && slices.Equal(a.Audience, b.Audience) &&
		a.ID == b.ID && a.Kind == b.Kind && a.IssuedAt == b.IssuedAt && a.Expiry == b.Expiry
}

func (a *authSteps) viewer(ctx context.Context, replica, name, who string) error {
	r, err := a.do(ctx, replica, name, http.MethodGet, "/api/v1/viewer", nil)
	if err != nil {
		return err
	}
	var u struct {
		ID         string   `json:"id"`
		JWT        string   `json:"jwt"`
		Name       string   `json:"name"`
		Teams      []string `json:"teams"`
		ValidUntil int64    `json:"validUntil"`
	}
	if err := json.Unmarshal(r.body, &u); err != nil || r.status != http.StatusOK {
		return fmt.Errorf("%w: viewer answered %d %s", errStep, r.status, r.body)
	}
	var c apiClaims
	if err := jwtPart(a.tokens[name], 1, &c); err != nil {
		return err
	}
	if u.ID != "api::"+tenants[who].key || u.JWT != a.tokens[name] || u.Name != "acceptance" ||
		!slices.Equal(u.Teams, []string{"acceptance"}) || u.ValidUntil != c.Expiry {
		return fmt.Errorf("%w: viewer %s", errStep, r.body)
	}
	return nil
}

func (a *authSteps) refused(ctx context.Context, how string) error {
	db, err := a.s.w.querier(ctx)
	if err != nil {
		return err
	}
	if err := home.seed(ctx, db); err != nil {
		return err
	}
	body := map[string]string{"id": home.key, "secret": home.secret()}
	change := map[string]string{
		"the home key once it is disabled": "disabled_at = now(), disabled_reason = 'ADMIN'",
		"the home key once it has expired": "expires_at = now()",
		"the home key once it is deleted":  "deleted_at = now()",
	}[how]
	switch how {
	case "the home key with a wrong secret":
		body["secret"] = sibling.secret()
	case "an unknown key":
		body["id"] = kernel.NewULID().String()
	default:
		if _, err := db.Query(ctx, fmt.Sprintf(`UPDATE api_keys SET %s WHERE id = '%s'`,
			change, uuidOf(home.key))); err != nil {
			return err
		}
		defer func() {
			_, _ = db.Query(context.WithoutCancel(ctx), fmt.Sprintf(`UPDATE api_keys
				SET disabled_at = NULL, disabled_reason = NULL, expires_at = NULL, deleted_at = NULL
				WHERE id = '%s'`, uuidOf(home.key)))
		}()
	}
	c, err := a.client("A", "")
	if err != nil {
		return err
	}
	resp, err := c.Do(ctx, http.MethodPost, "/api/v1/auth/token", body)
	if resp == nil {
		return err
	}
	r := response{status: resp.Status, header: resp.Header, body: resp.Body, err: err}
	a.s.sent = append(a.s.sent, r)
	a.refusals = append(a.refusals, r)
	return isProblem(r, "exchanging "+how, "401", "unauthenticated")
}

// sameProblem checks that the refusals cannot be told apart: one status,
// type, title and detail.
func (a *authSteps) sameProblem() error {
	var first sdk.Problem
	for i, r := range a.refusals {
		p, _ := sdk.AsProblem(r.err)
		p.Instance = ""
		if i == 0 {
			first = p
			continue
		}
		if p.Type != first.Type || p.Title != first.Title || p.Detail != first.Detail ||
			p.Status != first.Status {
			return fmt.Errorf("%w: refusal %d is %s %q, the first %s %q",
				errStep, i+1, p.Type, p.Detail, first.Type, first.Detail)
		}
	}
	return nil
}

// memberID and memberSession are the home account's member and their
// browser session, seeded as #171's sign-in will create them.
const (
	memberID      = "01KACME0000000000000000006"
	memberSession = "01KACME0000000000000000007"
)

func (a *authSteps) memberSession(ctx context.Context) error {
	db, err := a.s.w.querier(ctx)
	if err != nil {
		return err
	}
	if err := home.seed(ctx, db); err != nil {
		return err
	}
	_, err = db.Query(ctx, fmt.Sprintf(`
		INSERT INTO members (organization_id, id, login, name, email, status)
			VALUES ('%[1]s', '%[2]s', 'alice', 'Alice', 'alice@example.com', 'active');
		INSERT INTO account_users (account_id, organization_id, member_id)
			VALUES ('%[3]s', '%[1]s', '%[2]s');
		INSERT INTO sessions (organization_id, account_id, id, channel, actor_kind, actor_id,
				member_id, login, name, email, ip_address, expires_at, idle_expires_at)
			VALUES ('%[1]s', '%[3]s', '%[4]s', 'WEB', 'user', '%[2]s', '%[2]s', 'alice', 'Alice',
				'alice@example.com', '192.0.2.44', now() + interval '1 day', now() + interval '1 hour');`,
		uuidOf(home.org), uuidOf(memberID), uuidOf(home.account), uuidOf(memberSession)))
	return err
}

type listedSession struct {
	ID            string `json:"id"`
	Channel       string `json:"channel"`
	Current       bool   `json:"current"`
	Login         string `json:"login"`
	Type          string `json:"type"`
	IsMachineUser bool   `json:"isMachineUser"`
	IPAddress     string `json:"ipAddress"`
}

// list is every session the named token lists, following the cursor.
func (a *authSteps) list(ctx context.Context, replica, name string) ([]listedSession, error) {
	var out []listedSession
	path := "/api/v1/sessions?limit=2"
	for {
		r, err := a.do(ctx, replica, name, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		var page struct {
			Items      []listedSession `json:"items"`
			NextCursor *string         `json:"nextCursor"`
		}
		if err := json.Unmarshal(r.body, &page); err != nil || r.status != http.StatusOK {
			return nil, fmt.Errorf("%w: sessions answered %d %s", errStep, r.status, r.body)
		}
		out = append(out, page.Items...)
		if page.NextCursor == nil {
			return out, nil
		}
		path = "/api/v1/sessions?limit=2&cursor=" + *page.NextCursor
	}
}

func (a *authSteps) listedWithMember(ctx context.Context, replica, name string, n int) error {
	got, err := a.list(ctx, replica, name)
	if err != nil {
		return err
	}
	own, err := a.session(name)
	if err != nil {
		return err
	}
	var current, member bool
	for _, s := range got {
		switch s.ID {
		case own:
			current = s.Current && s.Type == "API_KEY" && s.IsMachineUser && s.Channel == "UNKNOWN"
		case memberSession:
			member = !s.Current && s.Type == "USER" && s.Channel == "WEB" && s.Login == "alice" &&
				s.IPAddress == "192.0.2.44"
		}
	}
	if len(got) != n || !current || !member {
		return fmt.Errorf("%w: %d sessions %+v, want %d with %s current and %s the member's",
			errStep, len(got), got, n, own, memberSession)
	}
	return nil
}

func (a *authSteps) listedAlone(ctx context.Context, replica, name string) error {
	got, err := a.list(ctx, replica, name)
	if err != nil {
		return err
	}
	own, err := a.session(name)
	if err != nil {
		return err
	}
	if len(got) != 1 || got[0].ID != own || !got[0].Current {
		return fmt.Errorf("%w: sessions %+v, want %s alone", errStep, got, own)
	}
	return nil
}

func (a *authSteps) deleteSession(ctx context.Context, victim, replica, name string) error {
	id, err := a.session(victim)
	if err != nil {
		return err
	}
	r, err := a.do(ctx, replica, name, http.MethodDelete, "/api/v1/sessions/"+id, nil)
	if err != nil {
		return err
	}
	var s listedSession
	if err := json.Unmarshal(r.body, &s); err != nil || r.status != http.StatusOK || s.ID != id {
		return fmt.Errorf("%w: sessionDelete answered %d %s", errStep, r.status, r.body)
	}
	return nil
}

func (a *authSteps) deleteRefused(
	ctx context.Context,
	victim, replica, name, code, slug string,
) error {
	id, err := a.session(victim)
	if err != nil {
		return err
	}
	r, err := a.do(ctx, replica, name, http.MethodDelete, "/api/v1/sessions/"+id, nil)
	if err != nil {
		return err
	}
	return isProblem(r, "deleting token "+victim+"'s session", code, slug)
}

func (a *authSteps) deleteOthers(ctx context.Context, name, replica string) error {
	r, err := a.do(ctx, replica, name, http.MethodDelete, "/api/v1/sessions", nil)
	if err != nil {
		return err
	}
	var revoked []listedSession
	if err := json.Unmarshal(r.body, &revoked); err != nil || r.status != http.StatusOK ||
		len(revoked) == 0 {
		return fmt.Errorf("%w: sessionDeleteAll answered %d %s", errStep, r.status, r.body)
	}
	return nil
}

func (a *authSteps) logout(ctx context.Context, name, replica string) error {
	r, err := a.do(ctx, replica, name, http.MethodPost, "/api/v1/auth/logout", nil)
	if err != nil {
		return err
	}
	var s listedSession
	if err := json.Unmarshal(r.body, &s); err != nil || r.status != http.StatusOK || !s.Current {
		return fmt.Errorf("%w: logout answered %d %s", errStep, r.status, r.body)
	}
	return nil
}

func (a *authSteps) viewerAnswers(ctx context.Context, replica, name string, code int) error {
	r, err := a.do(ctx, replica, name, http.MethodGet, "/api/v1/viewer", nil)
	if err != nil {
		return err
	}
	if r.status != code {
		return fmt.Errorf("%w: viewer answered %d %s, want %d", errStep, r.status, r.body, code)
	}
	return nil
}

func (a *authSteps) answersProblem(
	ctx context.Context,
	method, path, replica, name, code, slug string,
) error {
	r, err := a.do(ctx, replica, name, method, path, nil)
	if err != nil {
		return err
	}
	return isProblem(r, method+" "+path, code, slug)
}

func (a *authSteps) exchangeProblem(ctx context.Context, replica, code, slug string) error {
	c, err := a.client(replica, "")
	if err != nil {
		return err
	}
	resp, err := c.Do(ctx, http.MethodPost, "/api/v1/auth/token",
		map[string]string{"id": home.key, "secret": home.secret()})
	if resp == nil {
		return err
	}
	r := response{status: resp.Status, header: resp.Header, body: resp.Body, err: err}
	a.s.sent = append(a.s.sent, r)
	return isProblem(r, "exchanging the home key", code, slug)
}

// viewerRevoked waits at most revocationWithin for the replica to refuse
// a token whose session another replica revoked.
func (a *authSteps) viewerRevoked(ctx context.Context, replica, name string) error {
	ctx, cancel := context.WithTimeout(ctx, revocationWithin)
	defer cancel()
	return Eventually(ctx, "the revocation to reach "+replica,
		func(ctx context.Context) (bool, error) {
			r, err := a.do(ctx, replica, name, http.MethodGet, "/api/v1/viewer", nil)
			if err != nil {
				return false, err
			}
			return isProblem(r, "viewer", "401", "unauthenticated") == nil, nil
		})
}
