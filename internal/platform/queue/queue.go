package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// Name is a logical queue: each has its own workers per replica, so a
// backlog in one never starves another.
type Name string

// The logical queues.
//
// ref: decision 0011
const (
	Default     Name = "default"
	Events      Name = "events"
	VCSInbox    Name = "vcs-inbox"
	Deliveries  Name = "deliveries"
	Scheduler   Name = "scheduler"
	Flows       Name = "flows"
	Maintenance Name = "maintenance"
)

// Queues are every logical queue, in the order Stats reports them.
//
// ref: decision 0011
var Queues = []Name{Default, Events, VCSInbox, Deliveries, Scheduler, Flows, Maintenance}

// Policy resolves an enqueue whose kind and unique key match a pending or
// running job.
type Policy int

const (
	// Coalesce keeps a pending job's earlier run time and takes the newer
	// payload; a running job is marked to run once more after its current
	// attempt, so a request that arrives mid-run is honoured.
	Coalesce Policy = iota + 1
	// Ignore drops the duplicate.
	Ignore
)

// Enqueue's defaults and the payload limit.
//
// ref: decision 0011
const (
	DefaultPriority    = 5
	DefaultMaxAttempts = 5
	// MaxPayload is the largest payload, as JSON text.
	MaxPayload = 64 << 10
)

// Kind is a job kind: a code constant <area>.<verb>, with the queue it runs
// on and the policy its duplicates follow. Every process that enqueues the
// kind declares the same value, so a replica that runs no handler for it
// still enqueues it correctly.
type Kind struct {
	Name   string
	Queue  Name
	Policy Policy
	// MaxAttempts is the attempt limit; 0 means DefaultMaxAttempts.
	MaxAttempts int
}

// Errors of enqueueing and of a claimed job's transitions.
var (
	// ErrKind reports a kind with no name, an unknown queue or no policy.
	ErrKind = errors.New("invalid job kind")
	// ErrJob reports an option out of range, such as a priority above 9.
	ErrJob = errors.New("invalid job")
	// ErrPayloadTooLarge reports a payload over MaxPayload.
	ErrPayloadTooLarge = errors.New("job payload over 64 KiB")
	// ErrNoTransaction reports a call that must join its caller's
	// transaction made outside one.
	ErrNoTransaction = errors.New("no transaction on the context")
)

func (k Kind) validate() error {
	if k.Name == "" || !slices.Contains(Queues, k.Queue) ||
		k.Policy != Coalesce && k.Policy != Ignore ||
		k.MaxAttempts < 0 {
		return fmt.Errorf("%w: %+v", ErrKind, k)
	}
	return nil
}

func (k Kind) maxAttempts() int {
	if k.MaxAttempts == 0 {
		return DefaultMaxAttempts
	}
	return k.MaxAttempts
}

// Option sets one of a job's optional fields.
type Option func(*job)

type job struct {
	account     kernel.ULID
	uniqueKey   *string
	orderingKey *string
	priority    int
	runAt       *time.Time
	maxAttempts int
}

// Account names the account whose entitlement the claim checks.
func Account(id kernel.ULID) Option { return func(j *job) { j.account = id } }

// UniqueKey deduplicates the job within its kind by the kind's policy.
func UniqueKey(k string) Option { return func(j *job) { j.uniqueKey = &k } }

// OrderingKey runs the job after every older job with the same key, one at
// a time.
func OrderingKey(k string) Option { return func(j *job) { j.orderingKey = &k } }

// Priority sets the priority, 0 (first) to 9; the default is 5.
func Priority(p int) Option { return func(j *job) { j.priority = p } }

// RunAt makes the job claimable no earlier than t; the default is now.
func RunAt(t time.Time) Option { return func(j *job) { j.runAt = &t } }

// MaxAttempts overrides the kind's attempt limit for this job.
func MaxAttempts(n int) Option { return func(j *job) { j.maxAttempts = n } }

