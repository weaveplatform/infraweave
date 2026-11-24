package auth

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/cucumber/godog"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// federationClaims are the claims every OIDC federation token carries,
// in the reference's order, which the discovery document lists.
//
// ref: decision 0006 § "OIDC federation token"
var federationClaims = []string{
	"iss", "sub", "aud", "exp", "iat", "jti", "nbf",
	"spaceId", "spacePath", "callerType", "callerId", "runType", "runId", "scope",
}

// privateMembers are the JWK members of a private key, none of which a
// published key may carry, and the certificate members, which the
// federation JWKS never carries either.
var privateMembers = []string{"d", "p", "q", "dp", "dq", "qi", "k", "x5c", "x5t", "x5t#S256", "x5u"}

// discoverySteps read the federation issuer's discovery document and JWKS
// as a relying party does, with no credential, and through go-oidc, which
// AWS-, GCP- and Vault-style relying parties behave like.
//
// ref: decision 0006 § "OIDC federation token"
type discoverySteps struct {
	w *harness.World
	// provider is the issuer go-oidc last discovered, and issuer its
	// identifier.
	provider *oidc.Provider
	issuer   string
}

func (d *discoverySteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^two infraweave servers, A and B, on one database, with the federation issuer "([^"]+)"(?: and the JWKS URI "([^"]+)")?$`,
		d.withIssuer)
	sc.Step(`^two infraweave servers, A and B, on one database, with oidc-federation switched off$`, d.switchedOff)
	sc.Step(`^two infraweave servers, A and B, on one database, with a standard budget of (\d+)$`, d.withBudget)
	sc.Step(`^"(GET|HEAD) ([^"]+)" with no credential through ([AB]) answers cacheable JSON$`, d.cacheable)
	sc.Step(`^the discovery document names the issuer "([^"]+)", the JWKS URI "([^"]+)" and the algorithm "([A-Z0-9]+)"$`,
		d.document)
	sc.Step(`^the JWKS holds exactly the published federation keys and no api-token key$`, d.exactKeys)
	sc.Step(`^the JWKS's one federation key is kept as "([\w.]+)"$`, d.keepOnly)
	sc.Step(`^go-oidc discovers the issuer "([^"]+)" through ([AB])$`, d.discover)
	sc.Step(`^a token signed by the active (federation|api-token) key (verifies|fails) against the discovered keys$`,
		d.verify)
	sc.Step(`^the active federation key is aged past its lifetime, and authn\.keys\.rotate publishes a next key, kept as "([\w.]+)"$`,
		d.publishNext)
	sc.Step(`^the next federation key is aged past its publication, and authn\.keys\.rotate promotes it$`, d.promote)
	sc.Step(`^the retiring federation key's tokens have expired, and authn\.keys\.rotate retires it$`, d.retire)
	sc.Step(`^the JWKS through ([AB]) eventually holds the federation keys "([^"]*)"$`, d.eventuallyHolds)
	sc.Step(`^the federation key "([\w.]+)" is (next|active|retiring|retired)$`, d.inState)
	sc.Step(`^the federation documents are read with no credential through A and B in turn until the standard budget of (\d+) is spent$`,
		d.spend)
}

// start starts the scenario's two servers with the auth settings auth,
// when not nil, and the rate limits rateLimit.
func (d *discoverySteps) start(ctx context.Context, auth map[string]any, rateLimit map[string]any) error {
	if auth != nil {
		d.w.Settings["auth"] = auth
	}
	for range 2 {
		if err := d.w.Start(ctx, rateLimit); err != nil {
			return err
		}
	}
	return nil
}

func (d *discoverySteps) withIssuer(ctx context.Context, issuer, jwksURI string) error {
	federation := map[string]any{"issuer": issuer}
	if jwksURI != "" {
		federation["jwks_uri"] = jwksURI
	}
	return d.start(ctx, map[string]any{"federation": federation}, nil)
}

