package acceptance

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"syscall"

	"github.com/cucumber/godog"
	"gopkg.in/yaml.v3"
)

// handlerLines are the line each platform periodic task logs when it runs.
var handlerLines = map[string]string{
	"queue.prune":       "finished jobs pruned",
	"idempotency.purge": "expired idempotency keys purged",
	"events.prune":      "fanned-out events pruned",
}

// lockHolder names the session that holds a table lock across steps.
const lockHolder = "acceptance-lock"

// queueSteps drive two infraweave all instances' queues through the
// scenario's own connection and read the instances' logs.
type queueSteps struct {
	s *servedSteps
	// since is the highest job ID before the step that started the work
	// the scenario checks; logged is each line's count then.
	since  int
	logged map[string]int
	killed int
}

func (q *queueSteps) register(sc *godog.ScenarioContext) {
	sc.Step(
		`^two infraweave all instances, A and B, on one database with a (\d+)-second queue lease$`,
		q.twoInstances,
	)
	sc.Step(`^each platform periodic task has run once across both instances$`, q.ranOnce)
	sc.Step(`^the "([a-z.]+)" periodic task falls due$`, q.fallsDue)
	sc.Step(`^"([a-z.]+)" eventually runs once more, as one job$`, q.runsOnceMore)
	sc.Step(`^the instances logged "([^"]+)" once more between them$`, q.loggedOnceMore)
	sc.Step(`^the idempotency_keys table is renamed away$`, q.renameAway)
	sc.Step(
		`^the newest "([a-z.]+)" job eventually dies after (\d+) attempts with an error naming "([^"]+)"$`,
		q.dies,
	)
	sc.Step(
		`^a "([a-z.]+)" job with unique key "([^"]+)" is enqueued in a transaction that rolls back$`,
		q.rolledBack,
	)
	sc.Step(`^a "([a-z.]+)" job with unique key "([^"]+)" is enqueued and committed$`, q.committed)
	sc.Step(`^the job with unique key "([^"]+)" eventually succeeds$`, q.keySucceeds)
	sc.Step(`^no job with unique key "([^"]+)" was ever stored$`, q.neverStored)
	sc.Step(`^a session holds an exclusive lock on idempotency_keys$`, q.holdLock)
	sc.Step(`^the instance running "([a-z.]+)" is killed$`, q.killRunner)
	sc.Step(`^the lock is released$`, q.releaseLock)
	sc.Step(
		`^the newest "([a-z.]+)" job eventually succeeds on its attempt (\d+) after its lease lapsed$`,
		q.succeedsAfterLapse,
	)
	sc.Step(`^the surviving instance logged "([^"]+)"$`, q.survivorLogged)
}

// twoInstances starts two all instances with a short lease and fast
// polling, logging at debug so each task's run is a line.
func (q *queueSteps) twoInstances(ctx context.Context, lease int) error {
	db, err := q.s.w.database(ctx)
	if err != nil {
		return err
	}
	config, err := yaml.Marshal(map[string]any{
		"server": map[string]any{
			"listen":       "127.0.0.1:0",
			"admin_listen": "127.0.0.1:0",
			"shutdown":     map[string]any{"ready_delay": "0s"},
		},
		"database":    map[string]any{"url": db.DSN},
		"secrets":     secretsWith(nil),
		"objectstore": objectStore(q.s.w.objects()),
		"log":         map[string]any{"level": "debug"},
		"queue": map[string]any{
			"lease":          fmt.Sprintf("%ds", lease),
			"poll_interval":  "100ms",
			"scheduler_tick": "100ms",
		},
	})
	if err != nil {
		return fmt.Errorf("instance config: %w", err)
	}
	for range 2 {
		if _, err := q.s.w.launch(ctx, "all", config); err != nil {
			return err
		}
	}
	return nil
}

func (q *queueSteps) number(ctx context.Context, sql string) (int, error) {
	out, err := q.s.query(ctx, sql)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("%w: %q from %s", errStep, out, sql)
	}
	return n, nil
}

// lines counts line across every instance's log.
func (q *queueSteps) lines(line string) int {
	n := 0
	for _, srv := range q.s.w.servers {
		n += bytes.Count(srv.Stdout(), []byte(`"msg":"`+line+`"`))
	}
	return n
}

// mark records where the work a scenario starts begins: the highest job ID
// and each handler line's count.
func (q *queueSteps) mark(ctx context.Context) error {
	since, err := q.number(ctx, `SELECT coalesce(max(id), 0) FROM jobs`)
	if err != nil {
		return err
	}
	q.since = since
	q.logged = map[string]int{}
	for _, line := range handlerLines {
		q.logged[line] = q.lines(line)
	}
	return nil
}

