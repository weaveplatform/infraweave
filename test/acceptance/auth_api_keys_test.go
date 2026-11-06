package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// keySteps are the steps of decision 0006's API keys, on authSteps'
// servers and named tokens.
type keySteps struct {
	a *authSteps
	// keys are the scenario's created keys by name.
	keys map[string]*createdKey
}

// createdKey is what the scenario knows of a key it created.
type createdKey struct {
	id, secret, first string
	// answer is create's answer.
	answer apiKeyView
	// subject and client are an OIDC key's.
	subject, client string
	expiresAt       int64
}

type apiKeyView struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Teams       []string `json:"teams"`
	Secret      *string  `json:"secret"`
	Deleted     bool     `json:"deleted"`
	Admin       bool     `json:"admin"`
	Type        string   `json:"type"`
	ExpiresAt   *int64   `json:"expiresAt"`
}

func (k *keySteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^two infraweave servers, A and B, on one database, allowing the shared OIDC issuer$`,
		k.servers)
	sc.Step(`^token "(\w+)" creates the secret key "(\w+)" with team "(\w+)" through ([AB])$`,
		func(ctx context.Context, tok, name, team, replica string) error {
			return k.create(ctx, tok, name, team, replica, 0)
		})
	sc.Step(
		`^token "(\w+)" creates the secret key "(\w+)" with team "(\w+)", expiring in an hour, through ([AB])$`,
		func(ctx context.Context, tok, name, team, replica string) error {
			return k.create(ctx, tok, name, team, replica, time.Now().Add(time.Hour).Unix())
		},
	)
	sc.Step(
		`^the key "(\w+)" was answered with a secret, which reading it through ([AB]) does not answer$`,
		k.secretOnce,
	)
	sc.Step(`^the key "(\w+)" was answered without a secret$`, k.noSecret)
	sc.Step(`^the key "(\w+)" exchanges through ([AB]) for token "(\w+)"$`, k.exchange)
	sc.Step(`^the key "(\w+)" exchanges through ([AB]) for a token valid until the key expires$`,
		k.exchangeUntilExpiry)
	sc.Step(
		`^the viewer through ([AB]) with token "(\w+)" is the key "(\w+)" with teams? "([^"]*)"$`,
		k.viewer,
	)
	sc.Step(
		`^the keys listed through ([AB]) with token "(\w+)" are the seeded key and "(\w+)", with no secret$`,
		func(ctx context.Context, replica, tok, name string) error {
			return k.listed(ctx, replica, tok, name)
		},
	)
	sc.Step(
		`^the keys listed through ([AB]) with token "(\w+)" are the seeded key alone$`,
		func(ctx context.Context, replica, tok string) error { return k.listed(ctx, replica, tok, "") },
	)
	sc.Step(`^token "(\w+)" updates the key "(\w+)" through ([AB]) with '([^']*)'$`, k.update)
	sc.Step(
		`^reading the key "(\w+)" through ([AB]) with token "(\w+)" answers name "([^"]*)", teams "([^"]*)" and description "([^"]*)"$`,
		k.read,
	)
	sc.Step(`^token "(\w+)" resets the key "(\w+)" through ([AB])$`, k.reset)
	sc.Step(
		`^token "(\w+)" deletes the key "(\w+)" through ([AB]), which answers it deleted$`,
		k.delete,
	)
	sc.Step(`^the key "(\w+)" is disabled$`, k.disable)
	sc.Step(`^exchanging the key "(\w+)" answers a 401 problem "unauthenticated"$`,
		func(ctx context.Context, name string) error { return k.refused(ctx, name, false) })
	sc.Step(
		`^exchanging the key "(\w+)" with its first secret answers a 401 problem "unauthenticated"$`,
		func(ctx context.Context, name string) error { return k.refused(ctx, name, true) },
	)
	sc.Step(
		`^reading the key "(\w+)" through ([AB]) with token "(\w+)" answers a (\d+) problem "([a-z-]+)"$`,
		k.readRefused,
	)
	sc.Step(
		`^creating a key through ([AB]) with token "(\w+)" and '([^']*)' answers a (\d+) problem "([a-z-]+)"$`,
		k.createRefused,
	)
	sc.Step(
		`^token "(\w+)" reading, updating, resetting and deleting the key "(\w+)" through ([AB]) each answer a 404 problem "not-found"$`,
		k.foreign,
	)
	sc.Step(
		`^token "(\w+)" creates an OIDC key "(\w+)" for the shared issuer, client "([^"]+)", subject "([^"]+)" and claim "([^"]+)" as groups, through ([AB])$`,
		k.createOIDC,
	)
	sc.Step(
		`^the key "(\w+)" exchanges through ([AB]) an issuer token with sub "([^"]+)", aud "([^"]+)" and roles "([^"]+)" for token "(\w+)"$`,
		k.exchangeOIDC,
	)
	sc.Step(
		`^exchanging the key "(\w+)" with an issuer token whose (sub|aud|iss) is "([^"]+)" answers a 401 problem "unauthenticated"$`,
		func(ctx context.Context, name, claim, value string) error {
			return k.refusedOIDC(ctx, name, map[string]any{claim: value})
		},
	)
	sc.Step(
		`^exchanging the key "(\w+)" with an issuer token that expired an hour ago answers a 401 problem "unauthenticated"$`,
		func(ctx context.Context, name string) error {
			return k.refusedOIDC(
				ctx,
				name,
				map[string]any{"exp": time.Now().Add(-time.Hour).Unix()},
			)
		},
	)
	sc.Step(
		`^creating an OIDC key through ([AB]) with token "(\w+)" for the shared issuer over plain HTTP answers a 422 problem "validation-failed"$`,
		func(ctx context.Context, replica, tok string) error {
			iss, err := k.a.s.w.s.deps.OIDC()
			if err != nil {
				return err
			}
			return k.oidcRefused(
				ctx,
				replica,
				tok,
				strings.Replace(iss.URL(), "https://", "http://", 1),
			)
		},
	)
	sc.Step(
		`^creating an OIDC key through ([AB]) with token "(\w+)" for "([^"]+)" answers a 422 problem "validation-failed"$`,
		k.oidcRefused,
	)
	sc.Step(
		`^"([A-Z]+) ([^"]+)" through ([AB]) with token "(\w+)" and '([^']*)' answers a (\d+) problem "([a-z-]+)"$`,
		k.sendRefused,
	)
	sc.Step(
		`^setting non-admin API key managers through ([AB]) with token "(\w+)" to (true|false) answers (true|false)$`,
		k.managers,
	)
}

