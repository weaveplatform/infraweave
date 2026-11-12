package report

import (
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// StepOf is step: what a worker does with run as it is.
func StepOf(run lifecycle.Run) workersapi.Step { return step(run) }
