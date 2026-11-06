package oidc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
)

// The limits of decision 0006 on fetching an issuer's documents.
const (
	// Timeout bounds each fetch of a discovery document or JWKS.
	Timeout = 5 * time.Second
	// MaxBody is the largest document read; a larger one is refused.
	MaxBody = 1 << 20
	// CacheFor is how long a JWKS answers before it is fetched again.
	CacheFor = time.Hour
	// RefetchEvery is the shortest interval between two fetches of one
	// issuer's JWKS for a kid it lacks, so tokens naming made-up kids
	// cannot make the server fetch on every request.
	RefetchEvery = time.Minute
	// Leeway is the clock skew exp, nbf and iat are allowed.
	Leeway = 60 * time.Second
)

// ErrToken reports a token that fails verification; the error names the
// check.
var ErrToken = errors.New("OIDC token refused")

// ErrIssuer reports an issuer whose documents cannot be read or are not
// what OpenID Connect Discovery says they must be.
var ErrIssuer = errors.New("OIDC issuer unusable")

// algorithms are the asymmetric algorithms a token may be signed with.
// none and the HMAC algorithms are absent, so a token signed with a
// public key as an HMAC secret is refused before any key is tried.
var algorithms = []jose.SignatureAlgorithm{
	jose.RS256, jose.RS384, jose.RS512,
	jose.PS256, jose.PS384, jose.PS512,
	jose.ES256, jose.ES384, jose.ES512,
	jose.EdDSA,
}

// Verifier is port.OIDC over HTTPS, caching each issuer's JWKS.
type Verifier struct {
	client *http.Client
	now    func() time.Time

	mu      sync.Mutex
	issuers map[string]*issuer
}

var _ port.OIDC = (*Verifier)(nil)

// issuer is one issuer's cached JWKS. mu serializes its fetches, so
// concurrent exchanges with an unknown kid fetch once.
type issuer struct {
	mu      sync.Mutex
	keys    jose.JSONWebKeySet
	fetched time.Time
}

// Options are what a deployment or a test changes.
type Options struct {
	// RootCAs are PEM certificates trusted beside the system's roots, such
	// as auth.oidc_api_keys.ca_file's for an issuer under a private CA.
	RootCAs []byte
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// New returns a verifier that fetches over HTTPS only, trusting the
// system's roots and o.RootCAs, and never follows a redirect off HTTPS.
func New(o Options) (*Verifier, error) {
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("oidc: system roots: %w", err)
	}
	if len(o.RootCAs) > 0 && !roots.AppendCertsFromPEM(o.RootCAs) {
		return nil, fmt.Errorf("%w: the extra roots hold no PEM certificate", ErrIssuer)
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	transport := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // the standard library's
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	return &Verifier{
		client: &http.Client{
			Timeout:   Timeout,
			Transport: transport,
			CheckRedirect: func(r *http.Request, _ []*http.Request) error {
				if r.URL.Scheme != "https" {
					return fmt.Errorf("%w: redirected to %s", ErrIssuer, r.URL.Redacted())
				}
				return nil
			},
		},
		now:     now,
		issuers: map[string]*issuer{},
	}, nil
}

// Verify implements port.OIDC.
func (v *Verifier) Verify(ctx context.Context, iss, audience, raw string) (map[string]any, error) {
	tok, err := jwt.ParseSigned(raw, algorithms)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrToken, err)
	}
	if len(tok.Headers) != 1 {
		return nil, fmt.Errorf("%w: %d signatures", ErrToken, len(tok.Headers))
	}
	kid := tok.Headers[0].KeyID
	keys, err := v.keys(ctx, iss, kid)
	if err != nil {
		return nil, err
	}
	var (
		std    jwt.Claims
		claims map[string]any
	)
	verified := false
	for _, k := range keys {
		if err := tok.Claims(k, &std, &claims); err == nil {
			verified = true
			break
		}
	}
	if !verified {
		return nil, fmt.Errorf("%w: no key of %s verifies the signature", ErrToken, iss)
	}
	if std.Expiry == nil {
		return nil, fmt.Errorf("%w: no exp", ErrToken)
	}
	if err := std.ValidateWithLeeway(jwt.Expected{
		Issuer:      iss,
		AnyAudience: jwt.Audience{audience},
		Time:        v.now(),
	}, Leeway); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrToken, err)
	}
	return claims, nil
}

// keys are the issuer's keys that kid names, or all of them for no kid.
// The JWKS is fetched when the cache is empty or older than CacheFor, or
// when kid is unknown and the last fetch is RefetchEvery old.
func (v *Verifier) keys(ctx context.Context, iss, kid string) ([]jose.JSONWebKey, error) {
	v.mu.Lock()
	is, ok := v.issuers[iss]
	if !ok {
		is = &issuer{}
		v.issuers[iss] = is
	}
	v.mu.Unlock()

	is.mu.Lock()
	defer is.mu.Unlock()
	now := v.now()
	find := func() []jose.JSONWebKey {
		if kid == "" {
			return is.keys.Keys
		}
		return is.keys.Key(kid)
	}
	stale := is.fetched.IsZero() || now.Sub(is.fetched) >= CacheFor
	if !stale && len(find()) == 0 && now.Sub(is.fetched) >= RefetchEvery {
		stale = true
	}
	if stale {
		// A failed fetch counts too, so an unreachable issuer is not
		// asked again on every request.
		is.fetched = now
		set, err := v.fetch(ctx, iss)
		if err != nil {
			return nil, err
		}
		is.keys = set
	}
	if keys := find(); len(keys) > 0 {
		return keys, nil
	}
	return nil, fmt.Errorf("%w: %s publishes no key %q", ErrToken, iss, kid)
}

// fetch reads iss's discovery document and the JWKS it names, both over
// HTTPS. The document's issuer must be iss exactly (OpenID Connect
// Discovery, section 4.3).
func (v *Verifier) fetch(ctx context.Context, iss string) (jose.JSONWebKeySet, error) {
	var disc struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"` //nolint:tagliatelle // fixed by OpenID Connect Discovery
	}
	if err := v.get(
		ctx,
		strings.TrimSuffix(iss, "/")+"/.well-known/openid-configuration",
		&disc,
	); err != nil {
		return jose.JSONWebKeySet{}, err
	}
	if disc.Issuer != iss {
		return jose.JSONWebKeySet{}, fmt.Errorf("%w: %s's discovery document names issuer %q",
			ErrIssuer, iss, disc.Issuer)
	}
	var set jose.JSONWebKeySet
	if err := v.get(ctx, disc.JWKSURI, &set); err != nil {
		return jose.JSONWebKeySet{}, err
	}
	return set, nil
}

// get decodes the JSON document at raw, an HTTPS URL, of at most MaxBody
// bytes.
func (v *Verifier) get(ctx context.Context, raw string, out any) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("%w: %q is not an HTTPS URL", ErrIssuer, raw)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrIssuer, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrIssuer, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: GET %s answered %d", ErrIssuer, u.Redacted(), resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
	if err != nil {
		return fmt.Errorf("%w: GET %s: %w", ErrIssuer, u.Redacted(), err)
	}
	if len(b) > MaxBody {
		return fmt.Errorf("%w: GET %s is over %d bytes", ErrIssuer, u.Redacted(), MaxBody)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("%w: GET %s: %w", ErrIssuer, u.Redacted(), err)
	}
	return nil
}