func (d *discoverySteps) switchedOff(ctx context.Context) error {
	d.w.Settings["features"] = map[string]any{"disabled": []string{"oidc-federation"}}
	return d.start(ctx, nil, nil)
}

// withBudget starts the servers with a standard budget of burst that
// refills too slowly to matter in a scenario.
func (d *discoverySteps) withBudget(ctx context.Context, burst int) error {
	return d.start(ctx, nil, map[string]any{"standard": map[string]any{"rate": 1e-6, "burst": burst}})
}

// cacheable sends the request with no credential and checks it answers
// 200 as JSON a cache may keep for an hour.
func (d *discoverySteps) cacheable(ctx context.Context, method, path, replica string) error {
	r, err := d.w.Do(ctx, replica, "", method, path, nil)
	if err != nil && r.Status == 0 {
		return err
	}
	if r.Status != http.StatusOK || r.Header.Get("Cache-Control") != "public, max-age=3600" ||
		r.Header.Get("Content-Type") != "application/json" {
		return fmt.Errorf("%w: %s %s through %s answered %d, Cache-Control %q, Content-Type %q: %s",
			harness.ErrStep, method, path, replica, r.Status, r.Header.Get("Cache-Control"),
			r.Header.Get("Content-Type"), r.Body)
	}
	if method == http.MethodHead && len(r.Body) != 0 {
		return fmt.Errorf("%w: HEAD %s answered a body", harness.ErrStep, path)
	}
	return nil
}

// discoveryDocument is the discovery document's members.
//
//nolint:tagliatelle // the names OpenID Connect Discovery sets
type discoveryDocument struct {
	Issuer          string   `json:"issuer"`
	JWKSURI         string   `json:"jwks_uri"`
	ResponseTypes   []string `json:"response_types_supported"`
	SubjectTypes    []string `json:"subject_types_supported"`
	SigningAlgs     []string `json:"id_token_signing_alg_values_supported"`
	ClaimsSupported []string `json:"claims_supported"`
}

// document checks the last answer is the discovery document of issuer,
// naming jwksURI verbatim, the published keys' one algorithm alg and the
// federation token's claims, with no other member.
func (d *discoverySteps) document(issuer, jwksURI, alg string) error {
	body := d.w.Last().Body
	var members map[string]json.RawMessage
	var doc discoveryDocument
	if err := json.Unmarshal(body, &members); err != nil {
		return fmt.Errorf("%w: the discovery document: %w: %s", harness.ErrStep, err, body)
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("%w: the discovery document: %w: %s", harness.ErrStep, err, body)
	}
	if len(members) != 6 || doc.Issuer != issuer || doc.JWKSURI != jwksURI ||
		!slices.Equal(doc.ResponseTypes, []string{"id_token"}) ||
		!slices.Equal(doc.SubjectTypes, []string{"public"}) ||
		!slices.Equal(doc.SigningAlgs, []string{alg}) ||
		!slices.Equal(doc.ClaimsSupported, federationClaims) {
		return fmt.Errorf("%w: the discovery document is not issuer %s's with jwks_uri %s and %s: %s",
			harness.ErrStep, issuer, jwksURI, alg, body)
	}
	return nil
}

// jwks parses body as a JWKS, failing on a key with a private or
// certificate member or without use sig, alg and kid, and answers its
// kids in order.
func jwks(body []byte) ([]string, error) {
	var set struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(body, &set); err != nil {
		return nil, fmt.Errorf("%w: the JWKS: %w: %s", harness.ErrStep, err, body)
	}
	kids := make([]string, 0, len(set.Keys))
	for _, k := range set.Keys {
		for _, m := range privateMembers {
			if _, ok := k[m]; ok {
				return nil, fmt.Errorf("%w: a JWKS key carries %q: %s", harness.ErrStep, m, body)
			}
		}
		kid, _ := k["kid"].(string)
		if k["use"] != "sig" || k["alg"] == nil || k["alg"] == "" || kid == "" {
			return nil, fmt.Errorf("%w: a JWKS key lacks use sig, alg or kid: %s", harness.ErrStep, body)
		}
		kids = append(kids, kid)
	}
	return kids, nil
}

