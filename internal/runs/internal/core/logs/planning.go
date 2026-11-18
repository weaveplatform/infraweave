package logs

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/report"
)

// PlanningState contains only the existence, deletion and chunk extent of an
// exact PLANNING transition. Private run configuration never enters this read.
type PlanningState struct {
	Deleted bool
	Chunks  int64
}

// PlanningStore projects a planning transition and pages its chunk index in the
// caller's transaction; it does not load the run's execution configuration.
type PlanningStore interface {
	PlanningState(ctx context.Context, account kernel.AccountID, run kernel.ULID, version int64) (PlanningState, error)
	Chunks(ctx context.Context, account kernel.AccountID, run kernel.ULID, version, from int64, limit int) ([]report.Chunk, error)
}

// PlanningObjects opens the existing masked log objects without signing URLs.
type PlanningObjects interface {
	Open(ctx context.Context, store objectstore.Name, key string) (io.ReadCloser, error)
}

// PlanningReader streams exact planning logs into a bounded tail, retaining
// raw newline bytes and a missing-chunk placeholder for unavailable portions.
type PlanningReader struct {
	store   PlanningStore
	objects PlanningObjects
}

func NewPlanningReader(store PlanningStore, objects PlanningObjects) *PlanningReader {
	return &PlanningReader{store: store, objects: objects}
}

func (r *PlanningReader) PlanningLog(ctx context.Context, account kernel.AccountID, run kernel.ULID, version int64, maxBytes int) (runsapi.PlanningLog, error) {
	if version <= 0 || maxBytes <= 0 || maxBytes > runsapi.MaxPlanningLogBytes {
		return runsapi.PlanningLog{}, runsapi.ErrPlanningLogInput
	}
	if account.IsZero() || run.IsZero() {
		return runsapi.PlanningLog{}, runsapi.ErrPlanningLogNotFound
	}
	state, err := r.store.PlanningState(ctx, account, run, version)
	if err != nil {
		return runsapi.PlanningLog{}, fmt.Errorf("runs: read planning log state: %w", err)
	}
	if state.Deleted {
		return runsapi.PlanningLog{Unavailable: true}, nil
	}
	if state.Chunks < 0 {
		return runsapi.PlanningLog{}, errPlanningIndex
	}
	tail := newPlanningTail(maxBytes)
	unavailable := false
	for from := int64(0); from < state.Chunks; {
		if err := ctx.Err(); err != nil {
			return runsapi.PlanningLog{}, fmt.Errorf("runs: planning log canceled: %w", err)
		}
		chunks, err := r.store.Chunks(ctx, account, run, version, from, pageChunks)
		if err != nil {
			return runsapi.PlanningLog{}, fmt.Errorf("runs: read planning log index: %w", err)
		}
		for _, chunk := range chunks {
			if chunk.Version != version || chunk.State != kernel.RunStatePlanning || chunk.Seq < from || chunk.Seq >= state.Chunks {
				return runsapi.PlanningLog{}, errPlanningIndex
			}
			if chunk.Seq > from {
				unavailable = true
				if err := tail.missing(chunk.Seq - from); err != nil {
					return runsapi.PlanningLog{}, err
				}
			}
			missing, err := r.readChunk(ctx, tail, chunk)
			if err != nil {
				return runsapi.PlanningLog{}, err
			}
			unavailable = unavailable || missing
			from = chunk.Seq + 1
		}
		if len(chunks) < pageChunks && from < state.Chunks {
			unavailable = true
			if err := tail.missing(state.Chunks - from); err != nil {
				return runsapi.PlanningLog{}, err
			}
			break
		}
	}
	out := tail.result()
	out.Unavailable = unavailable
	return out, nil
}

var errPlanningIndex = errors.New("runs: inconsistent planning log index")

func (r *PlanningReader) readChunk(ctx context.Context, tail *planningTail, chunk report.Chunk) (bool, error) {
	if chunk.Lost {
		return true, tail.missing(1)
	}
	stream, err := r.objects.Open(ctx, objectstore.RunLogs, chunk.Key)
	if errors.Is(err, objectstore.ErrNotFound) {
		return true, tail.missing(1)
	}
	if err != nil {
		return false, fmt.Errorf("runs: open planning log chunk: %w", err)
	}
	readErr := copyPlanning(ctx, tail, stream)
	closeErr := stream.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return false, fmt.Errorf("runs: read planning log chunk: %w", err)
	}
	return false, nil
}

func copyPlanning(ctx context.Context, tail *planningTail, stream io.Reader) error {
	var buffer [32 << 10]byte
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("runs: stream planning log: %w", err)
		}
		n, err := stream.Read(buffer[:])
		tail.write(buffer[:n])
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("runs: stream planning log: %w", err)
		}
	}
}

var _ runsapi.PlanningLogs = (*PlanningReader)(nil)
