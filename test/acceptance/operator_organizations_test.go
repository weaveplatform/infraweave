package acceptance

import (
	"bytes"
	"context"
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/pkg/sdk"
)

// jsonType is the media type of every request body.
const jsonType = "application/json"

// operatorKey is the scenario's operator key; its secret derives from it
// as a tenant's does, so no secret is written down.
const operatorKey = "01KA2V0P3RAT0RKEY000000000"

func operatorSecret() string {
	h := sha256.Sum256([]byte("acceptance:" + operatorKey))
	return "iwk_" + base64.RawURLEncoding.EncodeToString(h[:])
}

// operatorSteps are the steps of the operator API, on authSteps' servers
// and named tokens.
type operatorSteps struct {
	a *authSteps
	// hash is the password hash infraweave auth hash-password printed.
	hash string
}

func (o *operatorSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^two infraweave servers, A and B, on one database, with an operator key$`, o.servers)
	sc.Step(`^the password "([^"]+)" hashed by infraweave auth hash-password$`, o.hashPassword)
	sc.Step(`^the hash verifies "([^"]+)" and not "([^"]+)"$`, o.verifies)
	sc.Step(`^the operator key is exchanged through ([AB]) for token "(\w+)"$`, o.exchange)
	sc.Step(
		`^token "(\w+)" creates the organization "([a-z-]+)" with the account "([a-z-]+)" and the break-glass login "([^"]+)" through ([AB])$`,
		o.create,
	)
	sc.Step(
		`^the organizations listed through ([AB]) with token "(\w+)" (include|leave out) "([a-z-]+)"$`,
		o.listed,
	)
	sc.Step(
		`^the organization "([a-z-]+)" read through ([AB]) with token "(\w+)" has the account "([a-z-]+)", unclaimed, and (?:the break-glass login "([^"]+)"|no break-glass credential)$`,
		o.read,
	)
	sc.Step(
		`^token "(\w+)" sets the break-glass login of "([a-z-]+)" to "([^"]+)" through ([AB])$`,
		o.setBreakGlass,
	)
	sc.Step(`^token "(\w+)" removes the break-glass credential of "([a-z-]+)" through ([AB])$`,
		o.deleteBreakGlass)
	sc.Step(`^token "(\w+)" deletes the organization "([a-z-]+)" through ([AB])$`, o.delete)
	sc.Step(
		`^the trail of "([a-z-]+)" holds "([a-z_.]+)", "([a-z_.]+)", "([a-z_.]+)" and "([a-z_.]+)" by the operator$`,
		func(ctx context.Context, slug, a, b, c, d string) error { return o.trail(ctx, slug, a, b, c, d) },
	)
	sc.Step(
		`^creating the organization "([a-z-]+)" again through ([AB]) with token "(\w+)" answers a (\d+) problem "([a-z-]+)"$`,
		o.createAgain,
	)
	sc.Step(
		`^creating the organization "([a-z-]+)" with a hash of (\d+) iterations through ([AB]) with token "(\w+)" answers a (\d+) problem "([a-z-]+)" naming "([^"]+)"$`,
		o.createWeak,
	)
	sc.Step(`^token "(\w+)" carries the claims of an operator token$`, o.claims)
}

// operatorAuth is the auth settings that hold the operator key in
// auth.operator.keys.
func operatorAuth() map[string]any {
	sum := sha256.Sum256([]byte(operatorSecret()))
	return map[string]any{"operator": map[string]any{"keys": []map[string]any{{
		"id": operatorKey, "secret_sha256": fmt.Sprintf("%x", sum),
	}}}}
}

// servers starts the scenario's two servers with the operator key.
func (o *operatorSteps) servers(ctx context.Context) error {
	o.a.s.w.auth = operatorAuth()
	return o.a.s.twoReplicas(ctx)
}

// hashPassword runs infraweave auth hash-password with password on stdin,
// in FIPS 140-3 only mode, so the hash is one a FIPS build can make.
func (o *operatorSteps) hashPassword(ctx context.Context, password string) error {
	bin, err := binary(ctx)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, bin, "auth", "hash-password")
	cmd.Env = append(os.Environ(), "GOCOVERDIR="+o.a.s.w.s.coverDir, "GODEBUG=fips140=only")
	cmd.Stdin = strings.NewReader(password + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("infraweave auth hash-password: %w: %s", err, stderr.String())
	}
	o.hash = strings.TrimSuffix(string(out), "\n")
	return nil
}

