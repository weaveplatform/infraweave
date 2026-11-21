package api

import (
	"context"
	"encoding/json"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// TaskInputBuilder projects a request before a task run exists. It grants no
// access and does not manufacture a run identity for a pre-creation decision.
type TaskInputBuilder interface {
	BuildTaskInput(context.Context, TaskInputFacts) (json.RawMessage, error)
}

// TaskInputFacts contains the command and owner-backed request/session/stack
// snapshot selected in the task-creation transaction.
type TaskInputFacts struct {
	Account  kernel.AccountID
	At       time.Time
	Command  string
	RemoteIP Fact[string]
	Session  SessionFacts
	Stack    StackFacts
}
