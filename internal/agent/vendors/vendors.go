package vendors

import (
	"errors"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// PlanFile is the saved plan's name in the project root, which planning
// writes and applying reads. A paused run's saved workspace carries it to
// the container that applies.
const PlanFile = "infraweave.plan"

// Command is one command of a phase: its arguments, the first the binary,
// and Stdout, a file its standard output is written to instead of the
// log; empty for the log.
type Command struct {
	Args   []string
	Stdout string
}

// Options are what a phase's commands take from the run: the lock
// timeout of each command that locks the state, whether to lock it at
// all, and the file planning writes the plan's JSON to.
type Options struct {
	LockTimeout time.Duration
	NoLock      bool
	PlanJSON    string
}

// Vendor is one tool's command set.
type Vendor interface {
	// Commands are the commands of state's phase, in the order they run.
	Commands(state wire.WorkerState, o Options) ([]Command, error)
}

// Errors of a command set that cannot be had.
var (
	ErrTool  = errors.New("no command set for the tool")
	ErrState = errors.New("the tool has no phase for the state")
)

// Lookup answers the command set of tool, the binary a run's
// specification names.
func Lookup(tool wire.RunSpecToolName) (Vendor, error) {
	switch tool {
	case wire.Terraform:
		return terraform{bin: string(wire.Terraform)}, nil
	case wire.Tofu:
		return terraform{bin: string(wire.Tofu)}, nil
	}
	return nil, fmt.Errorf("%w %q", ErrTool, tool)
}
