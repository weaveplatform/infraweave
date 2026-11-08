package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// Subject is what an event is about, such as a run.
type Subject struct {
	Kind string
	ID   string
}

// Event is the envelope of every event, shared by Deploy and Flows.
//
// ref: decision 0015
type Event struct {
	Account kernel.ULID
	// ID is assigned by Append when zero.
	ID            kernel.ULID
	Kind          string
	SchemaVersion int
	// Space is the space the event belongs to, when it has one.
	Space   string
	Subject Subject
	// Sequence orders one subject's events: the run's stateVersion, or a
	// per-subject counter.
	Sequence int64
	// OccurredAt is set by Append when zero; it is stored in nanoseconds.
	OccurredAt time.Time
	// Document is the kind's JSON document, checked against its schema.
	Document json.RawMessage
}

// The outbox's job kinds, on the events queue with policy Ignore so a
// duplicate fan-out or delivery request is dropped while one is pending.
var (
	FanoutKind  = queue.Kind{Name: "events.fanout", Queue: queue.Events, Policy: queue.Ignore}
	ConsumeKind = queue.Kind{Name: "events.consume", Queue: queue.Events, Policy: queue.Ignore}
)

// Errors of the emit path and of consumers.
var (
	// ErrNoTransaction reports an Append outside a transaction, whose event
	// could outlive or miss its change.
	ErrNoTransaction = errors.New("event appended outside a transaction")
	// ErrEvent reports an event without an account, a subject or a kind.
	ErrEvent = errors.New("invalid event")
	// ErrConsumer reports a consumer with no name or handler, or one
	// subscribed to a kind twice.
	ErrConsumer = errors.New("invalid event consumer")
	// errGone marks a consume job whose event no longer exists.
	errGone = errors.New("event no longer stored")
)

// Handler consumes one event for one consumer. It runs at least once per
// event, on the events queue; job is its events.consume job, which it may
// complete with its own writes through Queue.CompleteTx.
type Handler func(ctx context.Context, ev Event, job queue.Job) error

// Outbox appends events in their change's transaction and fans them out
// to the consumers subscribed to their kinds.
type Outbox struct {
	db    queue.DB
	queue *queue.Queue
	kinds *Kinds

	mu        sync.RWMutex
	consumers map[string]map[string]Handler
}

// New appends to db's events table, checks documents against kinds and
// enqueues on q.
func New(db queue.DB, q *queue.Queue, kinds *Kinds) *Outbox {
	return &Outbox{db: db, queue: q, kinds: kinds, consumers: map[string]map[string]Handler{}}
}