// verifies checks the hash against decision 0006's format with the
// standard library alone, so the check does not share infraweave's code.
func (o *operatorSteps) verifies(good, bad string) error {
	rest, ok := strings.CutPrefix(o.hash, "$pbkdf2-sha256$i=")
	parts := strings.Split(rest, "$")
	if !ok || len(parts) != 3 {
		return fmt.Errorf("%w: hash %q is not $pbkdf2-sha256$i=<n>$<salt>$<hash>", errStep, o.hash)
	}
	n, err := strconv.Atoi(parts[0])
	salt, serr := base64.RawStdEncoding.DecodeString(parts[1])
	key, kerr := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || serr != nil || kerr != nil || n != 600_000 || len(salt) != 16 ||
		len(key) != 32 {
		return fmt.Errorf("%w: hash %q is not 600000 iterations, a 16-byte salt and a 32-byte key",
			errStep, o.hash)
	}
	matches := func(password string) bool {
		k, err := pbkdf2.Key(sha256.New, password, salt, n, len(key))
		return err == nil && subtle.ConstantTimeCompare(k, key) == 1
	}
	if !matches(good) || matches(bad) {
		return fmt.Errorf("%w: hash %q verifies %q %t and %q %t", errStep, o.hash,
			good, matches(good), bad, matches(bad))
	}
	return nil
}

func (o *operatorSteps) exchange(ctx context.Context, replica, name string) error {
	r, err := o.a.do(ctx, replica, "", http.MethodPost, "/api/v1/auth/token",
		map[string]string{"id": operatorKey, "secret": operatorSecret()})
	if err != nil {
		return err
	}
	var u struct {
		JWT string `json:"jwt"`
	}
	if err := json.Unmarshal(r.body, &u); err != nil || r.status != http.StatusOK || u.JWT == "" {
		return fmt.Errorf(
			"%w: exchanging the operator key answered %d %s",
			errStep,
			r.status,
			r.body,
		)
	}
	o.a.tokens[name] = u.JWT
	return nil
}

// client is the operator API's client of pkg/sdk on the replica's API
// listener, holding the named token.
func (o *operatorSteps) client(replica, name string) (*sdk.Client, error) {
	return o.a.client(replica, o.a.tokens[name])
}

// jsonOf is v as a request body.
func jsonOf(v any) io.Reader {
	b, _ := json.Marshal(v) //nolint:errchkjson // maps of strings
	return bytes.NewReader(b)
}

// input is an organization create body: slug with the account and the
// break-glass login and hash.
func input(slug, account, login, hash string) io.Reader {
	return jsonOf(map[string]any{
		"slug": slug, "name": "Initech",
		"account":    map[string]string{"slug": account, "name": "Main"},
		"breakGlass": map[string]string{"login": login, "passwordHash": hash},
	})
}

// operatorOrganization is the operator API's Organization, as a step
// reads it.
type operatorOrganization struct {
	Slug     string `json:"slug"`
	Accounts []struct {
		Slug    string `json:"slug"`
		Claimed bool   `json:"claimed"`
	} `json:"accounts"`
	BreakGlass *struct {
		Login string `json:"login"`
	} `json:"breakGlass"`
}

func (o *operatorSteps) create(
	ctx context.Context,
	name, slug, account, login, replica string,
) error {
	c, err := o.client(replica, name)
	if err != nil {
		return err
	}
	resp, err := c.Operator.OrganizationCreateWithBodyWithResponse(ctx, nil, jsonType,
		input(slug, account, login, o.hash))
	if err != nil {
		return fmt.Errorf("create %s: %w", slug, err)
	}
	if resp.StatusCode() != http.StatusCreated {
		return fmt.Errorf(
			"%w: create %s answered %d %s",
			errStep,
			slug,
			resp.StatusCode(),
			resp.Body,
		)
	}
	return organization(resp.Body, slug, account, login)
}

