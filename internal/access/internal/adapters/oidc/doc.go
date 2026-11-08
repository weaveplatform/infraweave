// Package oidc is the access domain's OIDC adapter: Verifier is
// port.OIDC, which checks the JWT an OIDC API key's caller presents. It
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
// trusting the system's roots and the PEM roots of Options.RootCAs. It
// writes nothing and logs nothing; errors name the check that failed.
//
// # Dependencies
//
// It imports go-jose and the access domain's port package. Only the
// access domain's assembly imports it.
package oidc
