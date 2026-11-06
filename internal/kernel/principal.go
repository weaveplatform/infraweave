package kernel

import (
	"errors"
	"fmt"
	"slices"
)

// PrincipalKind is what a request authenticated as (decision 0006).
type PrincipalKind string

// The principal kinds of decision 0006. A user is a member signed in, an
// API key a key's token, and a run a run's in-run token; organization is an
// organization key's token, break_glass an organization's break-glass
// sign-in, operator an operator key's token, and anonymous no credential.
const (
	PrincipalUser         PrincipalKind = "user"
	PrincipalAPIKey       PrincipalKind = "api_key"
	PrincipalRun          PrincipalKind = "run"
	PrincipalOrganization PrincipalKind = "organization"
	PrincipalBreakGlass   PrincipalKind = "break_glass"
	PrincipalOperator     PrincipalKind = "operator"
	PrincipalAnonymous    PrincipalKind = "anonymous"
)

// PrincipalKinds are every kind, in decision 0006's order.
var PrincipalKinds = []PrincipalKind{
	PrincipalUser,
	PrincipalAPIKey,
	PrincipalRun,
	PrincipalOrganization,
	PrincipalBreakGlass,
	PrincipalOperator,
	PrincipalAnonymous,
}

// ErrPrincipalKind reports text that names no principal kind.
var ErrPrincipalKind = errors.New("not a principal kind")

// ParsePrincipalKind reads a kind as decision 0006 spells it.
func ParsePrincipalKind(s string) (PrincipalKind, error) {
	if k := PrincipalKind(s); slices.Contains(PrincipalKinds, k) {
		return k, nil
	}
	return "", fmt.Errorf("%w: %q", ErrPrincipalKind, s)
}

// Principal is who a request acts as. ActorID is the user, API key,
// organization key or run, and the organization for break_glass;
// anonymous has none. A principal names the organization it
// belongs to, the one account it works in, if any, and, for a person, the
// member. An organization, break-glass or operator principal has no
// account, and nor has a member that no account admits.
type Principal struct {
	Kind           PrincipalKind
	ActorID        ULID
	OrganizationID OrganizationID
	AccountID      AccountID
	MemberID       MemberID
}

// Caller is the principal's key for decision 0003's idempotency keys and
// rate-limit buckets, <kind>:<actor ID>, so every session of one API key
// shares one budget.
func (p Principal) Caller() string {
	return string(p.Kind) + ":" + p.ActorID.String()
}