// Subscribe runs h, as consumer, for every event of kind fanned out after
// it. Every replica subscribes the same consumers, as code constants.
func (o *Outbox) Subscribe(kind, consumer string, h Handler) error {
	if kind == "" || consumer == "" || h == nil {
		return fmt.Errorf("%w: %q on %q", ErrConsumer, consumer, kind)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.consumers[kind] == nil {
		o.consumers[kind] = map[string]Handler{}
	}
	if _, dup := o.consumers[kind][consumer]; dup {
		return fmt.Errorf("%w: %q subscribed to %q twice", ErrConsumer, consumer, kind)
	}
	o.consumers[kind][consumer] = h
	return nil
}

// ref is an event's key, the payload of its fan-out job.
type ref struct {
	Account kernel.ULID `json:"account"`
	ID      kernel.ULID `json:"id"`
}

// consumption is an events.consume job's payload.
type consumption struct {
	ref
	Consumer string `json:"consumer"`
}

const insert = `
INSERT INTO events (account_id, id, kind, schema_version, space, subject_kind, subject_id,
    sequence, occurred_at, document)
VALUES ($1, $2, $3, $4, nullif($5, ''), $6, $7, $8, $9, $10)`

// Append writes ev in the transaction on ctx, with an events.fanout job
// keyed by its ID and a notification on infraweave_events carrying
// <account>:<id>, which PostgreSQL delivers at commit; so the event, its
// fan-out and its notification exist exactly when the change does. It
// returns the event as stored. A kind and version with no schema, or a
// document its schema rejects, is refused before anything is written.
func (o *Outbox) Append(ctx context.Context, ev Event) (Event, error) {
	if ev.Account.IsZero() || ev.Kind == "" || ev.Subject.Kind == "" || ev.Subject.ID == "" {
		return Event{}, fmt.Errorf("%w: account %s, kind %q, subject %s %q",
			ErrEvent, ev.Account, ev.Kind, ev.Subject.Kind, ev.Subject.ID)
	}
	if err := o.kinds.Validate(ev.Kind, ev.SchemaVersion, ev.Document); err != nil {
		return Event{}, err
	}
	if !tx.In(ctx) {
		return Event{}, ErrNoTransaction
	}
	if ev.ID.IsZero() {
		ev.ID = kernel.NewULID()
	}
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = time.Now()
	}
	q := tx.Q(ctx, o.db)
	if _, err := q.Exec(ctx, insert, ev.Account, ev.ID, ev.Kind, ev.SchemaVersion, ev.Space,
		ev.Subject.Kind, ev.Subject.ID, ev.Sequence, ev.OccurredAt.UnixNano(),
		string(ev.Document)); err != nil {
		return Event{}, fmt.Errorf("append %s event: %w", ev.Kind, sql.Unavailable(err))
	}
	if _, err := o.queue.EnqueueTx(ctx, FanoutKind, ref{ev.Account, ev.ID},
		queue.Account(ev.Account), queue.UniqueKey(ev.ID.String())); err != nil {
		return Event{}, fmt.Errorf("append %s event: %w", ev.Kind, err)
	}
	if err := notify.Notify(
		ctx,
		q,
		notify.Events,
		ev.Account.String()+":"+ev.ID.String(),
	); err != nil {
		return Event{}, fmt.Errorf("append %s event: %w", ev.Kind, sql.Unavailable(err))
	}
	return ev, nil
}

// Register adds the outbox's job handlers to reg: events.fanout and
// events.consume, which the scheduler runs.
func (o *Outbox) Register(reg *queue.Registry) error {
	for k, h := range map[queue.Kind]queue.Handler{FanoutKind: o.fanout, ConsumeKind: o.consume} {
		if err := reg.Register(k, h, queue.HandlerOptions{}); err != nil {
			return fmt.Errorf("outbox: %w", err)
		}
	}
	return nil
}

