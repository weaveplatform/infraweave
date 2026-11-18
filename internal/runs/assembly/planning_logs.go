package assembly

import runsapi "github.com/weaveplatform/infraweave/internal/runs/api"

// PlanningLogs exposes bounded masked text of an exact planning transition;
// callers receive no object key, signed URL or private execution configuration.
func (d *Domain) PlanningLogs() runsapi.PlanningLogs { return d.planningLogs }
