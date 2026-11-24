package federation

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	jose "github.com/go-jose/go-jose/v4"
)

// Claims are the claims of every OIDC federation token, in the order the
// reference lists them, which the discovery document lists as
// claims_supported.
//
// ref: decision 0006 § "OIDC federation token"
// ref: report 12 section 6.3
var Claims = []string{
	"iss", "sub", "aud", "exp", "iat", "jti", "nbf",
	"spaceId", "spacePath", "callerType", "callerId", "runType", "runId", "scope",
}

// jwksPath is where the issuer publishes its keys, below the issuer, and
// the default of auth.federation.jwks_uri; accessapi.FederationJWKSPattern
// is the same path.
const jwksPath = "/.well-known/jwks"

// ErrUnpublishable reports a key with no public half to publish.
var ErrUnpublishable = errors.New("federation key has no public half")

// Document is the federation issuer's discovery document, OpenID Connect
// Discovery's provider metadata limited to what a relying party of a
// federation token reads: the issuer, where its keys are, that it issues
// ID tokens with a public subject, the algorithms its keys sign and the
// token's claims.
//
// ref: OpenID Connect Discovery 1.0, section 3
//
//nolint:tagliatelle // the names OpenID Connect Discovery sets
type Document struct {
	Issuer                           string   `json:"issuer"`
	JWKSURI                          string   `json:"jwks_uri"`
	ResponseTypesSupported           []string `json:"response_types_supported"`
	SubjectTypesSupported            []string `json:"subject_types_supported"`
	IDTokenSigningAlgValuesSupported []string `json:"id_token_signing_alg_values_supported"`
	ClaimsSupported                  []string `json:"claims_supported"`
}

// NewDocument is issuer's discovery document: jwksURI as its jwks_uri,
// or JWKSURI's default when it is empty, and the algorithms of keys, the
// published federation keys, once each and sorted. During a change of
// auth.federation.algorithm the published keys sign two algorithms, and
// the document names both, so a relying party accepts the next key's
// tokens before it signs any.
func NewDocument(issuer, jwksURI string, keys []jose.JSONWebKey) Document {
	algs := make([]string, 0, len(keys))
	for _, k := range keys {
		if !slices.Contains(algs, k.Algorithm) {
			algs = append(algs, k.Algorithm)
		}
	}
	slices.Sort(algs)
	return Document{
		Issuer:                           issuer,
		JWKSURI:                          JWKSURI(issuer, jwksURI),
		ResponseTypesSupported:           []string{"id_token"},
		SubjectTypesSupported:            []string{"public"},
		IDTokenSigningAlgValuesSupported: algs,
		ClaimsSupported:                  slices.Clone(Claims),
	}
}

// JWKSURI is override, auth.federation.jwks_uri, verbatim, or when it is
// empty the issuer's own JWKS route: the issuer without a trailing slash
// and then /.well-known/jwks, where the issuer's routes serve it.
func JWKSURI(issuer, override string) string {
	if override != "" {
		return override
	}
	return strings.TrimSuffix(issuer, "/") + jwksPath
}

// IssuerPath is the path of issuer without its trailing slash, below
// which OIDC discovery looks for the discovery document: empty for an
// issuer at its host's root or one that does not parse.
//
// ref: OpenID Connect Discovery 1.0, section 4
func IssuerPath(issuer string) string {
	u, err := url.Parse(issuer)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(u.Path, "/")
}

// KeySet is the JWKS of keys, the published federation keys: each one's
// public members alone, with its kid, use sig and its alg, sorted by kid.
// It never carries a certificate (x5c, x5t, x5u) or a private member, so
// no relying party pins a certificate the next key lacks. A key with no
// public half is ErrUnpublishable.
func KeySet(keys []jose.JSONWebKey) (jose.JSONWebKeySet, error) {
	out := make([]jose.JSONWebKey, 0, len(keys))
	for _, k := range keys {
		pub := k.Public()
		if !pub.Valid() {
			return jose.JSONWebKeySet{}, fmt.Errorf("%w: %s", ErrUnpublishable, k.KeyID)
		}
		out = append(out, jose.JSONWebKey{
			Key:       pub.Key,
			KeyID:     k.KeyID,
			Algorithm: k.Algorithm,
			Use:       "sig",
		})
	}
	slices.SortFunc(out, func(a, b jose.JSONWebKey) int { return strings.Compare(a.KeyID, b.KeyID) })
	return jose.JSONWebKeySet{Keys: out}, nil
}
