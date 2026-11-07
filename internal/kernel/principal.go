package kernel

import (
	"context"
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
	// SessionID is the session the credential stands for, the token's
	// jti; zero for a credential with no session row.
	SessionID ULID
	// Login, Name and Teams are what the session recorded of the actor: a
	// member's login, or an API key's ID and name, and its teams.
	Login string
	Name  string
	Teams []string
	// Admin is whether the session's actor is an account admin.
	Admin bool
	// Channel is the session's channel, such as UNKNOWN or WEB.
	Channel string
	// Scopes narrow an OAuth grant's token; empty for every other kind.
	Scopes []string
}

// principalKey is the context key of the request's principal.
type principalKey struct{}

// WithPrincipal returns ctx carrying p, which the authentication
// middleware sets on every request it authenticates.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the principal ctx carries, and false on a request
// no credential authenticated.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// Caller is the principal's key for decision 0003's idempotency keys and
// rate-limit buckets, <kind>:<actor ID>, so every session of one API key
// shares one budget.
func (p Principal) Caller() string {
	return string(p.Kind) + ":" + p.ActorID.String()
}

// Human reports whether a person acts: a member signed in, or an
// organization's break-glass admin. Every other kind is a key or a run.
func (p Principal) Human() bool {
	return p.Kind == PrincipalUser || p.Kind == PrincipalBreakGlass
}
