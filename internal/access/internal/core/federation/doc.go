// Package federation holds what the OIDC federation token's relying
// parties read of it. The subject template of an account's token:
// DefaultSubjectTemplate, which an account that set none uses;
// CheckSubjectTemplate, which refuses a template with ErrInvalid naming
// the offending part; and RenderSubject, which fills a template's
// placeholders from a run's Caller and refuses a subject longer than
// MaxSubject. And the issuer's discovery: NewDocument builds the
// discovery document of an issuer, its jwks_uri JWKSURI's, its signing
// algorithms those of the published keys and its claims Claims, the
// token's; KeySet builds the JWKS of the published keys, sorted by kid,
// with use sig and the public members alone, ErrUnpublishable for a key
// with no public half; and IssuerPath is the issuer's path, under which
// the discovery routes are served too. And the token itself: NewToken
// builds the claim set of an Issue, exactly Claims, as a Token, whose
// aud is Audience's, auth.federation.audience or the issuer's host
// followed by "/" and the account's slug, whose spacePath is SpacePath's
// of the space's ancestry, and whose scope is Scope's for the run's type,
// the phase its handover starts and its stack's autodeploy; a parse run
// gets none, ErrNoToken.
//
// ref: decision 0006 § "OIDC federation token"
// ref: report 12 sections 6.3, 6.4 and 6.5
//
// # Input and output
//
// None: pure functions over strings, keys and the request's time.
//
// # Dependencies
//
// It imports github.com/go-jose/go-jose/v4 for the keys, internal/kernel
// for the run's identifiers, type and state, and the standard library.
// The access domain's settings service, its HTTP adapter's discovery
// routes and the federation token's issuer in authn import it.
package federation
