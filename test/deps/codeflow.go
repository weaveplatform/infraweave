package deps

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
)

// CodeFlow is a relying party's side of the authorization code flow with
// PKCE and a nonce, which proves a test issuer on its own: it builds the
// authorization request and exchanges the code its callback carries.
type CodeFlow struct {
	Issuer, ClientID, Secret, RedirectURI string
	// Client reaches the issuer, trusting its certificate.
	Client *http.Client
	// Extra are further authorization request parameters, such as
	// max_age and prompt.
	Extra url.Values

	// State, Nonce and Verifier are the request's, which AuthorizeURL
	// chooses.
	State, Nonce, Verifier string

	disc discovery
}

// discovery is the part of an OpenID Provider's discovery document a
// relying party reads.
type discovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"` //nolint:tagliatelle // fixed by OpenID Connect Discovery
	TokenEndpoint         string `json:"token_endpoint"`         //nolint:tagliatelle // fixed by OpenID Connect Discovery
	EndSessionEndpoint    string `json:"end_session_endpoint"`   //nolint:tagliatelle // fixed by OpenID Connect Discovery
	JWKSURI               string `json:"jwks_uri"`               //nolint:tagliatelle // fixed by OpenID Connect Discovery
}

// TokenAnswer is the token endpoint's answer to an exchange.
type TokenAnswer struct {
	Status int
	// IDToken is the answer's id_token, and Error its error code.
	IDToken, Error string
}

// AuthorizeURL reads the issuer's discovery document and answers the
// authorization request for a fresh state, nonce and S256 PKCE verifier.
func (f *CodeFlow) AuthorizeURL(ctx context.Context) (string, error) {
	if err := f.getJSON(ctx, strings.TrimSuffix(f.Issuer, "/")+"/.well-known/openid-configuration", &f.disc); err != nil {
		return "", err
	}
	if f.disc.Issuer != f.Issuer {
		return "", fmt.Errorf("%w: %s's discovery document names %q", ErrIssuer, f.Issuer, f.disc.Issuer)
	}
	f.State, f.Nonce, f.Verifier = randomToken(), randomToken(), randomToken()
	sum := sha256.Sum256([]byte(f.Verifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {f.ClientID},
		"redirect_uri":          {f.RedirectURI},
		"scope":                 {"openid profile email"},
		"state":                 {f.State},
		"nonce":                 {f.Nonce},
		"code_challenge":        {b64(sum[:])},
		"code_challenge_method": {"S256"},
	}
	for k, v := range f.Extra {
		q[k] = v
	}
	return f.disc.AuthorizationEndpoint + "?" + q.Encode(), nil
}

// Discovery answers the end-session endpoint and JWKS URI the issuer's
// discovery document named, once AuthorizeURL has read it.
func (f *CodeFlow) Discovery() (endSession, jwks string) {
	return f.disc.EndSessionEndpoint, f.disc.JWKSURI
}

// Exchange checks callback's state and exchanges its code with the PKCE
// verifier, authenticating with the client secret as HTTP Basic.
func (f *CodeFlow) Exchange(ctx context.Context, callback *url.URL) (TokenAnswer, error) {
	return f.ExchangeWith(ctx, callback, f.Verifier)
}

// ExchangeWith is Exchange with another verifier, to show the issuer
// refuses one that is not the request's.
func (f *CodeFlow) ExchangeWith(ctx context.Context, callback *url.URL, verifier string) (TokenAnswer, error) {
	q := callback.Query()
	if q.Get("state") != f.State {
		return TokenAnswer{}, fmt.Errorf("%w: the callback's state is %q, not the request's", ErrIssuer, q.Get("state"))
	}
	if e := q.Get("error"); e != "" {
		return TokenAnswer{Error: e}, nil
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {q.Get("code")},
		"redirect_uri":  {f.RedirectURI},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.disc.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return TokenAnswer{}, fmt.Errorf("%w: %w", ErrIssuer, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(url.QueryEscape(f.ClientID), url.QueryEscape(f.Secret))
	resp, err := f.Client.Do(req)
	if err != nil {
		return TokenAnswer{}, fmt.Errorf("%w: token endpoint: %w", ErrIssuer, err)
	}
	defer resp.Body.Close()
	var body struct {
		IDToken string `json:"id_token"` //nolint:tagliatelle // fixed by OpenID Connect Core
		Error   string `json:"error"`
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return TokenAnswer{}, fmt.Errorf("%w: token endpoint: %w", ErrIssuer, err)
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return TokenAnswer{}, fmt.Errorf("%w: token endpoint answered %d: %.200s", ErrIssuer, resp.StatusCode, raw)
	}
	return TokenAnswer{Status: resp.StatusCode, IDToken: body.IDToken, Error: body.Error}, nil
}

// Verify checks token's RS256 signature against the issuer's JWKS and
// answers its header and claims. It checks nothing else, so a scenario can
// read what a faulty token carries.
func (f *CodeFlow) Verify(ctx context.Context, token string) (header, claims map[string]any, err error) {
	header, claims, err = Decode(token)
	if err != nil {
		return nil, nil, err
	}
	var set struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := f.getJSON(ctx, f.disc.JWKSURI, &set); err != nil {
		return nil, nil, err
	}
	parts := strings.Split(token, ".")
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || header["alg"] != "RS256" {
		return header, claims, fmt.Errorf("%w: the token is not RS256", ErrIssuer)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	for _, k := range set.Keys {
		if k.Kty != "RSA" || k.Kid != header["kid"] {
			continue
		}
		n, errN := base64.RawURLEncoding.DecodeString(k.N)
		e, errE := base64.RawURLEncoding.DecodeString(k.E)
		if errN != nil || errE != nil {
			continue
		}
		pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		if rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig) == nil {
			return header, claims, nil
		}
	}
	return header, claims, fmt.Errorf("%w: no key of the JWKS verifies the token", ErrIssuer)
}

// Decode answers a JWS's header and claims without checking its signature.
func Decode(token string) (header, claims map[string]any, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, nil, fmt.Errorf("%w: a JWS has three parts, not %d", ErrIssuer, len(parts))
	}
	for k, into := range []*map[string]any{&header, &claims} {
		raw, err := base64.RawURLEncoding.DecodeString(parts[k])
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %w", ErrIssuer, err)
		}
		if err := json.Unmarshal(raw, into); err != nil {
			return nil, nil, fmt.Errorf("%w: %w", ErrIssuer, err)
		}
	}
	return header, claims, nil
}

func (f *CodeFlow) getJSON(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrIssuer, err)
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: GET %s: %w", ErrIssuer, u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: GET %s answered %d", ErrIssuer, u, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return fmt.Errorf("%w: GET %s: %w", ErrIssuer, u, err)
	}
	return nil
}
