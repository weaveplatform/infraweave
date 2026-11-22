package api

import "github.com/weaveplatform/infraweave/internal/kernel"

// TriggerContext contains owner-supplied terminal-run and target facts. A known
// absent trigger is distinct from unavailable ancestry. Workflow graphs and
// module-release inputs are supplied by their owning workflow and module services.
type TriggerContext struct {
	Priority      kernel.RunPriority
	TriggerAbsent bool
	History       Fact[[]TransitionFacts]
	Stacks        []TriggerStackFacts
}

// TriggerStackFacts keeps a target's actual queue count beside its stack facts.
type TriggerStackFacts struct {
	Stack      StackFacts
	QueuedRuns Fact[int64]
}

// TransitionFacts retains committed history without borrowing an HTTP view.
type TransitionFacts struct {
	State          kernel.RunState
	Note, Username Fact[string]
}
