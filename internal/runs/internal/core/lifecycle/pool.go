package lifecycle

import "github.com/weaveplatform/infraweave/internal/kernel"

// ExecutionPool is the selected private pool or the bound public installation
// identity. An unassigned public run has no concrete execution identity yet.
func (r Run) ExecutionPool() *kernel.ULID {
	if r.WorkerPool != nil {
		return r.WorkerPool
	}
	return r.PublicWorkerPool
}