// kids are the kids of purpose's keys in state, which where narrows,
// sorted.
func (d *discoverySteps) kids(ctx context.Context, purpose, where string) ([]string, error) {
	var list string
	err := d.w.Row(ctx, `SELECT coalesce(string_agg(public_jwk->>'kid', ',' ORDER BY public_jwk->>'kid'), '')
FROM signing_keys WHERE purpose = $1 AND `+where, []any{purpose}, &list)
	if err != nil || list == "" {
		return nil, err
	}
	return strings.Split(list, ","), nil
}

// exactKeys checks the last answer holds the federation keys that are not
// retired, sorted by kid, and none of the api-token keys, of which the
// scenario's exchanges made at least one.
func (d *discoverySteps) exactKeys(ctx context.Context) error {
	got, err := jwks(d.w.Last().Body)
	if err != nil {
		return err
	}
	published, err := d.kids(ctx, "federation", "state <> 'retired'")
	if err != nil {
		return err
	}
	api, err := d.kids(ctx, "api-token", "true")
	if err != nil {
		return err
	}
	if len(got) == 0 || !slices.Equal(got, published) || len(api) == 0 {
		return fmt.Errorf("%w: the JWKS holds %v, want the published federation keys %v (api-token keys %v)",
			harness.ErrStep, got, published, api)
	}
	for _, kid := range api {
		if slices.Contains(got, kid) {
			return fmt.Errorf("%w: the JWKS holds the api-token key %s", harness.ErrStep, kid)
		}
	}
	return nil
}

// keepOnly keeps the last JWKS answer's one key's kid as name.
func (d *discoverySteps) keepOnly(name string) error {
	kids, err := jwks(d.w.Last().Body)
	if err != nil {
		return err
	}
	if len(kids) != 1 {
		return fmt.Errorf("%w: the JWKS holds %v, not one key", harness.ErrStep, kids)
	}
	d.w.IDs[name] = kids[0]
	return nil
}

// toReplica sends every request to the replica's API listener, keeping
// its path and query, as a relying party reaches the issuer's host
// through the deployment's ingress.
type toReplica struct{ addr string }

func (t toReplica) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host, r.Host = "http", t.addr, ""
	return http.DefaultTransport.RoundTrip(r) //nolint:wrapcheck // the transport's own error
}

// relyingParty is ctx carrying go-oidc's HTTP client, which reaches
// replica whatever host a URL names.
func (d *discoverySteps) relyingParty(ctx context.Context, replica string) context.Context {
	base, _ := url.Parse(d.w.Base(replica))
	return oidc.ClientContext(ctx, &http.Client{Transport: toReplica{addr: base.Host}, Timeout: 30 * time.Second})
}

// discover runs go-oidc's discovery of issuer through replica, which
// fetches the discovery document from the issuer's path and refuses one
// whose issuer differs.
func (d *discoverySteps) discover(ctx context.Context, issuer, replica string) error {
	p, err := oidc.NewProvider(d.relyingParty(ctx, replica), issuer)
	if err != nil {
		return fmt.Errorf("%w: go-oidc refuses the issuer %s: %w", harness.ErrStep, issuer, err)
	}
	d.provider, d.issuer = p, issuer
	return nil
}

