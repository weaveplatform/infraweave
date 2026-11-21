package api

import (
	"context"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// LoginEvaluator joins the account-entry transaction. Policy refusals are
// decisions so their evidence can commit; operational errors roll it back.
// The access owner resolves names, applies its root-key exception and persists
// session grants. The evaluator grants no access by itself.
type LoginEvaluator interface {
	EvaluateLogin(context.Context, LoginRequest) (LoginEvaluation, error)
}

// LoginRequest contains account-owned metadata and original authentication
// facts. It carries no credential or a previous account's policy-produced teams.
type LoginRequest struct {
	Account  kernel.AccountID
	At       time.Time
	RemoteIP *string
	Session  LoginSession
	Spaces   []PolicySpace
	Roles    []PolicyRole
}

// LoginSession preserves unavailable membership and team facts rather than
// deriving upstream membership from a local managed-user row. TeamsKnown with
// nil Teams is an explicit null; a known empty list remains an empty array.
type LoginSession struct {
	Machine    bool
	Member     *bool
	Admin      *bool
	Login      string
	Name       string
	CreatorIP  *string
	IDPSubject *string
	TeamsKnown bool
	Teams      []string
	Identity   *ReviewIdentity
}

// LoginEvaluation contains unresolved, credential-free policy output. An
// account with no LOGIN policies leaves its documented fallback to access.
// Names in Roles and the legacy levels must resolve inside that same account.
type LoginEvaluation struct {
	HasPolicies bool
	Allow       bool
	Admin       bool
	Message     string
	Teams       []string
	Roles       map[string][]string
	SpaceAdmin  []string
	SpaceWrite  []string
	SpaceRead   []string
}
