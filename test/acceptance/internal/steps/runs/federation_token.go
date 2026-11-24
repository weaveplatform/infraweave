package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// federationSteps read the OIDC federation tokens runs receive from their
// logs, where a hook prints each with its dots replaced by commas, so
// the log's masker, which hides the token itself, leaves it readable, and
// verify them as a relying party does, through go-oidc's discovery of the
// served issuer.
//
// ref: decision 0006 § "OIDC federation token"
type federationSteps struct {
	w *harness.World
	// tokens are the raw tokens the scenario kept, by name, and jtis the
	// jti of each token verified so far.
	tokens map[string]string
	jtis   map[string]string
}

// federationLine starts the log line a hook prints the token on.
const federationLine = "oidc token: "

func (f *federationSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^token "(\w+)" reads the run "(\w+)"'s ([A-Z_]+) log through ([AB]), whose federation token is kept as "(\w+)"$`,
		f.keep)
	sc.Step(`^the federation token "(\w+)" verifies against the issuer "([^"]+)" discovered through ([AB]), with the claims:$`,
		f.verify)
	sc.Step(`^no object in the scenario's bucket holds the federation token "(\w+)"$`, f.unstored)
	sc.Step(`^token "(\w+)" gives the home organization a copy of the tier "([^"]+)" without "([a-z-]+)", with billing on, through ([AB])$`,
		f.tierWithout)
}

// keep reads the token a hook printed in the run's log of state, which
// must not hold the token itself.
func (f *federationSteps) keep(ctx context.Context, tok, run, state, replica, name string) error {
	text, err := logText(ctx, f.w, tok, run, state, replica)
	if err != nil {
		return err
	}
	var printed []string
	for line := range strings.SplitSeq(text, "\n") {
		if rest, ok := strings.CutPrefix(line, federationLine); ok {
			printed = append(printed, rest)
		}
	}
	if len(printed) != 1 {
		return fmt.Errorf("%w: the %s log prints %d federation tokens, want 1", harness.ErrStep, state, len(printed))
	}
	raw := strings.ReplaceAll(printed[0], ",", ".")
	if strings.Count(raw, ".") != 2 {
		return fmt.Errorf("%w: the %s log prints no compact JWS", harness.ErrStep, state)
	}
	if strings.Contains(text, raw) {
		return fmt.Errorf("%w: the %s log holds the federation token unmasked", harness.ErrStep, state)
	}
	f.tokens[name] = raw
	return nil
}

// toIssuer sends every request to a replica's API listener, keeping its
// path and query, as a relying party reaches the issuer's host through
// the deployment's ingress.
type toIssuer struct{ addr string }

func (t toIssuer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host, r.Host = "http", t.addr, ""
	return http.DefaultTransport.RoundTrip(r) //nolint:wrapcheck // the transport's own error
}

// verify discovers issuer through replica with go-oidc, verifies the kept
// token's signature against the served JWKS and its issuer and expiry,
// and checks its claims are exactly the discovery document's
// claims_supported, with the table's values, each JSON with the
// scenario's IDs resolved, and a fresh ULID jti, a lifetime of an hour
// and nbf equal to iat.
func (f *federationSteps) verify(ctx context.Context, name, issuer, replica string, table *godog.Table) error {
	raw, ok := f.tokens[name]
	if !ok {
		return fmt.Errorf("%w: no federation token kept as %q", harness.ErrStep, name)
	}
	base, err := url.Parse(f.w.Base(replica))
	if err != nil {
		return fmt.Errorf("%w: %w", harness.ErrStep, err)
	}
	ctx = oidc.ClientContext(ctx, &http.Client{Transport: toIssuer{addr: base.Host}, Timeout: 30 * time.Second})
	p, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return fmt.Errorf("%w: go-oidc refuses the issuer %s: %w", harness.ErrStep, issuer, err)
	}
	var doc struct {
		Claims []string `json:"claims_supported"` //nolint:tagliatelle // the name OpenID Connect Discovery sets
	}
	if err := p.Claims(&doc); err != nil {
		return fmt.Errorf("%w: the discovery document: %w", harness.ErrStep, err)
	}
	// The audience is checked with the other claims below, as the table
	// names it.
	idt, err := p.Verifier(&oidc.Config{SkipClientIDCheck: true}).Verify(ctx, raw)
	if err != nil {
		return fmt.Errorf("%w: the federation token %q fails verification: %w", harness.ErrStep, name, err)
	}
	var claims map[string]json.RawMessage
	if err := idt.Claims(&claims); err != nil {
		return fmt.Errorf("%w: the claims: %w", harness.ErrStep, err)
	}
	if got := slices.Sorted(func(yield func(string) bool) {
		for k := range claims {
			if !yield(k) {
				return
			}
		}
	}); !slices.Equal(got, slices.Sorted(slices.Values(doc.Claims))) {
		return fmt.Errorf("%w: the token's claims are %v, want the document's %v", harness.ErrStep, got, doc.Claims)
	}
	for _, row := range table.Rows {
		if len(row.Cells) != 2 {
			return fmt.Errorf("%w: a claim row has %d cells, want 2", harness.ErrStep, len(row.Cells))
		}
		claim, want := row.Cells[0].Value, f.w.Resolve(row.Cells[1].Value)
		var got, wanted any
		if err := json.Unmarshal(claims[claim], &got); err != nil {
			return fmt.Errorf("%w: the claim %s: %w", harness.ErrStep, claim, err)
		}
		if err := json.Unmarshal([]byte(want), &wanted); err != nil {
			return fmt.Errorf("%w: the expected %s %q: %w", harness.ErrStep, claim, want, err)
		}
		if !reflect.DeepEqual(got, wanted) {
			return fmt.Errorf("%w: the claim %s is %s, want %s", harness.ErrStep, claim, claims[claim], want)
		}
	}
	return f.times(name, claims)
}

