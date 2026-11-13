package kernel

// PlanDelta is what a run's plan changes: the resources it adds, changes
// in place, deletes, imports, moves and forgets, whether any root module
// output changes, and Resources, the state's resource count before the
// plan. A worker that does not count moves, forgets or resources reports
// zero. The workers domain takes it in a report, the runs domain keeps
// it, and the stacks domain shows the blocking run's.
//
// ref: decision 0008 § "Phases, reports and the plan decision"
type PlanDelta struct {
	Add       int
	Change    int
	Delete    int
	Import    int
	Move      int
	Forget    int
	Resources int
	Outputs   bool
}

// Changes reports whether the plan changes any resource or output. A
// move or a forget changes the state only when applied, so either is a
// change; Resources counts what the state holds, not what the plan does.
func (d PlanDelta) Changes() bool {
	return d.Add > 0 || d.Change > 0 || d.Delete > 0 || d.Import > 0 || d.Move > 0 ||
		d.Forget > 0 || d.Outputs
}