// ranOnce waits for the runs both instances' startups made due, and checks
// there was one job and one handler line per task between them.
func (q *queueSteps) ranOnce(ctx context.Context) error {
	for kind, line := range handlerLines {
		err := Eventually(ctx, kind+" to run at startup", func(ctx context.Context) (bool, error) {
			n, err := q.number(ctx, fmt.Sprintf(
				`SELECT count(*) FROM jobs WHERE kind = %s AND state = 'succeeded'`, literal(kind)))
			return n >= 1 && q.lines(line) >= 1, err
		})
		if err != nil {
			return err
		}
		jobs, err := q.number(
			ctx,
			fmt.Sprintf(`SELECT count(*) FROM jobs WHERE kind = %s`, literal(kind)),
		)
		if err != nil {
			return err
		}
		if jobs != 1 || q.lines(line) != 1 {
			return fmt.Errorf("%w: %s ran as %d jobs and logged %q %d times, want once",
				errStep, kind, jobs, line, q.lines(line))
		}
	}
	due, err := q.number(ctx, `SELECT count(*) FROM periodic_tasks WHERE next_run_at <= now()`)
	if err == nil && due != 0 {
		err = fmt.Errorf("%w: %d periodic tasks still due", errStep, due)
	}
	return err
}

func (q *queueSteps) fallsDue(ctx context.Context, task string) error {
	if err := q.mark(ctx); err != nil {
		return err
	}
	_, err := q.s.query(ctx, fmt.Sprintf(
		`UPDATE periodic_tasks SET next_run_at = now() WHERE name = %s`, literal(task)))
	return err
}

// newest is the state, attempt and last error of the newest job of kind
// since the mark, or "" when there is none yet.
func (q *queueSteps) newest(
	ctx context.Context,
	kind string,
) (state string, attempt int, lastError string, err error) {
	out, err := q.s.query(
		ctx,
		fmt.Sprintf(`SELECT state || '|' || attempt || '|' || coalesce(last_error, '')
FROM jobs WHERE kind = %s AND id > %d ORDER BY id DESC LIMIT 1`, literal(kind), q.since),
	)
	if err != nil || strings.TrimSpace(out) == "" {
		return "", 0, "", err
	}
	parts := strings.SplitN(strings.TrimSpace(out), "|", 3)
	attempt, _ = strconv.Atoi(parts[1])
	return parts[0], attempt, parts[2], nil
}

func (q *queueSteps) runsOnceMore(ctx context.Context, kind string) error {
	err := Eventually(ctx, kind+" to run again", func(ctx context.Context) (bool, error) {
		state, _, _, err := q.newest(ctx, kind)
		return state == "succeeded", err
	})
	if err != nil {
		return err
	}
	n, err := q.number(ctx, fmt.Sprintf(`SELECT count(*) FROM jobs WHERE kind = %s AND id > %d`,
		literal(kind), q.since))
	if err == nil && n != 1 {
		err = fmt.Errorf("%w: %s fired as %d jobs, want 1", errStep, kind, n)
	}
	return err
}

func (q *queueSteps) loggedOnceMore(ctx context.Context, line string) error {
	want := q.logged[line] + 1
	err := Eventually(
		ctx,
		fmt.Sprintf("%q to be logged", line),
		func(context.Context) (bool, error) {
			return q.lines(line) >= want, nil
		},
	)
	if err == nil && q.lines(line) != want {
		err = fmt.Errorf("%w: %q logged %d times, want %d", errStep, line, q.lines(line), want)
	}
	return err
}

func (q *queueSteps) renameAway(ctx context.Context) error {
	_, err := q.s.query(ctx, `ALTER TABLE idempotency_keys RENAME TO idempotency_keys_away`)
	return err
}

func (q *queueSteps) dies(ctx context.Context, kind string, attempts int, naming string) error {
	var (
		state, lastError string
		attempt          int
	)
	err := Eventually(ctx, kind+" to die", func(ctx context.Context) (bool, error) {
		var err error
		state, attempt, lastError, err = q.newest(ctx, kind)
		return state == "dead", err
	})
	if err != nil {
		return err
	}
	if attempt != attempts || !strings.Contains(lastError, naming) {
		return fmt.Errorf(
			"%w: dead after %d attempts with %q; want %d attempts and an error naming %q",
			errStep,
			attempt,
			lastError,
			attempts,
			naming,
		)
	}
	return nil
}

func insertJob(kind, key string) string {
	return fmt.Sprintf(
		`INSERT INTO jobs (queue, kind, payload, unique_key, priority, run_at, state, max_attempts)
VALUES ('maintenance', %s, '{}', %s, 5, now(), 'pending', 5);
SELECT pg_notify('infraweave_jobs', 'maintenance');`,
		literal(kind),
		literal(key),
	)
}

