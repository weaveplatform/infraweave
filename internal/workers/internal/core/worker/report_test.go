package worker_test

import (
	"context"
	"errors"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/worker"
)

// reports is the runs domain's reports: it records each call's run and
// whether it ran in a transaction, answers its fields, and fails with
// err.
type reports struct {
	runs []kernel.ULID
	inTx bool
	step workersapi.Step
	err  error
}

func (r *reports) seen(ctx context.Context, run kernel.ULID) {
	r.runs, r.inTx = append(r.runs, run), tx.In(ctx)
}

func (r *reports) Report(ctx context.Context, run kernel.ULID,
	_ workersapi.Report,
) (workersapi.Step, error) {
	r.seen(ctx, run)
	return r.step, r.err
}

func (r *reports) Uploads(ctx context.Context, run kernel.ULID, _ int64,
	u []workersapi.Upload,
) ([]workersapi.SignedUpload, error) {
	r.seen(ctx, run)
	return []workersapi.SignedUpload{{Name: u[0].Name}}, r.err
}

func (r *reports) LogChunks(ctx context.Context, run kernel.ULID, _ int64,
	_ []workersapi.LogChunk,
) error {
	r.seen(ctx, run)
	return r.err
}

func (r *reports) Artifacts(ctx context.Context, run kernel.ULID, _ int64,
	_ []workersapi.Recorded,
) error {
	r.seen(ctx, run)
	return r.err
}

func (r *reports) Workspace(ctx context.Context, run kernel.ULID) (string, error) {
	r.seen(ctx, run)
	return "https://store.test/ws", r.err
}

// TestUnitWorkerRunReports checks that a run token's bearer reaches the
// runs domain's reports for its own run, each call in a transaction, and
// is answered what they answer; that another run's token is refused
// before them; and that before BindReports a run is not on a worker.
func TestUnitWorkerRunReports(t *testing.T) {
	f := newFixture(t, worker.Deps{})
	ctx := t.Context()
	run := kernel.NewULID()
	b := accessapi.Bearer{ID: run}
	calls := func(b accessapi.Bearer) []error {
		_, rerr := f.svc.Report(ctx, b, run, workersapi.Report{})
		ups, uerr := f.svc.Uploads(ctx, b, run, 2, []workersapi.Upload{{Name: "0"}})
		if uerr == nil && ups[0].Name != "0" {
			uerr = errors.New("uploads not answered")
		}
		url, werr := f.svc.Workspace(ctx, b, run)
		if werr == nil && url != "https://store.test/ws" {
			werr = errors.New("workspace not answered")
		}
		return []error{
			rerr, uerr, f.svc.LogChunks(ctx, b, run, 2, nil),
			f.svc.Artifacts(ctx, b, run, 2, nil), werr,
		}
	}
	for i, err := range calls(b) {
		if !errors.Is(err, workersapi.ErrNotOnWorker) {
			t.Errorf("call %d before BindReports: %v", i, err)
		}
	}
	r := &reports{step: workersapi.Step{Action: workersapi.ActionExit}}
	f.svc.BindReports(r)
	for i, err := range calls(b) {
		if err != nil {
			t.Errorf("call %d: %v", i, err)
		}
	}
	if len(r.runs) != 5 || r.runs[0] != run || !r.inTx {
		t.Errorf("the reports saw %v, in a transaction %t", r.runs, r.inTx)
	}
	if got, err := f.svc.Report(ctx, b, run, workersapi.Report{}); err != nil ||
		got.Action != workersapi.ActionExit {
		t.Errorf("Report = %+v, %v", got, err)
	}
	for i, err := range calls(accessapi.Bearer{ID: kernel.NewULID()}) {
		if !errors.Is(err, worker.ErrUnauthenticated) {
			t.Errorf("call %d with another run's token: %v", i, err)
		}
	}
	r.err = workersapi.ErrMovedOn
	for i, err := range calls(b) {
		if !errors.Is(err, workersapi.ErrMovedOn) {
			t.Errorf("call %d refused: %v", i, err)
		}
	}
}
