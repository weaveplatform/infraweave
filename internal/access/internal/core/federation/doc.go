// Package federation holds the subject template of an account's OIDC
// federation token: DefaultSubjectTemplate, which an account that set none
// uses; CheckSubjectTemplate, which refuses a template with ErrInvalid
// naming the offending part; and RenderSubject, which fills a template's
// placeholders from a run's Caller and refuses a subject longer than
// MaxSubject.
//
// ref: decision 0006 § "OIDC federation token"
// ref: report 12 section 6.5
//
// # Input and output
//
// None: pure functions over strings.
//
// # Dependencies
//
// It imports the standard library only. The access domain's settings
// service and the federation token's issuer import it.
package federation