// servers starts the scenario's two servers trusting the shared issuer's
// certificate through auth.oidc_api_keys.ca_file and allowing it alone.
func (k *keySteps) servers(ctx context.Context) error {
	w := k.a.s.w
	iss, err := w.s.deps.OIDC()
	if err != nil {
		return err
	}
	ca := filepath.Join(w.dir, "oidc-ca.pem")
	if err := os.WriteFile(ca, iss.CertPEM(), 0o600); err != nil {
		return fmt.Errorf("oidc ca: %w", err)
	}
	w.auth = map[string]any{"oidc_api_keys": map[string]any{
		"allowed_issuers": []string{iss.URL()},
		"ca_file":         ca,
	}}
	return k.a.s.twoReplicas(ctx)
}

// call sends method to path through replica with the named token, and
// decodes a 2xx answer into out when out is not nil.
func (k *keySteps) call(
	ctx context.Context,
	replica, tok, method, path string,
	body, out any,
	want int,
) (response, error) {
	r, err := k.a.do(ctx, replica, tok, method, path, body)
	if err != nil {
		return r, err
	}
	if want != 0 && r.status != want {
		return r, fmt.Errorf("%w: %s %s answered %d, want %d: %s", errStep, method, path, r.status,
			want, r.body)
	}
	if out != nil {
		if err := json.Unmarshal(r.body, out); err != nil {
			return r, fmt.Errorf("%w: %s %s: %w: %s", errStep, method, path, err, r.body)
		}
	}
	return r, nil
}

func (k *keySteps) key(name string) (*createdKey, error) {
	c, ok := k.keys[name]
	if !ok {
		return nil, fmt.Errorf("%w: no key %q", errStep, name)
	}
	return c, nil
}

func (k *keySteps) create(
	ctx context.Context,
	tok, name, team, replica string,
	expires int64,
) error {
	body := map[string]any{
		"name": name, "description": "created by acceptance", "teams": []string{team},
		"machine": true,
	}
	if expires != 0 {
		body["expiresAt"] = expires
	}
	var v apiKeyView
	if _, err := k.call(ctx, replica, tok, http.MethodPost, "/api/v1/api-keys", body, &v,
		http.StatusCreated); err != nil {
		return err
	}
	if v.Secret == nil || v.Name != name || v.Admin || v.Type != "SECRET" {
		return fmt.Errorf("%w: created %+v", errStep, v)
	}
	k.keys[name] = &createdKey{
		id:        v.ID,
		secret:    *v.Secret,
		first:     *v.Secret,
		answer:    v,
		expiresAt: expires,
	}
	return nil
}