// fanout enqueues one events.consume job per consumer of the event's kind
// that has none yet, records each in event_consumptions, marks the event
// fanned out and completes its own job, all in one transaction; a fan-out
// that runs twice therefore enqueues no consumer twice.
func (o *Outbox) fanout(ctx context.Context, job queue.Job) error {
	var r ref
	if err := json.Unmarshal(job.Payload, &r); err != nil {
		return permanent(fmt.Errorf("events.fanout payload: %w", err))
	}
	//nolint:wrapcheck // each step names the event
	return tx.Do(
		ctx,
		o.db,
		tx.Options{},
		func(ctx context.Context) error {
			q := tx.Q(ctx, o.db)
			var kind string
			err := q.QueryRow(ctx, `SELECT kind FROM events WHERE account_id = $1 AND id = $2 FOR UPDATE`,
				r.Account, r.ID).
				Scan(&kind)
			if errors.Is(err, pgx.ErrNoRows) {
				return permanent(fmt.Errorf("fan out %s: %w", r.ID, errGone))
			}
			if err != nil {
				return fmt.Errorf("fan out %s: %w", r.ID, sql.Unavailable(err))
			}
			for _, c := range o.subscribers(kind) {
				tag, err := q.Exec(
					ctx,
					`INSERT INTO event_consumptions (account_id, event_id, consumer)
VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
					r.Account,
					r.ID,
					c,
				)
				if err != nil {
					return fmt.Errorf("fan out %s to %s: %w", r.ID, c, sql.Unavailable(err))
				}
				if tag.RowsAffected() == 0 {
					continue
				}
				if _, err := o.queue.EnqueueTx(
					ctx,
					ConsumeKind,
					consumption{r, c},
					queue.Account(r.Account),
					queue.UniqueKey(r.ID.String()+":"+c),
				); err != nil {
					return fmt.Errorf("fan out %s to %s: %w", r.ID, c, err)
				}
			}
			if _, err := q.Exec(
				ctx,
				`UPDATE events SET fanned_out_at = coalesce(fanned_out_at, now())
WHERE account_id = $1 AND id = $2`,
				r.Account,
				r.ID,
			); err != nil {
				return fmt.Errorf("fan out %s: %w", r.ID, sql.Unavailable(err))
			}
			return o.queue.CompleteTx(ctx, job)
		},
	)
}

// permanent makes a job dead at once: its event or payload cannot be read
// on any retry.
func permanent(err error) error {
	return queue.Permanent(err) //nolint:wrapcheck // marks the error it is given
}

func (o *Outbox) subscribers(kind string) []string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	names := make([]string, 0, len(o.consumers[kind]))
	for c := range o.consumers[kind] {
		names = append(names, c)
	}
	slices.Sort(names)
	return names
}

func (o *Outbox) handler(kind, consumer string) Handler {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.consumers[kind][consumer]
}

// consume reads the event and runs its consumer's handler. A consumer
// this replica does not subscribe fails the attempt, so a replica that
// knows it takes the retry.
func (o *Outbox) consume(ctx context.Context, job queue.Job) error {
	var c consumption
	if err := json.Unmarshal(job.Payload, &c); err != nil {
		return permanent(fmt.Errorf("events.consume payload: %w", err))
	}
	ev, err := o.read(ctx, c.ref)
	if err != nil {
		return err
	}
	h := o.handler(ev.Kind, c.Consumer)
	if h == nil {
		return fmt.Errorf("%w: %q is not subscribed to %q here", ErrConsumer, c.Consumer, ev.Kind)
	}
	return h(ctx, ev, job)
}

func (o *Outbox) read(ctx context.Context, r ref) (Event, error) {
	var (
		ev    Event
		space *string
		nanos int64
	)
	err := tx.Q(ctx, o.db).QueryRow(ctx, `
SELECT account_id, id, kind, schema_version, space, subject_kind, subject_id, sequence,
    occurred_at, document
FROM events WHERE account_id = $1 AND id = $2`, r.Account, r.ID).Scan(&ev.Account, &ev.ID,
		&ev.Kind, &ev.SchemaVersion, &space, &ev.Subject.Kind, &ev.Subject.ID, &ev.Sequence, &nanos,
		&ev.Document)
	if errors.Is(err, pgx.ErrNoRows) {
		return Event{}, permanent(fmt.Errorf("read event %s: %w", r.ID, errGone))
	}
	if err != nil {
		return Event{}, fmt.Errorf("read event %s: %w", r.ID, sql.Unavailable(err))
	}
	if space != nil {
		ev.Space = *space
	}
	ev.OccurredAt = time.Unix(0, nanos)
	return ev, nil
}

// Prune deletes events fanned out more than retention ago, with their
// consumption records, and returns how many events it deleted; the
// scheduler's events.prune task runs it. An event not yet fanned out is
// never deleted, so every consumer still receives it.
//
// ref: decision 0015
func (o *Outbox) Prune(ctx context.Context, retention time.Duration) (int64, error) {
	var n int64
	err := tx.Q(ctx, o.db).QueryRow(ctx, `
WITH old AS (
    SELECT account_id, id FROM events
    WHERE fanned_out_at < now() - $1 * interval '1 microsecond'),
consumed AS (
    DELETE FROM event_consumptions c USING old
    WHERE c.account_id = old.account_id AND c.event_id = old.id),
gone AS (
    DELETE FROM events e USING old
    WHERE e.account_id = old.account_id AND e.id = old.id
    RETURNING 1)
SELECT count(*) FROM gone`, retention.Microseconds()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("prune events: %w", sql.Unavailable(err))
	}
	return n, nil
}
