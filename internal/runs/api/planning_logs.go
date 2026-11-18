package api

import (
	"context"
	"errors"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// MaxPlanningLogBytes bounds the retained tail independently of log length.
const MaxPlanningLogBytes = 4 << 20

var (
	// ErrPlanningLogNotFound reports a missing or foreign run, or a version
	// at which that run did not enter PLANNING.
	ErrPlanningLogNotFound = errors.New("runs: planning log not found")
	// ErrPlanningLogInput refuses a nonpositive state version or a byte budget
	// outside 1..MaxPlanningLogBytes.
	ErrPlanningLogInput = errors.New("runs: invalid planning log input")
)

// PlanningLog is a bounded tail of the exact stored, worker-masked log bytes,
// including their newline endings. CutLines counts fully omitted lines;
// PartialLine says the first retained line lacks its prefix. A huge single
// truncated line therefore has CutLines 0 and PartialLine true. Unavailable
// marks deletion or missing/expired/lost chunks; partial logs retain other
// stored text and the existing server-owned missing-chunk placeholder.
type PlanningLog struct {
	Text        string
	CutLines    int
	PartialLine bool
	Unavailable bool
}

// PlanningLogs reads an exact PLANNING state version without exposing object
// keys, signed URLs, private configuration or credentials. Reads join ctx's
// transaction; callers supply positive maxBytes no greater than 4MiB. An exact
// existing planning state with zero chunks is an available empty log.
type PlanningLogs interface {
	PlanningLog(ctx context.Context, account kernel.AccountID, run kernel.ULID, version int64, maxBytes int) (PlanningLog, error)
}