func (k *keySteps) secretOnce(ctx context.Context, name, replica string) error {
	c, err := k.key(name)
	if err != nil {
		return err
	}
	var v apiKeyView
	if _, err := k.call(ctx, replica, "admin", http.MethodGet, "/api/v1/api-keys/"+c.id, nil, &v,
		http.StatusOK); err != nil {
		return err
	}
	if c.answer.Secret == nil || !strings.HasPrefix(*c.answer.Secret, "iwk_") || v.Secret != nil ||
		v.ID != c.id {
		return fmt.Errorf("%w: create answered %+v, read %+v", errStep, c.answer, v)
	}
	return nil
}

func (k *keySteps) noSecret(name string) error {
	c, err := k.key(name)
	if err != nil {
		return err
	}
	if c.answer.Secret != nil {
		return fmt.Errorf("%w: the OIDC key was answered with a secret", errStep)
	}
	return nil
}

// exchangeWith trades the key id for a token with secret through replica.
func (k *keySteps) exchangeWith(ctx context.Context, replica, id, secret string) (response, error) {
	return k.a.do(ctx, replica, "", http.MethodPost, "/api/v1/auth/token",
		map[string]string{"id": id, "secret": secret})
}

func (k *keySteps) exchange(ctx context.Context, name, replica, tok string) error {
	c, err := k.key(name)
	if err != nil {
		return err
	}
	r, err := k.exchangeWith(ctx, replica, c.id, c.secret)
	if err != nil {
		return err
	}
	return k.keep(r, tok)
}

// keep names the token of an exchange's answer.
func (k *keySteps) keep(r response, tok string) error {
	var u struct {
		JWT string `json:"jwt"`
	}
	if err := json.Unmarshal(r.body, &u); err != nil || r.status != http.StatusOK || u.JWT == "" {
		return fmt.Errorf("%w: the exchange answered %d %s", errStep, r.status, r.body)
	}
	k.a.tokens[tok] = u.JWT
	return nil
}

func (k *keySteps) exchangeUntilExpiry(ctx context.Context, name, replica string) error {
	c, err := k.key(name)
	if err != nil {
		return err
	}
	r, err := k.exchangeWith(ctx, replica, c.id, c.secret)
	if err != nil {
		return err
	}
	var u struct {
		ValidUntil int64 `json:"validUntil"`
	}
	if err := json.Unmarshal(r.body, &u); err != nil || r.status != http.StatusOK ||
		u.ValidUntil > c.expiresAt || u.ValidUntil < c.expiresAt-5 {
		return fmt.Errorf("%w: the token is valid until %d, the key expires at %d: %s",
			errStep, u.ValidUntil, c.expiresAt, r.body)
	}
	return nil
}

func (k *keySteps) viewer(ctx context.Context, replica, tok, name, teams string) error {
	c, err := k.key(name)
	if err != nil {
		return err
	}
	var u struct {
		ID    string   `json:"id"`
		Name  string   `json:"name"`
		Teams []string `json:"teams"`
	}
	if _, err := k.call(ctx, replica, tok, http.MethodGet, "/api/v1/viewer", nil, &u,
		http.StatusOK); err != nil {
		return err
	}
	if u.ID != "api::"+c.id || u.Name != name || !slices.Equal(u.Teams, strings.Split(teams, ",")) {
		return fmt.Errorf("%w: viewer %+v, want key %s with teams %s", errStep, u, c.id, teams)
	}
	return nil
}

// listed checks that the account's keys, followed page by page, are the
// seeded key and the key name, or the seeded key alone for no name, and
// answer no secret.
func (k *keySteps) listed(ctx context.Context, replica, tok, name string) error {
	var ids []string
	path := "/api/v1/api-keys?limit=1"
	for {
		var page struct {
			Items      []apiKeyView `json:"items"`
			NextCursor *string      `json:"nextCursor"`
		}
		if _, err := k.call(
			ctx,
			replica,
			tok,
			http.MethodGet,
			path,
			nil,
			&page,
			http.StatusOK,
		); err != nil {
			return err
		}
		for _, v := range page.Items {
			if v.Secret != nil {
				return fmt.Errorf("%w: the list answered %s's secret", errStep, v.ID)
			}
			ids = append(ids, v.ID)
		}
		if page.NextCursor == nil {
			break
		}
		path = "/api/v1/api-keys?limit=1&cursor=" + *page.NextCursor
	}
	want := []string{home.key}
	if name != "" {
		c, err := k.key(name)
		if err != nil {
			return err
		}
		want = append(want, c.id)
	}
	slices.Sort(ids)
	slices.Sort(want)
	if !slices.Equal(ids, want) {
		return fmt.Errorf("%w: listed %v, want %v", errStep, ids, want)
	}
	return nil
}

