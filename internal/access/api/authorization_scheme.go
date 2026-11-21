package api

// AuthorizationScheme selects one account's source of live authorization.
// Saved configuration for the other scheme remains intact.
type AuthorizationScheme string

const (
	ManagedUsers  AuthorizationScheme = "MANAGED_USERS"
	LoginPolicies AuthorizationScheme = "LOGIN_POLICIES"
)

// Valid reports whether s names a supported account authorization strategy.
func (s AuthorizationScheme) Valid() bool { return s == ManagedUsers || s == LoginPolicies }

const (
	SchemeChangedKind    = "account.authorization_scheme_changed"
	SchemeChangedVersion = 1
	SchemeChangedSchema  = `{"type":"object","additionalProperties":false,"required":["previous","current"],"properties":{"previous":{"enum":["MANAGED_USERS","LOGIN_POLICIES"]},"current":{"enum":["MANAGED_USERS","LOGIN_POLICIES"]}}}`
)

// SchemeChanged contains only the two strategy names, without credentials or
// policy input. The event envelope supplies the account; the audit trail names
// the actor in the same transaction.
type SchemeChanged struct {
	Previous AuthorizationScheme `json:"previous"`
	Current  AuthorizationScheme `json:"current"`
}
