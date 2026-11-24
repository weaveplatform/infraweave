package oidc

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
	"golang.org/x/oauth2"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/sso"
)

var _ sso.RelyingParty = (*Verifier)(nil)

// discoveryPath is where an issuer publishes its discovery document,
// below the issuer identifier (OpenID Connect Discovery, section 4).
const discoveryPath = "/.well-known/openid-configuration"

// document is the part of a discovery document a relying party reads.
//
//nolint:tagliatelle // the names OpenID Connect Discovery fixes
type document struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	EndSessionEndpoint    string `json:"end_session_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

// discovered is a cached discovery document and when it was read.
type discovered struct {
	ep sso.Endpoints
	at time.Time
}

// locate splits host, an issuer or its discovery URL, into the issuer the
// document must name and the URL it is read from.
func locate(host string) (issuer, at string) {
	if iss, ok := strings.CutSuffix(host, discoveryPath); ok {
		return iss, host
	}
	return host, strings.TrimSuffix(host, "/") + discoveryPath
}

// Discover implements sso.RelyingParty: it reads the discovery document
// at host over HTTPS, whose issuer must be the issuer host names exactly
// and whose authorization and token endpoints must be HTTPS URLs, as must
// an end-session endpoint it names. A document read is kept for CacheFor;
// a failed read is not kept.
func (v *Verifier) Discover(ctx context.Context, host string) (sso.Endpoints, error) {
	now := v.now()
	v.mu.Lock()
	d, ok := v.docs[host]
	v.mu.Unlock()
	if ok && now.Sub(d.at) < CacheFor {
		return d.ep, nil
	}
	iss, at := locate(host)
	var doc document
	if err := v.get(ctx, at, &doc); err != nil {
		return sso.Endpoints{}, err
	}
	if doc.Issuer != iss {
		return sso.Endpoints{}, fmt.Errorf("%w: %s's discovery document names issuer %q", ErrIssuer, host, doc.Issuer)
	}
	for name, u := range map[string]string{
		"authorization_endpoint": doc.AuthorizationEndpoint, "token_endpoint": doc.TokenEndpoint,
	} {
		if !isHTTPS(u) {
			return sso.Endpoints{}, fmt.Errorf("%w: %s's %s %q is not an HTTPS URL", ErrIssuer, iss, name, u)
		}
	}
	if doc.EndSessionEndpoint != "" && !isHTTPS(doc.EndSessionEndpoint) {
		return sso.Endpoints{}, fmt.Errorf("%w: %s's end_session_endpoint %q is not an HTTPS URL",
			ErrIssuer, iss, doc.EndSessionEndpoint)
	}
	ep := sso.Endpoints{
		Issuer: doc.Issuer, Authorization: doc.AuthorizationEndpoint, Token: doc.TokenEndpoint,
		EndSession: doc.EndSessionEndpoint,
	}
	v.mu.Lock()
	v.docs[host] = discovered{ep: ep, at: now}
	v.mu.Unlock()
	return ep, nil
}

func isHTTPS(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != ""
}

// Exchange implements sso.RelyingParty with golang.org/x/oauth2 over the
// verifier's HTTPS client, so the token endpoint is reached as the
// discovery document is: HTTPS only, within Timeout. Only the answer's
// id_token is kept.
func (v *Verifier) Exchange(ctx context.Context, ep sso.Endpoints, c sso.Client, code, verifier string) (string, error) {
	conf := oauth2.Config{
		ClientID:     c.ID,
		ClientSecret: c.Secret,
		Endpoint:     oauth2.Endpoint{AuthURL: ep.Authorization, TokenURL: ep.Token},
		RedirectURL:  c.RedirectURI,
	}
	tok, err := conf.Exchange(context.WithValue(ctx, oauth2.HTTPClient, v.client), code,
		oauth2.VerifierOption(verifier))
	if err != nil {
		return "", fmt.Errorf("%w: the token endpoint: %w", ErrIssuer, err)
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return "", fmt.Errorf("%w: the token endpoint answered no id_token", ErrIssuer)
	}
	return raw, nil
}

// algorithmNames are algorithms as go-oidc names them.
func algorithmNames() []string {
	out := make([]string, 0, len(algorithms))
	for _, a := range algorithms {
		out = append(out, string(a))
	}
	return out
}

// verifier is go-oidc's verifier for issuer and clientID over the key set
// of this Verifier, with the expiry left to times, which allows Leeway.
func (v *Verifier) verifier(issuer, clientID string) *gooidc.IDTokenVerifier {
	return gooidc.NewVerifier(issuer, issuerKeys{v: v, issuer: issuer}, &gooidc.Config{
		ClientID:             clientID,
		SupportedSigningAlgs: algorithmNames(),
		SkipExpiryCheck:      true,
		Now:                  v.now,
	})
}

// VerifyIDToken implements sso.RelyingParty with go-oidc's verifier: the
// signature by a key of the issuer's JWKS, the algorithm, iss and aud;
// then exp, which must be present, and nbf within Leeway.
func (v *Verifier) VerifyIDToken(ctx context.Context, issuer, clientID, raw string) (map[string]any, error) {
	tok, err := v.verifier(issuer, clientID).Verify(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrToken, err)
	}
	var claims map[string]any
	if err := tok.Claims(&claims); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrToken, err)
	}
	if err := v.times(tok.Expiry, claims["nbf"]); err != nil {
		return nil, err
	}
	return claims, nil
}

// VerifyLogout implements sso.RelyingParty with go-oidc's VerifyLogout,
// which also checks the logout event, the absence of a nonce and a sub
// or sid, and then exp within Leeway.
func (v *Verifier) VerifyLogout(ctx context.Context, issuer, clientID, raw string) (sso.LogoutToken, error) {
	tok, err := v.verifier(issuer, clientID).VerifyLogout(ctx, raw)
	if err != nil {
		return sso.LogoutToken{}, fmt.Errorf("%w: %w", ErrToken, err)
	}
	if err := v.times(tok.Expiry, nil); err != nil {
		return sso.LogoutToken{}, err
	}
	return sso.LogoutToken{ID: tok.TokenID, Subject: tok.Subject, SID: tok.SessionID, Expiry: tok.Expiry}, nil
}

// times refuses an exp in the past, or an nbf in the future, by more than
// Leeway.
func (v *Verifier) times(exp time.Time, nbf any) error {
	now := v.now()
	if exp.IsZero() || now.After(exp.Add(Leeway)) {
		return fmt.Errorf("%w: expired at %s", ErrToken, exp.UTC().Format(time.RFC3339))
	}
	if n, ok := nbf.(float64); ok && now.Add(Leeway).Before(time.Unix(int64(n), 0)) {
		return fmt.Errorf("%w: not valid before %d", ErrToken, int64(n))
	}
	return nil
}

// issuerKeys is go-oidc's KeySet over the Verifier's cache of issuer's JWKS,
// so a sign-in fetches keys as an OIDC API key's exchange does: HTTPS
// only, at most MaxBody, cached for CacheFor, refetched for an unknown kid
// at most every RefetchEvery, and without certificate members.
type issuerKeys struct {
	v      *Verifier
	issuer string
}

// VerifySignature implements go-oidc's KeySet: it verifies the signature
// of raw, in the compact serialization and so signed once, with the
// issuer's keys its kid names, all of them for no kid, and answers the
// payload. Only an asymmetric algorithm parses.
func (k issuerKeys) VerifySignature(ctx context.Context, raw string) ([]byte, error) {
	jws, err := jose.ParseSignedCompact(raw, algorithms)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrToken, err)
	}
	keys, err := k.v.keys(ctx, k.issuer, jws.Signatures[0].Header.KeyID)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		if payload, err := jws.Verify(key); err == nil {
			return payload, nil
		}
	}
	return nil, fmt.Errorf("%w: no key of %s verifies the signature", ErrToken, k.issuer)
}
