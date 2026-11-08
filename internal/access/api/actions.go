package api

import "slices"

// Action is a value of the reference's Action enum, which roles hold and
// the authorizer decides, or one of the pseudo-actions no role grants.
type Action string

// The pseudo-actions: operations decided outside the enum. ACCOUNT_ADMIN
// takes space-admin on the account's top space, ORGANIZATION_ADMIN the
// organization-admin role, and AUTHENTICATED any principal of the account.
const (
	ActionAccountAdmin      Action = "ACCOUNT_ADMIN"
	ActionOrganizationAdmin Action = "ORGANIZATION_ADMIN"
	ActionAuthenticated     Action = "AUTHENTICATED"
)

// Fallback is the legacy access level that grants an action when no role
// does. Each level grants what the levels below it grant.
type Fallback string

const (
	FallbackReader Fallback = "reader"
	FallbackWriter Fallback = "writer"
	FallbackAdmin  Fallback = "admin"
)

// Within reports whether level grants an action whose fallback is f.
func (f Fallback) Within(level Fallback) bool {
	rank := map[Fallback]int{FallbackReader: 1, FallbackWriter: 2, FallbackAdmin: 3}
	return rank[f] > 0 && rank[f] <= rank[level]
}

// ActionInfo is one catalogue row: the kind of entity whose space decides
// the action, its fallback, and whether the schema disables or deprecates
// it. Infraweave marks an action the reference's enum lacks: an account
// action on a resource that lives in no space (an API key, a session, a
// managed user or group), decided on the account's top space, or, when
// Anywhere, in any space where the caller holds it, as report 14 lets
// the admin of any space read keys, users and groups.
type ActionInfo struct {
	Action     Action
	Subject    string
	Fallback   Fallback
	Disabled   bool
	Deprecated bool
	Infraweave bool
	Anywhere   bool
}

// Assignable reports whether a role may hold the action: a disabled or
// deprecated one is refused (decision 0007).
func (i ActionInfo) Assignable() bool { return !i.Disabled && !i.Deprecated }

// Actions is the catalogue: the reference's enum in its order, then
// infraweave's actions by name.
func Actions() []ActionInfo { return slices.Clone(catalogue) }

// ActionOf is a's catalogue row; ok is false for a pseudo-action or a
// value the catalogue lacks.
func ActionOf(a Action) (info ActionInfo, ok bool) {
	i := slices.IndexFunc(catalogue, func(i ActionInfo) bool { return i.Action == a })
	if i < 0 {
		return ActionInfo{}, false
	}
	return catalogue[i], true
}
