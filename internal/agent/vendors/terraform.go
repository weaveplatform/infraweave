package vendors

import (
	"fmt"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// terraform is the command set of Terraform and of OpenTofu, whose init,
// plan, show and apply take the same arguments; bin is the binary.
type terraform struct {
	bin string
}

// Commands are init for INITIALIZING; plan to PlanFile, then its JSON
// to o.PlanJSON, for PLANNING; and apply of PlanFile for APPLYING. Each
// command that can lock the state waits o.LockTimeout for the lock; under
// o.NoLock plan skips it, while apply, which writes the state, still
// locks. None asks for input.
//
// ref: decision 0010 § "How a run reaches it"
func (t terraform) Commands(state wire.WorkerState, o Options) ([]Command, error) {
	timeout := "-lock-timeout=" + o.LockTimeout.String()
	switch state {
	case wire.WorkerStateINITIALIZING:
		return []Command{{Args: []string{t.bin, "init", "-input=false", timeout}}}, nil
	case wire.WorkerStatePLANNING:
		plan := []string{t.bin, "plan", "-input=false", timeout}
		if o.NoLock {
			plan = append(plan, "-lock=false")
		}
		return []Command{
			{Args: append(plan, "-out="+PlanFile)},
			{Args: []string{t.bin, "show", "-json", PlanFile}, Stdout: o.PlanJSON},
		}, nil
	case wire.WorkerStateAPPLYING:
		return []Command{
			{Args: []string{t.bin, "apply", "-input=false", timeout, PlanFile}},
		}, nil
	}
	return nil, fmt.Errorf("%w: %s has none for %s", ErrState, t.bin, state)
}