// verify signs a federation token's claims with the private half of the
// active key of purpose, opened from signing_keys under the scenario's
// keyring as the servers open it, and checks go-oidc's verifier of the
// discovered issuer accepts it or refuses it. The verifier accepts ES256
// beside the document's algorithms, so an api-token key's token fails on
// its key, not its algorithm.
func (d *discoverySteps) verify(ctx context.Context, purpose, outcome string) error {
	if d.provider == nil {
		return fmt.Errorf("%w: no issuer discovered", harness.ErrStep)
	}
	raw, err := d.signWithActive(ctx, purpose)
	if err != nil {
		return err
	}
	verifier := d.provider.Verifier(&oidc.Config{
		ClientID:             "sts.example.com",
		SupportedSigningAlgs: []string{"RS256", "PS256", "ES256"},
	})
	_, err = verifier.Verify(ctx, raw)
	switch {
	case outcome == "verifies" && err != nil:
		return fmt.Errorf("%w: a token of the active %s key fails: %w", harness.ErrStep, purpose, err)
	case outcome == "fails" && err == nil:
		return fmt.Errorf("%w: a token of the active %s key verifies against the federation JWKS",
			harness.ErrStep, purpose)
	}
	return nil
}

// signWithActive signs a federation token's claims with the active key of
// purpose under its kid and algorithm.
func (d *discoverySteps) signWithActive(ctx context.Context, purpose string) (string, error) {
	var (
		id, kid, alg string
		sealed       []byte
	)
	if err := d.w.Row(ctx, `SELECT id::text, public_jwk->>'kid', algorithm, private_key_sealed
FROM signing_keys WHERE purpose = $1 AND state = 'active'`, []any{purpose}, &id, &kid, &alg, &sealed); err != nil {
		return "", err
	}
	keys, pool, err := d.w.Keyring(ctx)
	if err != nil {
		return "", err
	}
	defer pool.Close()
	der, err := keys.OpenColumn(ctx, crypt.Column{Table: "signing_keys", Name: "private_key_sealed", Key: []string{"id"}},
		[]string{id}, sealed)
	if err != nil {
		return "", fmt.Errorf("open the %s key %s: %w", purpose, kid, err)
	}
	private, err := x509.ParsePKCS8PrivateKey(der.Reveal())
	if err != nil {
		return "", fmt.Errorf("parse the %s key %s: %w", purpose, kid, err)
	}
	sg, err := jose.NewSigner(jose.SigningKey{
		Algorithm: jose.SignatureAlgorithm(alg),
		Key:       jose.JSONWebKey{Key: private, KeyID: kid},
	}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		return "", fmt.Errorf("sign with the %s key %s: %w", purpose, kid, err)
	}
	now := time.Now()
	raw, err := jwt.Signed(sg).Claims(map[string]any{
		"iss": d.issuer, "sub": "space:legacy:stack:infra:run_type:TRACKED:scope:write",
		"aud": "sts.example.com", "exp": now.Add(time.Hour).Unix(), "iat": now.Unix(), "nbf": now.Unix(),
		"jti": kernel.NewULID().String(), "spaceId": "legacy", "spacePath": "legacy",
		"callerType": "stack", "callerId": "infra", "runType": "TRACKED",
		"runId": kernel.NewULID().String(), "scope": "write",
	}).Serialize()
	if err != nil {
		return "", fmt.Errorf("sign with the %s key %s: %w", purpose, kid, err)
	}
	return raw, nil
}

// rotateFederation applies set to the federation key in state, makes
// authn.keys.rotate due, and waits until one federation key matches want.
func (d *discoverySteps) rotateFederation(ctx context.Context, set, state, want string) error {
	if _, err := d.w.Query(ctx, `UPDATE signing_keys SET `+set+
		` WHERE purpose = 'federation' AND state = `+harness.Literal(state)+`;
UPDATE periodic_tasks SET next_run_at = now() WHERE name = 'authn.keys.rotate'`); err != nil {
		return err
	}
	//nolint:wrapcheck // names what it waited for
	return deps.Eventually(ctx, "a federation key "+want, func(ctx context.Context) (bool, error) {
		n, err := d.w.Number(ctx, `SELECT count(*) FROM signing_keys WHERE purpose = 'federation' AND `+want)
		return n == 1, err
	})
}