// organization checks that body is slug with its one account, unclaimed,
// and the break-glass login, or none when login is empty.
func organization(body []byte, slug, account, login string) error {
	var got operatorOrganization
	ok := json.Unmarshal(body, &got) == nil && got.Slug == slug && len(got.Accounts) == 1 &&
		got.Accounts[0].Slug == account &&
		!got.Accounts[0].Claimed
	if login == "" {
		ok = ok && got.BreakGlass == nil
	} else {
		ok = ok && got.BreakGlass != nil && got.BreakGlass.Login == login
	}
	if !ok {
		return fmt.Errorf(
			"%w: organization %s, want %s with %s unclaimed and break-glass login %q",
			errStep,
			body,
			slug,
			account,
			login,
		)
	}
	return nil
}

// listed checks whether the list holds slug; the scenario's seeded
// tenants are organizations too, so it holds theirs as well.
func (o *operatorSteps) listed(ctx context.Context, replica, name, how, slug string) error {
	c, err := o.client(replica, name)
	if err != nil {
		return err
	}
	resp, err := c.Operator.OrganizationsWithResponse(ctx, nil)
	if err != nil {
		return fmt.Errorf("list organizations: %w", err)
	}
	if resp.JSON200 == nil {
		return fmt.Errorf("%w: list answered %d %s", errStep, resp.StatusCode(), resp.Body)
	}
	var got []string
	for _, org := range resp.JSON200.Items {
		got = append(got, org.Slug)
	}
	if slices.Contains(got, slug) != (how == "include") {
		return fmt.Errorf("%w: organizations %v, want them to %s %s", errStep, got, how, slug)
	}
	return nil
}

func (o *operatorSteps) read(
	ctx context.Context,
	slug, replica, name, account, login string,
) error {
	c, err := o.client(replica, name)
	if err != nil {
		return err
	}
	resp, err := c.Operator.OrganizationWithResponse(ctx, slug)
	if err != nil {
		return fmt.Errorf("read %s: %w", slug, err)
	}
	if resp.StatusCode() != http.StatusOK {
		return fmt.Errorf("%w: read %s answered %d %s", errStep, slug, resp.StatusCode(), resp.Body)
	}
	return organization(resp.Body, slug, account, login)
}

func (o *operatorSteps) setBreakGlass(
	ctx context.Context,
	name, slug, login, replica string,
) error {
	c, err := o.client(replica, name)
	if err != nil {
		return err
	}
	resp, err := c.Operator.OrganizationBreakGlassSetWithBodyWithResponse(ctx, slug, jsonType,
		jsonOf(map[string]string{"login": login, "passwordHash": o.hash}))
	if err != nil {
		return fmt.Errorf("set break glass of %s: %w", slug, err)
	}
	if resp.JSON200 == nil || resp.JSON200.BreakGlass == nil ||
		resp.JSON200.BreakGlass.Login != login {
		return fmt.Errorf(
			"%w: set break glass answered %d %s",
			errStep,
			resp.StatusCode(),
			resp.Body,
		)
	}
	return nil
}

func (o *operatorSteps) deleteBreakGlass(ctx context.Context, name, slug, replica string) error {
	c, err := o.client(replica, name)
	if err != nil {
		return err
	}
	resp, err := c.Operator.OrganizationBreakGlassDeleteWithResponse(ctx, slug)
	if err != nil {
		return fmt.Errorf("delete break glass of %s: %w", slug, err)
	}
	if resp.JSON200 == nil || resp.JSON200.BreakGlass != nil {
		return fmt.Errorf(
			"%w: delete break glass answered %d %s",
			errStep,
			resp.StatusCode(),
			resp.Body,
		)
	}
	return nil
}

func (o *operatorSteps) delete(ctx context.Context, name, slug, replica string) error {
	c, err := o.client(replica, name)
	if err != nil {
		return err
	}
	resp, err := c.Operator.OrganizationDeleteWithResponse(ctx, slug)
	if err != nil {
		return fmt.Errorf("delete %s: %w", slug, err)
	}
	if resp.JSON200 == nil || resp.JSON200.Slug != slug {
		return fmt.Errorf(
			"%w: delete %s answered %d %s",
			errStep,
			slug,
			resp.StatusCode(),
			resp.Body,
		)
	}
	return nil
}