func (k *keySteps) update(ctx context.Context, tok, name, replica, body string) error {
	c, err := k.key(name)
	if err != nil {
		return err
	}
	_, err = k.call(ctx, replica, tok, http.MethodPut, "/api/v1/api-keys/"+c.id,
		json.RawMessage(body), nil, http.StatusOK)
	return err
}

func (k *keySteps) read(
	ctx context.Context,
	name, replica, tok, wantName, teams, desc string,
) error {
	c, err := k.key(name)
	if err != nil {
		return err
	}
	var v apiKeyView
	if _, err := k.call(ctx, replica, tok, http.MethodGet, "/api/v1/api-keys/"+c.id, nil, &v,
		http.StatusOK); err != nil {
		return err
	}
	if v.Name != wantName || !slices.Equal(v.Teams, strings.Split(teams, ",")) ||
		v.Description != desc {
		return fmt.Errorf("%w: read %+v", errStep, v)
	}
	return nil
}

func (k *keySteps) reset(ctx context.Context, tok, name, replica string) error {
	c, err := k.key(name)
	if err != nil {
		return err
	}
	var v apiKeyView
	if _, err := k.call(ctx, replica, tok, http.MethodPost, "/api/v1/api-keys/"+c.id+"/reset", nil,
		&v, http.StatusOK); err != nil {
		return err
	}
	if v.Secret == nil || *v.Secret == c.secret {
		return fmt.Errorf("%w: reset answered %+v", errStep, v)
	}
	c.secret = *v.Secret
	return nil
}

func (k *keySteps) delete(ctx context.Context, tok, name, replica string) error {
	c, err := k.key(name)
	if err != nil {
		return err
	}
	var v apiKeyView
	if _, err := k.call(ctx, replica, tok, http.MethodDelete, "/api/v1/api-keys/"+c.id, nil, &v,
		http.StatusOK); err != nil {
		return err
	}
	if !v.Deleted || v.ID != c.id {
		return fmt.Errorf("%w: delete answered %+v", errStep, v)
	}
	return nil
}

// disable disables the key in the database, as no P3 operation disables
// an account's key; personalApiKeyDisable is P3b's.
func (k *keySteps) disable(ctx context.Context, name string) error {
	c, err := k.key(name)
	if err != nil {
		return err
	}
	db, err := k.a.s.w.querier(ctx)
	if err != nil {
		return err
	}
	_, err = db.Query(ctx, fmt.Sprintf(
		`UPDATE api_keys SET disabled_at = now(), disabled_reason = 'ADMIN' WHERE id = '%s'`,
		uuidOf(c.id),
	))
	return err
}

func (k *keySteps) refused(ctx context.Context, name string, first bool) error {
	c, err := k.key(name)
	if err != nil {
		return err
	}
	secret := c.secret
	if first {
		secret = c.first
	}
	r, err := k.exchangeWith(ctx, "A", c.id, secret)
	if err != nil {
		return err
	}
	return isProblem(r, "exchanging the key "+name, "401", "unauthenticated")
}

func (k *keySteps) readRefused(ctx context.Context, name, replica, tok, code, slug string) error {
	c, err := k.key(name)
	if err != nil {
		return err
	}
	r, err := k.call(ctx, replica, tok, http.MethodGet, "/api/v1/api-keys/"+c.id, nil, nil, 0)
	if err != nil {
		return err
	}
	return isProblem(r, "reading the key "+name, code, slug)
}

func (k *keySteps) createRefused(ctx context.Context, replica, tok, body, code, slug string) error {
	r, err := k.call(ctx, replica, tok, http.MethodPost, "/api/v1/api-keys", json.RawMessage(body),
		nil, 0)
	if err != nil {
		return err
	}
	return isProblem(r, "creating "+body, code, slug)
}

