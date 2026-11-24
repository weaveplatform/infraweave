// Package oidc is the access domain's OIDC adapter: Verifier is
// port.OIDC, which checks the JWT an OIDC API key's caller presents, and
// sso.RelyingParty, which an organization's OIDC sign-in reaches its
// provider through. Discover reads and caches for an hour a provider's
// discovery document, whose issuer must match and whose endpoints must be
// HTTPS; Exchange redeems a code with golang.org/x/oauth2 and keeps only
// the id_token; VerifyIDToken and VerifyLogout check an ID token and a
// back-channel logout token with go-oidc's verifier over this package's
// key cache, never go-oidc's own remote key set. It
// fetches the issuer's discovery document and the JWKS
// it names over HTTPS only, with a 5-second timeout and a 1 MiB limit, and
// never follows a redirect off HTTPS. Each issuer's JWKS is cached for an
// hour and fetched again at most once a minute for a kid it lacks. A token
// must be signed with an asymmetric algorithm (RS*, PS*, ES*, EdDSA) by a
// key of the JWKS, name the issuer exactly as iss, have the client ID in
// aud, and carry an exp; exp, nbf and iat hold within 60 seconds of skew.
//
// ref: decision 0006
//
// # Input and output
//
// HTTPS GETs of each issuer's /.well-known/openid-configuration and JWKS,
// and HTTPS POSTs of a sign-in's code to the provider's token endpoint,
// trusting the system's roots and the PEM roots of Options.RootCAs. It
// writes nothing and logs nothing; errors name the check that failed.
//
// # Dependencies
//
// It imports go-jose, github.com/coreos/go-oidc/v3, golang.org/x/oauth2
// and the access domain's port and sso packages. Only the access domain's
// assembly imports it.
package oidc