// trail checks the organization trail's entries for slug: the actions,
// each by the operator key, outliving the organization.
func (o *operatorSteps) trail(ctx context.Context, slug string, actions ...string) error {
	db, err := o.a.s.w.querier(ctx)
	if err != nil {
		return err
	}
	got, err := db.Query(ctx, fmt.Sprintf(
		`SELECT string_agg(action, ',' ORDER BY action) FROM organization_audit_trail_entries
		 WHERE affected_type = 'ORGANIZATION' AND affected_id = '%s'
		   AND actor_kind = 'operator' AND actor_id = '%s'`, slug, operatorKey))
	if err != nil {
		return err //nolint:wrapcheck // the querier names the query
	}
	slices.Sort(actions)
	if want := strings.Join(actions, ","); strings.TrimSpace(got) != want {
		return fmt.Errorf("%w: trail of %s holds %q, want %q", errStep, slug, got, want)
	}
	return nil
}

func (o *operatorSteps) createAgain(
	ctx context.Context,
	slug, replica, name, code, problem string,
) error {
	c, err := o.client(replica, name)
	if err != nil {
		return err
	}
	resp, err := c.Operator.OrganizationCreateWithBodyWithResponse(ctx, nil, jsonType,
		input(slug, "main", "admin", o.hash))
	if err != nil {
		return fmt.Errorf("create %s: %w", slug, err)
	}
	return operatorProblem(resp.HTTPResponse, resp.Body, "creating "+slug, code, problem, "")
}

func (o *operatorSteps) createWeak(
	ctx context.Context,
	slug string,
	iterations int,
	replica, name, code, problem, field string,
) error {
	salt := make([]byte, 16)
	key, err := pbkdf2.Key(sha256.New, "correct horse", salt, iterations, 32)
	if err != nil {
		return fmt.Errorf("weak hash: %w", err)
	}
	weak := fmt.Sprintf("$pbkdf2-sha256$i=%d$%s$%s", iterations,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
	c, err := o.client(replica, name)
	if err != nil {
		return err
	}
	resp, err := c.Operator.OrganizationCreateWithBodyWithResponse(ctx, nil, jsonType,
		input(slug, "main", "admin", weak))
	if err != nil {
		return fmt.Errorf("create %s: %w", slug, err)
	}
	return operatorProblem(resp.HTTPResponse, resp.Body, "creating "+slug, code, problem, field)
}

// operatorProblem checks that an operator API answer is a problem of code
// and slug whose detail names field, when field is not empty.
func operatorProblem(r *http.Response, body []byte, what, code, slug, field string) error {
	var p struct {
		Type   string `json:"type"`
		Detail string `json:"detail"`
	}
	if strconv.Itoa(r.StatusCode) != code ||
		!strings.HasPrefix(r.Header.Get("Content-Type"), "application/problem+json") ||
		json.Unmarshal(body, &p) != nil || !strings.HasSuffix(p.Type, ":"+slug) ||
		!strings.Contains(p.Detail, field) {
		return fmt.Errorf("%w: %s answered %d %s, want a %s problem %q naming %q",
			errStep, what, r.StatusCode, body, code, slug, field)
	}
	return nil
}

// claims checks an operator token: iss the public URL, aud the operator
// API's base URL, sub operator::<key>, iw_kind operator, and a life of
// auth.tokens.api.ttl.
func (o *operatorSteps) claims(name string) error {
	var c apiClaims
	if err := jwtPart(o.a.tokens[name], 1, &c); err != nil {
		return err
	}
	want := apiClaims{
		Issuer:   publicURL,
		Subject:  "operator::" + operatorKey,
		Audience: audience{publicURL + "/operator/v1"},
		ID:       c.ID,
		Kind:     "operator",
		IssuedAt: c.IssuedAt,
		Expiry:   c.IssuedAt + int64((10*time.Hour)/time.Second),
	}
	if !equalClaims(c, want) {
		return fmt.Errorf("%w: claims %+v, want %+v", errStep, c, want)
	}
	return nil
}
