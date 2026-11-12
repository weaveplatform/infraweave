package worker

import (
	"context"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// onRun runs fn in a transaction with the runs domain's reports, for the
// bearer b of a run token on run: ErrUnauthenticated when the token is
// another run's, and workersapi.ErrNotOnWorker before BindReports.
func (s *Service) onRun(
	ctx context.Context,
	b accessapi.Bearer,
	run kernel.ULID,
	what string,
	fn func(ctx context.Context, r workersapi.Reports) error,
) error {
	if b.ID != run {
		return refuse("run %s's token asks for run %s's %s", b.ID, run, what)
	}
	if s.reports == nil {
		return workersapi.ErrNotOnWorker
	}
	//nolint:wrapcheck // the runs domain names its refusal
	return tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		return fn(ctx, s.reports)
	})
}

// Report passes the bearer b's report on run to the runs domain, which
// moves the run in the same transaction, and answers the next step.
//
// ref: decision 0008 § "Phases, reports and the plan decision"
func (s *Service) Report(
	ctx context.Context,
	b accessapi.Bearer,
	run kernel.ULID,
	r workersapi.Report,
) (workersapi.Step, error) {
	var out workersapi.Step
	err := s.onRun(ctx, b, run, "report", func(ctx context.Context, rep workersapi.Reports) error {
		var err error
		out, err = rep.Report(ctx, run, r)
		return err //nolint:wrapcheck // the runs domain names its refusal
	})
	return out, err
}

// Uploads signs the bearer b's uploads of run's state version.
//
// ref: decision 0008 § "Logs and artifacts"
func (s *Service) Uploads(
	ctx context.Context,
	b accessapi.Bearer,
	run kernel.ULID,
	version int64,
	u []workersapi.Upload,
) ([]workersapi.SignedUpload, error) {
	var out []workersapi.SignedUpload
	err := s.onRun(ctx, b, run, "uploads", func(ctx context.Context, r workersapi.Reports) error {
		var err error
		out, err = r.Uploads(ctx, run, version, u)
		return err //nolint:wrapcheck // the runs domain names its refusal
	})
	return out, err
}

// LogChunks indexes the bearer b's uploaded log chunks of run's state
// version.
func (s *Service) LogChunks(
	ctx context.Context,
	b accessapi.Bearer,
	run kernel.ULID,
	version int64,
	c []workersapi.LogChunk,
) error {
	return s.onRun(
		ctx,
		b,
		run,
		"log chunks",
		func(ctx context.Context, r workersapi.Reports) error {
			return r.LogChunks(ctx, run, version, c) //nolint:wrapcheck // the runs domain names it
		},
	)
}

// Artifacts records the bearer b's uploaded objects of run's state
// version.
func (s *Service) Artifacts(
	ctx context.Context,
	b accessapi.Bearer,
	run kernel.ULID,
	version int64,
	rec []workersapi.Recorded,
) error {
	return s.onRun(ctx, b, run, "artifacts", func(ctx context.Context, r workersapi.Reports) error {
		return r.Artifacts(ctx, run, version, rec) //nolint:wrapcheck // the runs domain names it
	})
}

// Workspace answers a pre-signed GET URL of the workspace the bearer b's
// run recorded last.
func (s *Service) Workspace(
	ctx context.Context,
	b accessapi.Bearer,
	run kernel.ULID,
) (string, error) {
	var url string
	err := s.onRun(ctx, b, run, "workspace", func(ctx context.Context, r workersapi.Reports) error {
		var err error
		url, err = r.Workspace(ctx, run)
		return err //nolint:wrapcheck // the runs domain names its refusal
	})
	return url, err
}