// Queue keeps jobs in the jobs table of db.
type Queue struct {
	db tx.Beginner
	q  tx.Querier
}

// DB is a pool, which both begins transactions and runs statements.
type DB interface {
	tx.Beginner
	tx.Querier
}

// New keeps jobs in db, the primary pool.
func New(db DB) *Queue { return &Queue{db: db, q: db} }

// enqueue inserts a job, or resolves it as a duplicate by the kind's
// policy, and returns the ID of the job that will do the work: the new
// one or Coalesce's existing one, and no row when Ignore dropped it. The
// same statement notifies the queue's workers, so no job is left unannounced
// by a failure between two statements.
const enqueue = `
WITH j AS (
INSERT INTO jobs AS j (queue, kind, payload, account_id, unique_key, ordering_key,
    priority, run_at, state, max_attempts)
VALUES ($1, $2, $3, $4, $5, $6, $7, coalesce($8, now()), 'pending', $9)
ON CONFLICT (kind, unique_key) WHERE state IN ('pending', 'running') AND unique_key IS NOT NULL
DO UPDATE SET
    payload = excluded.payload,
    run_at = CASE WHEN j.state = 'pending' THEN least(j.run_at, excluded.run_at) ELSE j.run_at END,
    rerun = j.rerun OR j.state = 'running'
WHERE $10
RETURNING id)
SELECT id, pg_notify($11, $1)::text FROM j`

// EnqueueTx enqueues a job of kind k inside the transaction on ctx, so the
// job exists exactly when the caller's change commits; without a
// transaction it returns ErrNoTransaction. Payload is encoded as JSON and
// carries IDs, never secrets. The workers of k's queue are notified on
// infraweave_jobs, which PostgreSQL delivers at commit. It returns the ID
// of the job that will do the work, or 0 when Ignore dropped a duplicate.
func (q *Queue) EnqueueTx(ctx context.Context, k Kind, payload any, opts ...Option) (int64, error) {
	if err := k.validate(); err != nil {
		return 0, err
	}
	j := job{priority: DefaultPriority, maxAttempts: k.maxAttempts()}
	for _, o := range opts {
		o(&j)
	}
	if j.priority < 0 || j.priority > 9 || j.maxAttempts < 1 {
		return 0, fmt.Errorf("%w: priority %d, max attempts %d", ErrJob, j.priority, j.maxAttempts)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("%w: encode payload of %s: %w", ErrJob, k.Name, err)
	}
	if len(body) > MaxPayload {
		return 0, fmt.Errorf("%w: %s, %d bytes", ErrPayloadTooLarge, k.Name, len(body))
	}
	if !tx.In(ctx) {
		return 0, ErrNoTransaction
	}
	var (
		id      int64
		discard string
	)
	err = tx.Q(ctx, q.q).QueryRow(ctx, enqueue, k.Queue, k.Name, body, j.account, j.uniqueKey,
		j.orderingKey, j.priority, j.runAt, j.maxAttempts, k.Policy == Coalesce, notify.Jobs).
		Scan(&id, &discard)
	if err != nil && !noRows(err) {
		return 0, fmt.Errorf("enqueue %s: %w", k.Name, sql.Unavailable(err))
	}
	return id, nil
}

// Enqueue is EnqueueTx in a transaction of its own, for a caller with no
// change to commit alongside the job.
func (q *Queue) Enqueue(ctx context.Context, k Kind, payload any, opts ...Option) (int64, error) {
	var id int64
	err := tx.Do(ctx, q.db, tx.Options{}, func(ctx context.Context) error {
		var err error
		id, err = q.EnqueueTx(ctx, k, payload, opts...)
		return err
	})
	if err != nil {
		// EnqueueTx's errors name the kind; a transaction that could not
		// begin is classified here.
		return 0, sql.Unavailable(err) //nolint:wrapcheck // as above
	}
	return id, nil
}
