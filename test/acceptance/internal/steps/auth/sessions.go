package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// revocationWithin is how soon a revocation through one replica reaches
// another, by its infraweave_invalidate message.
//
// ref: decision 0006
const revocationWithin = time.Second

// authSteps are the steps of the token exchange and sessions.
//
// ref: decision 0006
type authSteps struct {
	w *harness.World
	// refusals are the answers of the refused exchanges.
	refusals []harness.Response
}

func (a *authSteps) register(sc *godog.ScenarioContext) {
	sc.Step(
		`^the (home|member|sibling|stranger) key is exchanged through ([AB]) for token "(\w+)"$`,
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

func (a *authSteps) exchange(ctx context.Context, who, replica, name string) error {
	t := harness.Tenants[who]
	db, err := a.w.Querier(ctx)
	if err != nil {
		return err
	}
	if err := t.Seed(ctx, db); err != nil {
		return err
	}
	c, err := a.w.Client(replica, "")
	if err != nil {
		return err
	}
	tok, err := t.Exchange(ctx, c)
	if err != nil {
		return err
	}
	a.w.Tokens[name] = tok
	return nil
}

// session is the session a named token stands for, its jti.
func (a *authSteps) session(name string) (string, error) {
	var c harness.APIClaims
	if err := harness.JWTPart(a.w.Tokens[name], 1, &c); err != nil {
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
	if err := harness.JWTPart(a.w.Tokens[name], 0, &h); err != nil {
		return err
	}
	var c harness.APIClaims
	if err := harness.JWTPart(a.w.Tokens[name], 1, &c); err != nil {
		return err
	}
	_, jtiErr := kernel.ParseULID(c.ID)
	want := harness.APIClaims{
		Issuer:   harness.PublicURL,
		Subject:  "api::" + harness.Tenants[who].Key,
		Audience: harness.Audience{harness.PublicURL + "/api/v1"},
		ID:       c.ID,
		Kind:     "api_key",
		IssuedAt: c.IssuedAt,
		Expiry:   c.IssuedAt + int64((10*time.Hour)/time.Second),
	}
	if h.Alg != "ES256" || h.Kid == "" || jtiErr != nil || !harness.EqualClaims(c, want) {
		return fmt.Errorf(
			"%w: header %+v and claims %+v, want ES256 and %+v",
			harness.ErrStep,
			h,
			c,
			want,
		)
	}
	return nil
}

func (a *authSteps) viewer(ctx context.Context, replica, name, who string) error {
	r, err := a.w.Do(ctx, replica, name, http.MethodGet, "/api/v1/viewer", nil)
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
	if err := json.Unmarshal(r.Body, &u); err != nil || r.Status != http.StatusOK {
		return fmt.Errorf("%w: viewer answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	var c harness.APIClaims
	if err := harness.JWTPart(a.w.Tokens[name], 1, &c); err != nil {
		return err
	}
	if u.ID != "api::"+harness.Tenants[who].Key || u.JWT != a.w.Tokens[name] ||
		u.Name != "acceptance" ||
		!slices.Equal(u.Teams, []string{"acceptance"}) ||
		u.ValidUntil != c.Expiry {
		return fmt.Errorf("%w: viewer %s", harness.ErrStep, r.Body)
	}
	return nil
}

func (a *authSteps) refused(ctx context.Context, how string) error {
	db, err := a.w.Querier(ctx)
	if err != nil {
		return err
	}
	if err := harness.Home.Seed(ctx, db); err != nil {
		return err
	}
	body := map[string]string{"id": harness.Home.Key, "secret": harness.Home.Secret()}
	change := map[string]string{
		"the home key once it is disabled": "disabled_at = now(), disabled_reason = 'ADMIN'",
		"the home key once it has expired": "expires_at = now()",
		"the home key once it is deleted":  "deleted_at = now()",
	}[how]
	switch how {
	case "the home key with a wrong secret":
		body["secret"] = harness.Sibling.Secret()
	case "an unknown key":
		body["id"] = kernel.NewULID().String()
	default:
		if _, err := db.Query(ctx, fmt.Sprintf(`UPDATE api_keys SET %s WHERE id = '%s'`,
			change, harness.UUIDOf(harness.Home.Key))); err != nil {
			return err
		}
		defer func() {
			_, _ = db.Query(context.WithoutCancel(ctx), fmt.Sprintf(`UPDATE api_keys
				SET disabled_at = NULL, disabled_reason = NULL, expires_at = NULL, deleted_at = NULL
				WHERE id = '%s'`, harness.UUIDOf(harness.Home.Key)))
		}()
	}
	c, err := a.w.Client("A", "")
	if err != nil {
		return err
	}
	resp, err := c.Do(ctx, http.MethodPost, "/api/v1/auth/token", body)
	if resp == nil {
		return err
	}
	r := harness.Response{Status: resp.Status, Header: resp.Header, Body: resp.Body, Err: err}
	a.w.Sent = append(a.w.Sent, r)
	a.refusals = append(a.refusals, r)
	return harness.IsProblem(r, "exchanging "+how, "401", "unauthenticated")
}

// sameProblem checks that the refusals cannot be told apart: one status,
// type, title and detail.
func (a *authSteps) sameProblem() error {
	var first sdk.Problem
	for i, r := range a.refusals {
		p, _ := sdk.AsProblem(r.Err)
		p.Instance = ""
		if i == 0 {
			first = p
			continue
		}
		if p.Type != first.Type || p.Title != first.Title || p.Detail != first.Detail ||
			p.Status != first.Status {
			return fmt.Errorf("%w: refusal %d is %s %q, the first %s %q",
				harness.ErrStep, i+1, p.Type, p.Detail, first.Type, first.Detail)
		}
	}
	return nil
}

func (a *authSteps) memberSession(ctx context.Context) error {
	db, err := a.w.Querier(ctx)
	if err != nil {
		return err
	}
	if err := harness.Home.Seed(ctx, db); err != nil {
		return err
	}
	_, err = db.Query(ctx, fmt.Sprintf(
		`
		INSERT INTO members (organization_id, id, login, name, email, status)
			VALUES ('%[1]s', '%[2]s', 'alice', 'Alice', 'alice@example.com', 'active');
		INSERT INTO account_users (account_id, organization_id, member_id)
			VALUES ('%[3]s', '%[1]s', '%[2]s');
		INSERT INTO sessions (organization_id, account_id, id, channel, actor_kind, actor_id,
				member_id, login, name, email, ip_address, expires_at, idle_expires_at)
			VALUES ('%[1]s', '%[3]s', '%[4]s', 'WEB', 'user', '%[2]s', '%[2]s', 'alice', 'Alice',
				'alice@example.com', '192.0.2.44', now() + interval '1 day', now() + interval '1 hour');`,
		harness.UUIDOf(
			harness.Home.Org,
		),
		harness.UUIDOf(harness.MemberID),
		harness.UUIDOf(harness.Home.Account),
		harness.UUIDOf(harness.MemberSession),
	))
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
func listSessions(
	ctx context.Context,
	w *harness.World,
	replica, name string,
) ([]listedSession, error) {
	var out []listedSession
	path := "/api/v1/sessions?limit=2"
	for {
		r, err := w.Do(ctx, replica, name, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		var page struct {
			Items      []listedSession `json:"items"`
			NextCursor *string         `json:"nextCursor"`
		}
		if err := json.Unmarshal(r.Body, &page); err != nil || r.Status != http.StatusOK {
			return nil, fmt.Errorf("%w: sessions answered %d %s", harness.ErrStep, r.Status, r.Body)
		}
		out = append(out, page.Items...)
		if page.NextCursor == nil {
			return out, nil
		}
		path = "/api/v1/sessions?limit=2&cursor=" + *page.NextCursor
	}
}

func (a *authSteps) listedWithMember(ctx context.Context, replica, name string, n int) error {
	got, err := listSessions(ctx, a.w, replica, name)
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
		case harness.MemberSession:
			member = !s.Current && s.Type == "USER" && s.Channel == "WEB" && s.Login == "alice" &&
				s.IPAddress == "192.0.2.44"
		}
	}
	if len(got) != n || !current || !member {
		return fmt.Errorf("%w: %d sessions %+v, want %d with %s current and %s the member's",
			harness.ErrStep, len(got), got, n, own, harness.MemberSession)
	}
	return nil
}

func (a *authSteps) listedAlone(ctx context.Context, replica, name string) error {
	got, err := listSessions(ctx, a.w, replica, name)
	if err != nil {
		return err
	}
	own, err := a.session(name)
	if err != nil {
		return err
	}
	if len(got) != 1 || got[0].ID != own || !got[0].Current {
		return fmt.Errorf("%w: sessions %+v, want %s alone", harness.ErrStep, got, own)
	}
	return nil
}

func (a *authSteps) deleteSession(ctx context.Context, victim, replica, name string) error {
	id, err := a.session(victim)
	if err != nil {
		return err
	}
	r, err := a.w.Do(ctx, replica, name, http.MethodDelete, "/api/v1/sessions/"+id, nil)
	if err != nil {
		return err
	}
	var s listedSession
	if err := json.Unmarshal(r.Body, &s); err != nil || r.Status != http.StatusOK || s.ID != id {
		return fmt.Errorf("%w: sessionDelete answered %d %s", harness.ErrStep, r.Status, r.Body)
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
	r, err := a.w.Do(ctx, replica, name, http.MethodDelete, "/api/v1/sessions/"+id, nil)
	if err != nil {
		return err
	}
	return harness.IsProblem(r, "deleting token "+victim+"'s session", code, slug)
}

func (a *authSteps) deleteOthers(ctx context.Context, name, replica string) error {
	r, err := a.w.Do(ctx, replica, name, http.MethodDelete, "/api/v1/sessions", nil)
	if err != nil {
		return err
	}
	var revoked []listedSession
	if err := json.Unmarshal(r.Body, &revoked); err != nil || r.Status != http.StatusOK ||
		len(revoked) == 0 {
		return fmt.Errorf("%w: sessionDeleteAll answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	return nil
}

func (a *authSteps) logout(ctx context.Context, name, replica string) error {
	r, err := a.w.Do(ctx, replica, name, http.MethodPost, "/api/v1/auth/logout", nil)
	if err != nil {
		return err
	}
	var s listedSession
	if err := json.Unmarshal(r.Body, &s); err != nil || r.Status != http.StatusOK || !s.Current {
		return fmt.Errorf("%w: logout answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	return nil
}

func (a *authSteps) viewerAnswers(ctx context.Context, replica, name string, code int) error {
	r, err := a.w.Do(ctx, replica, name, http.MethodGet, "/api/v1/viewer", nil)
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

func (a *authSteps) answersProblem(
	ctx context.Context,
	method, path, replica, name, code, slug string,
) error {
	r, err := a.w.Do(ctx, replica, name, method, path, nil)
	if err != nil {
		return err
	}
	return harness.IsProblem(r, method+" "+path, code, slug)
}

func (a *authSteps) exchangeProblem(ctx context.Context, replica, code, slug string) error {
	c, err := a.w.Client(replica, "")
	if err != nil {
		return err
	}
	resp, err := c.Do(ctx, http.MethodPost, "/api/v1/auth/token",
		map[string]string{"id": harness.Home.Key, "secret": harness.Home.Secret()})
	if resp == nil {
		return err
	}
	r := harness.Response{Status: resp.Status, Header: resp.Header, Body: resp.Body, Err: err}
	a.w.Sent = append(a.w.Sent, r)
	return harness.IsProblem(r, "exchanging the home key", code, slug)
}

// viewerRevoked waits at most revocationWithin for the replica to refuse
// a token whose session another replica revoked.
func (a *authSteps) viewerRevoked(ctx context.Context, replica, name string) error {
	ctx, cancel := context.WithTimeout(ctx, revocationWithin)
	defer cancel()
	return deps.Eventually(ctx, "the revocation to reach "+replica,
		func(ctx context.Context) (bool, error) {
			r, err := a.w.Do(ctx, replica, name, http.MethodGet, "/api/v1/viewer", nil)
			if err != nil {
				return false, err
			}
			return harness.IsProblem(r, "viewer", "401", "unauthenticated") == nil, nil
		})
}
