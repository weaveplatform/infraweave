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
// the discovery routes are served too.
//
// ref: decision 0006 § "OIDC federation token"
// ref: report 12 section 6.5
//
// # Input and output
//
// None: pure functions over strings and keys.
//
// # Dependencies
//
// It imports github.com/go-jose/go-jose/v4 for the keys and the standard
// library. The access domain's settings service, its HTTP adapter's
// discovery routes and the federation token's issuer import it.
package federation