func (q *queueSteps) rolledBack(ctx context.Context, kind, key string) error {
	if err := q.mark(ctx); err != nil {
		return err
	}
	_, err := q.s.query(ctx, "BEGIN;\n"+insertJob(kind, key)+"\nROLLBACK;")
	return err
}

func (q *queueSteps) committed(ctx context.Context, kind, key string) error {
	_, err := q.s.query(ctx, insertJob(kind, key))
	return err
}

func (q *queueSteps) keySucceeds(ctx context.Context, key string) error {
	return Eventually(ctx, "the job "+key+" to succeed", func(ctx context.Context) (bool, error) {
		n, err := q.number(ctx, fmt.Sprintf(
			`SELECT count(*) FROM jobs WHERE unique_key = %s AND state = 'succeeded'`,
			literal(key),
		))
		return n == 1, err
	})
}

func (q *queueSteps) neverStored(ctx context.Context, key string) error {
	n, err := q.number(
		ctx,
		fmt.Sprintf(`SELECT count(*) FROM jobs WHERE unique_key = %s`, literal(key)),
	)
	if err == nil && n != 0 {
		err = fmt.Errorf("%w: %d jobs with unique key %q", errStep, n, key)
	}
	return err
}

// holdLock takes the lock in a session that sleeps holding it until
// releaseLock ends it; dropping the scenario's database ends it otherwise.
func (q *queueSteps) holdLock(ctx context.Context) error {
	db, err := q.s.w.database(ctx)
	if err != nil {
		return err
	}
	go func() {
		_, _ = db.Query(context.WithoutCancel(ctx), fmt.Sprintf(`SET application_name = %s;
BEGIN;
LOCK TABLE idempotency_keys IN ACCESS EXCLUSIVE MODE;
SELECT pg_sleep(110);
COMMIT;`, literal(lockHolder)))
	}()
	return Eventually(ctx, "the lock to be held", func(ctx context.Context) (bool, error) {
		n, err := q.number(ctx, fmt.Sprintf(`SELECT count(*) FROM pg_locks l
JOIN pg_stat_activity a USING (pid)
WHERE a.application_name = %s AND l.relation = 'idempotency_keys'::regclass AND l.granted`,
			literal(lockHolder)))
		return n == 1, err
	})
}

// killRunner kills, without a chance to hand the job back, the instance
// whose worker holds the newest job of kind; a worker names itself
// host:pid on the job.
func (q *queueSteps) killRunner(ctx context.Context, kind string) error {
	var owner string
	err := Eventually(ctx, kind+" to be running", func(ctx context.Context) (bool, error) {
		out, err := q.s.query(ctx, fmt.Sprintf(`SELECT coalesce(lease_owner, '') FROM jobs
WHERE kind = %s AND id > %d AND state = 'running' ORDER BY id DESC LIMIT 1`, literal(kind), q.since))
		owner = strings.TrimSpace(out)
		return owner != "", err
	})
	if err != nil {
		return err
	}
	pid, err := strconv.Atoi(owner[strings.LastIndex(owner, ":")+1:])
	if err != nil {
		return fmt.Errorf("%w: lease owner %q", errStep, owner)
	}
	for _, srv := range q.s.w.servers {
		if srv.Pid() == pid {
			q.killed = pid
			return srv.Signal(syscall.SIGKILL)
		}
	}
	return fmt.Errorf("%w: no instance has pid %d", errStep, pid)
}

func (q *queueSteps) releaseLock(ctx context.Context) error {
	_, err := q.s.query(ctx, fmt.Sprintf(
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name = %s`,
		literal(lockHolder)))
	return err
}

func (q *queueSteps) succeedsAfterLapse(ctx context.Context, kind string, attempts int) error {
	var (
		state, lastError string
		attempt          int
	)
	err := Eventually(ctx, kind+" to succeed", func(ctx context.Context) (bool, error) {
		var err error
		state, attempt, lastError, err = q.newest(ctx, kind)
		return state == "succeeded", err
	})
	if err != nil {
		return err
	}
	if attempt != attempts || lastError != "lease lapsed" {
		return fmt.Errorf(
			"%w: succeeded on attempt %d after %q; want attempt %d after a lapsed lease",
			errStep,
			attempt,
			lastError,
			attempts,
		)
	}
	return nil
}

// survivorLogged checks that line was logged since the mark; the killed
// instance was blocked in its handler, so the line is the survivor's.
func (q *queueSteps) survivorLogged(ctx context.Context, line string) error {
	return Eventually(
		ctx,
		fmt.Sprintf("%q to be logged again", line),
		func(context.Context) (bool, error) {
			return q.lines(line) > q.logged[line], nil
		},
	)
}
