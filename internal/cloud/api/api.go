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

// Phase is what a phase receives of the stack's cloud integrations: the
// environment its credentials are handed over in, empty when no
// attachment serves the phase's side.
type Phase struct {
	Environment []Variable
}

// Credentials is the port the runs domain reads a phase's cloud
// credentials through. The cloud domain is constructed after runs, so
// internal/app binds its implementation into runs after construction, a
// back edge that adds no import.
//
// ref: decision 0004
type Credentials interface {
	ForPhase(ctx context.Context, r PhaseRequest) (Phase, error)
}