// times checks the token's jti is a ULID no other token of the scenario
// carries, and that it lives an hour from an iat within the last ten
// minutes, nbf equal to iat.
func (f *federationSteps) times(name string, claims map[string]json.RawMessage) error {
	var (
		jti           string
		iat, nbf, exp int64
	)
	for claim, into := range map[string]any{"jti": &jti, "iat": &iat, "nbf": &nbf, "exp": &exp} {
		if err := json.Unmarshal(claims[claim], into); err != nil {
			return fmt.Errorf("%w: the claim %s: %w", harness.ErrStep, claim, err)
		}
	}
	if _, err := kernel.ParseULID(jti); err != nil {
		return fmt.Errorf("%w: the jti %q is no ULID: %w", harness.ErrStep, jti, err)
	}
	for other, seen := range f.jtis {
		if seen == jti && other != name {
			return fmt.Errorf("%w: the federation tokens %q and %q share the jti", harness.ErrStep, other, name)
		}
	}
	f.jtis[name] = jti
	issued := time.Unix(iat, 0)
	if nbf != iat || exp-iat != int64(time.Hour/time.Second) || time.Since(issued) > 10*time.Minute ||
		time.Until(issued) > time.Minute {
		return fmt.Errorf("%w: iat %d, nbf %d, exp %d", harness.ErrStep, iat, nbf, exp)
	}
	return nil
}

// unstored checks that no object of the scenario's bucket holds the kept
// token: the logs, artifacts and saved workspaces the store holds are
// sealed, and the log masks the token before it is sealed.
func (f *federationSteps) unstored(ctx context.Context, name string) error {
	raw, ok := f.tokens[name]
	if !ok {
		return fmt.Errorf("%w: no federation token kept as %q", harness.ErrStep, name)
	}
	store, err := f.w.Suite.Deps.ObjectStore(ctx)
	if err != nil {
		return err //nolint:wrapcheck // names @objectstore
	}
	keys, read, err := store.Containing(ctx, f.w.Bucket, []byte(raw))
	switch {
	case err != nil:
		return err //nolint:wrapcheck // names the bucket
	case len(keys) > 0:
		return fmt.Errorf("%w: the objects %q hold the federation token %q", harness.ErrStep, keys, name)
	case read == 0:
		return fmt.Errorf("%w: the bucket holds no object", harness.ErrStep)
	}
	return nil
}

// tierWithout creates a tier holding what tier holds but feature, gives
// it to the home organization and turns billing on, so the organization
// keeps every other feature and limit the scenario uses.
func (f *federationSteps) tierWithout(ctx context.Context, tok, tier, feature, replica string) error {
	r, err := f.w.Do(ctx, replica, tok, http.MethodGet, "/operator/v1/tiers/"+url.PathEscape(tier), nil)
	if err != nil {
		return err //nolint:wrapcheck // names the request
	}
	var held struct {
		Features []string         `json:"features"`
		Limits   map[string]int64 `json:"limits"`
	}
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &held) != nil {
		return fmt.Errorf("%w: the tier %s answered %d %s", harness.ErrStep, tier, r.Status, r.Body)
	}
	if !slices.Contains(held.Features, feature) {
		return fmt.Errorf("%w: the tier %s lacks %s already", harness.ErrStep, tier, feature)
	}
	name := tier + " without " + feature
	for _, req := range []struct {
		method, path string
		body         any
		want         int
	}{
		{http.MethodPost, "/operator/v1/tiers", map[string]any{
			"name": name, "features": slices.DeleteFunc(held.Features, func(s string) bool { return s == feature }),
			"limits": held.Limits,
		}, http.StatusCreated},
		{
			http.MethodPut, "/operator/v1/organizations/" + harness.Home.OrgSlug + "/tier",
			map[string]string{"tier": name},
			http.StatusOK,
		},
		{http.MethodPut, "/operator/v1/billing", map[string]bool{"enabled": true}, http.StatusOK},
	} {
		r, err := f.w.Do(ctx, replica, tok, req.method, req.path, req.body)
		if err != nil {
			return err //nolint:wrapcheck // names the request
		}
		if r.Status != req.want {
			return fmt.Errorf("%w: %s %s answered %d, want %d: %s", harness.ErrStep, req.method, req.path,
				r.Status, req.want, r.Body)
		}
	}
	return nil
}
