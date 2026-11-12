package platform

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// startupSteps drive an infraweave process mode that must refuse to start.
type startupSteps struct {
	w *harness.World
	// err is why the last start failed, nil when it started.
	err error
}

func (s *startupSteps) register(sc *godog.ScenarioContext) {
	sc.Step(
		`^the scenario's database was last migrated by infraweave (v\S+), compatible from version (\d+) only$`,
		s.newerSchema,
	)
	sc.Step(`^a PostgreSQL of its own older than the floor$`, s.oldPostgres)
	sc.Step(`^infraweave (all|scheduler) is started on the scenario's database$`, s.startOnDatabase)
	sc.Step(`^infraweave (all|scheduler) is started on that PostgreSQL$`, s.startOnOwn)
	sc.Step(`^it exits before it listens, saying "([^"]+)"$`, s.refused)
}

// newerSchema records what a newer release leaves behind: a platform stream
// this binary's migrations cannot run against.
func (s *startupSteps) newerSchema(ctx context.Context, release string, from int) error {
	db, err := s.w.Database(ctx)
	if err != nil {
		return err
	}
	_, err = db.Query(ctx, fmt.Sprintf(
		"INSERT INTO schema_state (stream, compatible_from, release) VALUES ('platform', %d, '%s') "+
			"ON CONFLICT (stream) DO UPDATE SET compatible_from = %d, release = '%s'",
		from,
		release,
		from,
		release,
	))
	return err
}

func (s *startupSteps) oldPostgres(ctx context.Context) error {
	pg, err := s.w.Suite.Deps.OldPostgres(ctx)
	if err != nil {
		return err
	}
	s.w.OwnPG = pg
	return nil
}

func (s *startupSteps) startOnDatabase(ctx context.Context, mode string) error {
	db, err := s.w.Database(ctx)
	if err != nil {
		return err
	}
	return s.start(ctx, mode, db.DSN)
}

func (s *startupSteps) startOnOwn(ctx context.Context, mode string) error {
	return s.start(ctx, mode, s.w.OwnPG.DSN)
}

func (s *startupSteps) start(ctx context.Context, mode, dsn string) error {
	config, err := harness.ServerConfig(dsn, s.w.Objects(), nil)
	if err != nil {
		return err
	}
	_, s.err = s.w.Launch(ctx, mode, config)
	return nil
}

// refused checks that the process exited 1 before its "listening" line, so
// no listener ever answered, and that what it wrote says why.
func (s *startupSteps) refused(why string) error {
	exit, ok := errors.AsType[*exec.ExitError](s.err)
	if !errors.Is(s.err, harness.ErrExited) || !ok || exit.ExitCode() != 1 {
		return fmt.Errorf(
			"%w: infraweave did not exit 1 before listening: %w",
			harness.ErrStep,
			s.err,
		)
	}
	if !strings.Contains(s.err.Error(), why) {
		return fmt.Errorf("%w: it exited without saying %q: %w", harness.ErrStep, why, s.err)
	}
	return nil
}
