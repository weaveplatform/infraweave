package http

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	jose "github.com/go-jose/go-jose/v4"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/federation"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// federationCache is the Cache-Control of both documents. A next key is
// published for 48 hours before it signs, far longer than a relying
// party's copy lives, so no cache misses the key of a token it is shown.
//
// ref: decision 0006 § "OIDC federation token"
const federationCache = "public, max-age=3600"

// FederationKeys are the published federation keys, next, active and
// retiring; token.FederationService is one.
type FederationKeys interface {
	FederationKeys(ctx context.Context) ([]jose.JSONWebKey, error)
}

// FederationDiscovery serves the federation issuer's discovery document
// and JWKS beside the APIs. Both are the installation's, one issuer for
// every organization, so neither takes a credential or names an
// organization, and every caller is answered the same cacheable document.
//
// ref: decision 0006 § "OIDC federation token"
type FederationDiscovery struct {
	keys    FederationKeys
	issuer  string
	jwksURI string
}

// NewFederationDiscovery returns the routes of issuer, whose discovery
// document names jwksURI, empty for the issuer's own JWKS route, over
// keys.
func NewFederationDiscovery(keys FederationKeys, issuer, jwksURI string) FederationDiscovery {
	return FederationDiscovery{keys: keys, issuer: issuer, jwksURI: jwksURI}
}

// FederationDiscoveryPatterns are the routes of issuer's discovery
// document and JWKS: accessapi's federation patterns and, for an issuer
// with a path, both again under that path, since OIDC discovery appends
// the document's path to the issuer. An issuer on another host is the
// operator's proxy's to route.
func FederationDiscoveryPatterns(issuer string) []string {
	patterns := []string{accessapi.FederationDiscoveryPattern, accessapi.FederationJWKSPattern}
	if prefix := federation.IssuerPath(issuer); prefix != "" {
		patterns = append(patterns,
			prefix+accessapi.FederationDiscoveryPattern, prefix+accessapi.FederationJWKSPattern)
	}
	return patterns
}

// Routes mounts both documents for GET and HEAD on r, at every pattern
// FederationDiscoveryPatterns names.
func (f FederationDiscovery) Routes(r chi.Router) {
	for _, p := range FederationDiscoveryPatterns(f.issuer) {
		h := f.jwks
		if strings.HasSuffix(p, accessapi.FederationDiscoveryPattern) {
			h = f.document
		}
		r.Get(p, h)
		r.Head(p, h)
	}
}

// document answers the discovery document, whose algorithms are those of
// the published keys, the first of which the read creates.
func (f FederationDiscovery) document(w http.ResponseWriter, r *http.Request) {
	keys, err := f.keys.FederationKeys(r.Context())
	if err != nil {
		problem.Failure(w, err)
		return
	}
	writeFederation(w, federation.NewDocument(f.issuer, f.jwksURI, keys))
}

// jwks answers the published federation keys, and no key of another
// purpose, so no trust policy can accept an API or run token.
func (f FederationDiscovery) jwks(w http.ResponseWriter, r *http.Request) {
	keys, err := f.keys.FederationKeys(r.Context())
	if err != nil {
		problem.Failure(w, err)
		return
	}
	set, err := federation.KeySet(keys)
	if err != nil {
		problem.Failure(w, err)
		return
	}
	writeFederation(w, set)
}

// writeFederation answers v as cacheable JSON, replacing the no-store
// every other answer of the listener carries.
func writeFederation(w http.ResponseWriter, v any) {
	w.Header().Set("Cache-Control", federationCache)
	writeJSON(w, http.StatusOK, v)
}
