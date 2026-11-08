package role

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

// Origin is how a binding was made: a role binding, or a legacy
// SpaceAccessRule input stored as one.
type Origin string

const (
	OriginBinding    Origin = "binding"
	OriginAccessRule Origin = "access_rule"
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
}
