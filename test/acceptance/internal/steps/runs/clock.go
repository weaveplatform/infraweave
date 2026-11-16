package runs

import (
	"context"
	"fmt"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// clockSteps move the servers' clock on for one run or one worker: they
// move its timestamps back by the time that passes, so the next pass of
// run.timers or worker.reap finds it that much older, and a scenario
// reaches a timer's boundary without waiting for it. A real server reads
// the database's clock and its own, which no scenario can move, so the
// run or worker ages instead; the platform's "falls due" step runs the
// pass that reads it.
//
// ref: decision 0008 § "Timeouts and timers"
// ref: decision 0016
type clockSteps struct {
	w *harness.World
}

// passing is a span PostgreSQL reads as an interval, such as "7 days".
const passing = `(\d+ (?:minutes?|hours?|days?))`

func (c *clockSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^`+passing+` pass(?:es)? for the run "(\w+)"$`, c.run)
	sc.Step(`^`+passing+` pass(?:es)? for the worker "(\w+)"$`, c.worker)
	sc.Step(`^the outbox's run_updated event of the run "(\w+)" in the state "([A-Z_]+)" names `+
		`the timer "(\w+)"$`, c.timer)
}

// run ages the run kept as key by span: its creation, its state's last
// change, and each of its transitions' times.
func (c *clockSteps) run(ctx context.Context, span, key string) error {
	var runs, transitions int
	if err := c.w.Row(ctx, `WITH r AS (
			UPDATE runs SET created_at = created_at - $2::interval,
				state_changed_at = state_changed_at - $2::interval
			WHERE id = $1 RETURNING 1),
		t AS (
			UPDATE run_state_transitions SET timestamp = timestamp - $2::interval,
				until = until - $2::interval
			WHERE run_id = $1 RETURNING 1)
		SELECT (SELECT count(*) FROM r), (SELECT count(*) FROM t)`,
		[]any{harness.UUIDOf(c.w.IDs[key]), span}, &runs, &transitions); err != nil {
		return err //nolint:wrapcheck // names the query
	}
	if runs != 1 || transitions == 0 {
		return fmt.Errorf("%w: aged %d runs and %d transitions of the run %s", harness.ErrStep,
			runs, transitions, key)
	}
	return nil
}

// worker ages the worker kept as key by span: when the server last saw
// it. A live launcher's next poll sees it again, so a scenario kills the
// launcher first.
func (c *clockSteps) worker(ctx context.Context, span, key string) error {
	var workers int
	if err := c.w.Row(ctx, `WITH w AS (
			UPDATE workers SET last_seen_at = last_seen_at - $2::interval
			WHERE id = $1 RETURNING 1)
		SELECT count(*) FROM w`,
		[]any{harness.UUIDOf(c.w.IDs[key]), span}, &workers); err != nil {
		return err //nolint:wrapcheck // names the query
	}
	if workers != 1 {
		return fmt.Errorf("%w: aged %d workers kept as %s", harness.ErrStep, workers, key)
	}
	return nil
}

// timer checks that the run_updated event of the run kept as key in
// state names the run.timers rule want.
func (c *clockSteps) timer(ctx context.Context, key, state, want string) error {
	var got string
	if err := c.w.Row(ctx, `SELECT coalesce(string_agg(coalesce(document->>'timer', ''), ', '), '')
		FROM events WHERE kind = 'run_updated' AND subject_kind = 'run' AND subject_id = $1
			AND document->>'state' = $2`,
		[]any{c.w.IDs[key], state}, &got); err != nil {
		return err //nolint:wrapcheck // names the query
	}
	if got != want {
		return fmt.Errorf("%w: the run %s's run_updated events in %s name the timers %q, want %q",
			harness.ErrStep, key, state, got, want)
	}
	return nil
}