// foreign checks that the named token's account reaches the key by none of
// the operations on it, and that none of them changed it.
func (k *keySteps) foreign(ctx context.Context, tok, name, replica string) error {
	c, err := k.key(name)
	if err != nil {
		return err
	}
	path := "/api/v1/api-keys/" + c.id
	for _, op := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, path, nil},
		{http.MethodPut, path, map[string]any{"name": "taken"}},
		{http.MethodPost, path + "/reset", nil},
		{http.MethodDelete, path, nil},
	} {
		r, err := k.call(ctx, replica, tok, op.method, op.path, op.body, nil, 0)
		if err != nil {
			return err
		}
		if err := isProblem(r, op.method+" "+op.path, "404", "not-found"); err != nil {
			return err
		}
	}
	return nil
}

func (k *keySteps) createOIDC(
	ctx context.Context,
	tok, name, client, subject, claim, replica string,
) error {
	iss, err := k.a.s.w.s.deps.OIDC()
	if err != nil {
		return err
	}
	body := map[string]any{"name": name, "teams": []string{}, "oidc": map[string]any{
		"issuer":            iss.URL(),
		"clientId":          client,
		"subjectExpression": subject,
		"claimMappings": map[string]any{
			"entries": []map[string]string{{"name": claim, "value": "groups"}},
		},
	}}
	var v apiKeyView
	if _, err := k.call(ctx, replica, tok, http.MethodPost, "/api/v1/api-keys", body, &v,
		http.StatusCreated); err != nil {
		return err
	}
	if v.Type != "OIDC" {
		return fmt.Errorf("%w: created %+v", errStep, v)
	}
	k.keys[name] = &createdKey{id: v.ID, answer: v, subject: subject, client: client}
	return nil
}

// issuerToken mints a token of the shared issuer for the key: its subject
// and client as sub and aud, with claims over them.
func (k *keySteps) issuerToken(c *createdKey, claims map[string]any) (string, error) {
	iss, err := k.a.s.w.s.deps.OIDC()
	if err != nil {
		return "", err
	}
	all := map[string]any{"sub": c.subject, "aud": c.client}
	for name, v := range claims {
		all[name] = v
	}
	return iss.Mint(all) //nolint:wrapcheck // names itself
}

func (k *keySteps) exchangeOIDC(
	ctx context.Context,
	name, replica, sub, aud, roles, tok string,
) error {
	c, err := k.key(name)
	if err != nil {
		return err
	}
	raw, err := k.issuerToken(
		c,
		map[string]any{"sub": sub, "aud": aud, "roles": strings.Split(roles, ",")},
	)
	if err != nil {
		return err
	}
	r, err := k.exchangeWith(ctx, replica, c.id, raw)
	if err != nil {
		return err
	}
	return k.keep(r, tok)
}

func (k *keySteps) refusedOIDC(ctx context.Context, name string, claims map[string]any) error {
	c, err := k.key(name)
	if err != nil {
		return err
	}
	raw, err := k.issuerToken(c, claims)
	if err != nil {
		return err
	}
	r, err := k.exchangeWith(ctx, "A", c.id, raw)
	if err != nil {
		return err
	}
	return isProblem(
		r,
		fmt.Sprintf("exchanging the key %s with %v", name, claims),
		"401",
		"unauthenticated",
	)
}

func (k *keySteps) oidcRefused(ctx context.Context, replica, tok, issuer string) error {
	r, err := k.call(ctx, replica, tok, http.MethodPost, "/api/v1/api-keys", map[string]any{
		"name": "refused", "teams": []string{}, "oidc": map[string]any{
			"issuer": issuer, "clientId": "infraweave", "subjectExpression": "repo:.*",
		},
	}, nil, 0)
	if err != nil {
		return err
	}
	return isProblem(r, "creating an OIDC key for "+issuer, "422", "validation-failed")
}

func (k *keySteps) sendRefused(
	ctx context.Context,
	method, path, replica, tok, body, code, slug string,
) error {
	r, err := k.call(ctx, replica, tok, method, path, json.RawMessage(body), nil, 0)
	if err != nil {
		return err
	}
	return isProblem(r, method+" "+path, code, slug)
}

func (k *keySteps) managers(ctx context.Context, replica, tok, enable, want string) error {
	var got bool
	if _, err := k.call(
		ctx,
		replica,
		tok,
		http.MethodPut,
		"/api/v1/account/api-key-non-admin-managers",
		map[string]bool{"enable": enable == "true"},
		&got,
		http.StatusOK,
	); err != nil {
		return err
	}
	if fmt.Sprint(got) != want {
		return fmt.Errorf("%w: answered %v, want %s", errStep, got, want)
	}
	return nil
}
