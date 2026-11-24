package api

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Scope is the side of a stack's attachments a run's phase reads its
// credentials from: ScopeRead for planning phases and proposed runs,
// ScopeWrite for applying, tasks, destroy runs and module test runs.
//
// ref: decision 0006 "AWS external ID and session name"
type Scope string

const (
	ScopeRead  Scope = "read"
	ScopeWrite Scope = "write"
)

// PhaseRequest names the phase a run asks credentials for: the account,
// the stack, the run and the side of the stack's attachments.
type PhaseRequest struct {
	Account kernel.AccountID
	Stack   kernel.ULID
	Run     kernel.ULID
	Scope   Scope
}

// Variable is an environment variable a phase receives; Secret marks one
// the run's log masks.
type Variable struct {
	Name, Value string
	Secret      bool
}

// Assumption is a role a private worker assumes for a phase with its own
// credentials: the integration's ID and name, the role, the external ID
// and RoleSessionName it passes, the session's duration in seconds, the
// region whose STS endpoint it calls, empty for the worker's default, and
// the session tags, empty unless the integration tags its sessions. None
// of it is a secret.
//
// ref: decision 0013 § "Delivery to runs"
type Assumption struct {
	Integration     kernel.ULID
	IntegrationName string
	RoleARN         string
	ExternalID      string
	SessionName     string
	DurationSeconds int32
	Region          string
	Tags            map[string]string
}

// Phase is what a phase receives of the stack's cloud integrations: the
// Environment its credentials are handed over in when the server assumes
// the role, or the WorkerAssumptions its private worker makes when the
// integration assumes its role there; both empty when no attachment
// serves the phase's side.
type Phase struct {
	Environment       []Variable
	WorkerAssumptions []Assumption
}

// The computed environment variables a phase receives the AWS credentials
// the server assumes in. AWS_SECURITY_TOKEN repeats AWS_SESSION_TOKEN for
// tools that read the older name.
//
// ref: report 12 § "2.1 Behaviour"
const (
	AWSAccessKeyID     = "AWS_ACCESS_KEY_ID"
	AWSSecretAccessKey = "AWS_SECRET_ACCESS_KEY" //nolint:gosec // an environment variable's name, not a credential
	AWSSecurityToken   = "AWS_SECURITY_TOKEN"    //nolint:gosec // as above
	AWSSessionToken    = "AWS_SESSION_TOKEN"     //nolint:gosec // as above
)

// AWSVariables are the computed AWS credential variables, by name.
func AWSVariables() []string {
	return []string{AWSAccessKeyID, AWSSecretAccessKey, AWSSecurityToken, AWSSessionToken}
}

// Credentials is the port the runs domain reads a phase's cloud
// credentials through. The cloud domain is constructed after runs, so
// internal/app binds its implementation into runs after construction, a
// back edge that adds no import. ForPhase answers a *kernel.Invalid
// naming the integration when STS refuses or fails the assumption, which
// fails the run in preparation.
//
// ref: decision 0004
type Credentials interface {
	ForPhase(ctx context.Context, r PhaseRequest) (Phase, error)
}
