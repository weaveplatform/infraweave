package schedule

import (
	"context"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// LivenessStore is the liveness's storage port. Each method joins the
// transaction its context carries.
type LivenessStore interface {
	// Held reads the runs worker holds, the ones it claimed last that are
	// in a state on a worker, and locks their rows FOR UPDATE.
	Held(ctx context.Context, worker kernel.ULID) ([]lifecycle.Run, error)
	// Overdue reads up to batch runs in a preparing state whose claim's
	// preparing deadline is before now, earliest deadline first, among
	// the rows no other transaction locks, and locks them FOR UPDATE.
	Overdue(ctx context.Context, now time.Time, batch int) ([]lifecycle.Run, error)
}

// The notes of the runs the liveness fails.
//
// ref: decision 0008 § "Liveness: offline workers and lost runs"
// ref: decision 0008 § "Timeouts and timers"
const (
	NoteWorkerLost       = "worker lost"
	NotePreparingTimeout = "preparing timeout"
	NoteKilled           = "killed"
	NoteRunTimeout       = "run timeout"
	NoteLogTimeout       = "no log output"
)

// Liveness is the runs domain's workersapi.Liveness: the reap's lost
// runs and overdue preparations, and a launcher's report of a container
// that ended.
type Liveness struct {
	store     LivenessStore
	lifecycle *lifecycle.Lifecycle
}

var _ workersapi.Liveness = (*Liveness)(nil)

// NewLiveness returns the liveness over store and l.
func NewLiveness(store LivenessStore, l *lifecycle.Lifecycle) *Liveness {
	return &Liveness{store: store, lifecycle: l}
}

// Lost fails each run worker holds, as workersapi.Liveness says.
func (s *Liveness) Lost(ctx context.Context, worker kernel.ULID) (int, error) {
	if !tx.In(ctx) {
		return 0, lifecycle.ErrNoTransaction
	}
	runs, err := s.store.Held(ctx, worker)
	if err != nil {
		return 0, err //nolint:wrapcheck // the store names the read
	}
	for _, r := range runs {
		if err := s.fail(ctx, r, lifecycle.Actor{}, NoteWorkerLost); err != nil {
			return 0, err
		}
	}
	return len(runs), nil
}

// Ended fails run when worker holds it, as workersapi.Liveness says.
func (s *Liveness) Ended(ctx context.Context, worker, run kernel.ULID, exit workersapi.Exit) (bool, error) {
	if !tx.In(ctx) {
		return false, lifecycle.ErrNoTransaction
	}
	runs, err := s.store.Held(ctx, worker)
	if err != nil {
		return false, err //nolint:wrapcheck // the store names the read
	}
	for _, r := range runs {
		if r.ID != run {
			continue
		}
		actor, note := lifecycle.Actor{}, exitNote(exit)
		if exit.Reason == workersapi.ExitKilled {
			actor, note = r.Kill.Ends(note)
		}
		return true, s.fail(ctx, r, actor, note)
	}
	return false, nil
}

// Overdue fails the runs left in preparation too long, as
// workersapi.Liveness says.
func (s *Liveness) Overdue(ctx context.Context, now time.Time, batch int) (int, error) {
	if !tx.In(ctx) {
		return 0, lifecycle.ErrNoTransaction
	}
	runs, err := s.store.Overdue(ctx, now, batch)
	if err != nil {
		return 0, err //nolint:wrapcheck // the store names the read
	}
	for _, r := range runs {
		if err := s.fail(ctx, r, lifecycle.Actor{}, NotePreparingTimeout); err != nil {
			return 0, err
		}
	}
	return len(runs), nil
}

// fail moves r to FAILED through the liveness edge out of its state,
// which every state on a worker has, recording actor and note.
func (s *Liveness) fail(ctx context.Context, r lifecycle.Run, actor lifecycle.Actor, note string) error {
	_, err := s.lifecycle.Transition(ctx, lifecycle.Request{
		Account: r.Account, Run: r.ID, Version: r.Version, To: kernel.RunStateFailed,
		Driver: machine.DriverLiveness, Actor: actor, Note: note,
	})
	return err //nolint:wrapcheck // the lifecycle names the move
}

// exitNote is the note of a run whose container ended as e; a reason
// the launcher did not learn names no cause. A container the launcher
// killed on a kill signal is NoteKilled, which the kill's own note
// replaces, and one it killed on its run or no-log timeout is
// NoteRunTimeout or NoteLogTimeout.
func exitNote(e workersapi.Exit) string {
	switch e.Reason {
	case workersapi.ExitExited:
		return fmt.Sprintf("container exited with code %d", e.Code)
	case workersapi.ExitKilled:
		return NoteKilled
	case workersapi.ExitOutOfMemory:
		return "container out of memory"
	case workersapi.ExitMissing:
		return "container missing"
	case workersapi.ExitEvicted:
		return "container evicted"
	case workersapi.ExitRunTimeout:
		return NoteRunTimeout
	case workersapi.ExitLogTimeout:
		return NoteLogTimeout
	}
	return "container ended"
}