// publishNext ages the active key to its successor's creation and keeps
// the next key's kid as name.
func (d *discoverySteps) publishNext(ctx context.Context, name string) error {
	if err := d.rotateFederation(ctx,
		`created_at = created_at - interval '90 days', activated_at = activated_at - interval '90 days'`,
		"active", "state = 'next'"); err != nil {
		return err
	}
	next, err := d.kids(ctx, "federation", "state = 'next'")
	if err != nil {
		return err
	}
	d.w.IDs[name] = next[0]
	return nil
}

// promote ages the next key past its 48 hours of publication, so the
// rotation promotes it and the active key retires.
func (d *discoverySteps) promote(ctx context.Context) error {
	return d.rotateFederation(ctx, `created_at = created_at - interval '48 hours'`, "next", "state = 'retiring'")
}

// retire moves the retiring key's last token's expiry, plus the grace a
// relying party's cache has, into the past, so the rotation retires it.
func (d *discoverySteps) retire(ctx context.Context) error {
	return d.rotateFederation(ctx, `retire_after = now() - interval '25 hours'`, "retiring", "state = 'retired'")
}

// eventuallyHolds waits until the replica's JWKS holds exactly the keys
// kept under the comma-separated names, as it does once its cache of the
// published keys rereads them.
func (d *discoverySteps) eventuallyHolds(ctx context.Context, replica, names string) error {
	want := []string{}
	for _, n := range d.w.SlugList(names) {
		kid, ok := d.w.IDs[n]
		if !ok {
			return fmt.Errorf("%w: no key kept as %q", harness.ErrStep, n)
		}
		want = append(want, kid)
	}
	slices.Sort(want)
	var got []string
	//nolint:wrapcheck // names what it waited for
	return deps.Eventually(ctx, "the JWKS through "+replica+" to hold "+strings.Join(want, ", "),
		func(ctx context.Context) (bool, error) {
			r, err := d.w.Do(ctx, replica, "", http.MethodGet, "/.well-known/jwks", nil)
			if err != nil {
				return false, err
			}
			if got, err = jwks(r.Body); err != nil {
				return false, err
			}
			return slices.Equal(got, want), nil
		})
}

// inState checks the kept key's state in signing_keys.
func (d *discoverySteps) inState(ctx context.Context, name, state string) error {
	kids, err := d.kids(ctx, "federation", "state = "+harness.Literal(state))
	if err != nil {
		return err
	}
	if !slices.Contains(kids, d.w.IDs[name]) {
		return fmt.Errorf("%w: the federation key %s is not %s", harness.ErrStep, name, state)
	}
	return nil
}

// spend reads the discovery document and the JWKS with no credential, by
// turns through A and B, until an answer is refused, which must come
// once the client's address has drawn its standard budget of burst. Every
// answer counts against the standard budget, which the replicas share,
// so each says so in X-RateLimit-Limit.
func (d *discoverySteps) spend(ctx context.Context, burst int) error {
	paths := []string{"/.well-known/openid-configuration", "/.well-known/jwks"}
	for i := range burst + 1 {
		replica := []string{"A", "B"}[i%2]
		r, err := d.w.Do(ctx, replica, "", http.MethodGet, paths[i%2], nil)
		if r.Status == 0 {
			return err
		}
		if got := r.Header.Get("X-RateLimit-Limit"); got != strconv.Itoa(burst) {
			return fmt.Errorf("%w: answer %d drew on a budget of %q, want the standard budget of %d",
				harness.ErrStep, i+1, got, burst)
		}
		switch r.Status {
		case http.StatusTooManyRequests:
			return nil
		case http.StatusOK:
		default:
			return fmt.Errorf("%w: answer %d is %d: %s", harness.ErrStep, i+1, r.Status, r.Body)
		}
	}
	return fmt.Errorf("%w: %d reads were answered on a standard budget of %d", harness.ErrStep, burst+1, burst)
}
