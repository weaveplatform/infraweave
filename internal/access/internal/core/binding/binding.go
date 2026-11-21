package binding

import (
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ActorKind is the kind of principal a binding grants to.
type ActorKind string

const (
	ActorUser         ActorKind = "user"
	ActorGroup        ActorKind = "group"
	ActorAPIKey       ActorKind = "api_key"
	ActorStack        ActorKind = "stack"
	ActorFlowsProject ActorKind = "flows_project"
)

// Origin records an IAM binding's provenance or the distinct provenance of a
// session grant normalized for the evaluator.
type Origin string

const (
	OriginBinding    Origin = "binding"
	OriginAccessRule Origin = "access_rule"
	// OriginSchemeSwitch is a session grant, normalized for the evaluator;
	// it is never stored in role_bindings or attributed to an IAM binding.
	OriginSchemeSwitch Origin = "SCHEME_SWITCH"
	// LOGIN grants belong to an exact session, never to role_bindings.
	OriginAssignedRole Origin = "ASSIGNED_ROLE"
	OriginAccessLevel  Origin = "ACCESS_LEVEL"
)

// Binding grants Role's actions to one actor in Space and, by
// propagation, the spaces below it. CreatedBy is the caller key of the
// principal that made it.
type Binding struct {
	AccountID kernel.AccountID
	ID        kernel.ULID
	ActorKind ActorKind
	ActorID   kernel.ULID
	RoleID    kernel.ULID
	SpaceID   kernel.ULID
	Origin    Origin
	CreatedAt time.Time
	CreatedBy string
	Version   int64
}

// Listed is a binding as a view names it: its role's and space's slugs.
type Listed struct {
	Binding
	Role  string
	Space string
}
