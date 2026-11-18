package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/app/config"
	eventsassembly "github.com/weaveplatform/infraweave/internal/events/assembly"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	opapi "github.com/weaveplatform/infraweave/internal/platform/gen/operator"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/test/deps"
)

type eventWorld struct {
	holdWorld
	box *outbox.Outbox
}

func newEventWorld(t *testing.T) eventWorld {
	t.Helper()
	w := newHoldWorld(t)
	box := testEvents(t, w.q)
	d, err := newDomains(config.Defaults(), w.db, w.keys, w.blobs.Objects, nil, box, discard())
	if err != nil {
		t.Fatal(err)
	}
	w.d = d
	return eventWorld{holdWorld: w, box: box}
}

// cancel seeds a queued run, then uses the real lifecycle to change its state.
// Direct fixture inserts avoid unrelated source-resolution calls while the
// outbox workers exercise every consumer of the resulting run update.
func (w eventWorld) cancel(t *testing.T, bound bool) outbox.Event {
	t.Helper()
	ctx := t.Context()
	stack, run := kernel.NewULID(), kernel.NewULID()
	slug := "plain"
	var integration *kernel.ULID
	if bound {
		slug = "integrated"
		integration = new(kernel.NewULID())
		if _, err := w.q.Exec(ctx, `INSERT INTO vcs_integrations
			(account_id,id,provider,slug,name,space_id) VALUES ($1,$2,'GITLAB','feedback','Feedback',$1)`,
			w.acct, integration); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.q.Exec(ctx, `INSERT INTO stacks
		(account_id,id,slug,name,space_id,provider,repository,branch,vendor,vcs_integration_id)
		VALUES ($1,$2,$3,$3,$4,'GITLAB','infra','main','terraform',$5)`,
		w.acct, stack, slug, w.kept, integration); err != nil {
		t.Fatal(err)
	}
	if _, err := w.q.Exec(ctx, `INSERT INTO runs
		(account_id,id,stack_id,type,state,commit,triggered_by)
		VALUES ($1,$2,$3,'TRACKED','QUEUED','{"hash":"abc"}','alice')`, w.acct, run, stack); err != nil {
		t.Fatal(err)
	}
	result, err := w.d.Runs.API().RunCancel(w.admin, api.RunCancelRequestObject{StackId: slug, RunId: run.String()})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.(api.RunCancel200JSONResponse).Body.State; got != api.RunStateCANCELED {
		t.Fatalf("real cancellation state = %s", got)
	}
	event := outbox.Event{Account: w.acct.ULID, Kind: runsapi.UpdatedKind}
	if err := w.q.QueryRow(ctx, `SELECT id,schema_version,space,subject_kind,subject_id,sequence,document
		FROM events WHERE account_id=$1 AND kind=$2 AND subject_id=$3`, w.acct, runsapi.UpdatedKind, run.String()).
		Scan(&event.ID, &event.SchemaVersion, &event.Space, &event.Subject.Kind, &event.Subject.ID, &event.Sequence, &event.Document); err != nil {
		t.Fatal(err)
	}
	var updated runsapi.Updated
	if err := json.Unmarshal(event.Document, &updated); err != nil || updated.Run.ID != run || updated.State != kernel.RunStateCanceled {
		t.Fatalf("state change did not emit its run update: %s, %v", event.Document, err)
	}
	return event
}

func (w eventWorld) workers(t *testing.T) {
	t.Helper()
	reg := queue.NewRegistry()
	if err := w.box.Register(reg); err != nil {
		t.Fatal(err)
	}
	cfg := queue.Defaults()
	cfg.PollInterval = 10 * time.Millisecond
	workers := queue.NewWorkers(queue.New(w.q), reg, cfg, queue.AllowAll{}, nil, discard(), "events-app-test")
	ctx, stop := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	wg.Go(func() { workers.Run(ctx) })
	t.Cleanup(func() { stop(); wg.Wait() })
}

// The registered production consumer must finish the actual lifecycle event
// even before provider-specific targets exist, with either binding shape.
func TestAccEventsRunUpdates(t *testing.T) {
	w := newEventWorld(t)
	if err := w.box.Subscribe(runsapi.UpdatedKind, "events.sinks", w.d.Events.Consume); !errors.Is(err, outbox.ErrConsumer) {
		t.Fatalf("all-domain wiring did not subscribe events.sinks: %v", err)
	}
	var keys []string
	for _, bound := range []bool{false, true} {
		event := w.cancel(t, bound)
		keys = append(keys, event.ID.String()+":events.sinks")
	}
	w.workers(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := deps.Eventually(ctx, "both run updates consumed by events.sinks", func(ctx context.Context) (bool, error) {
		var completed int
		err := w.q.QueryRow(ctx, `SELECT count(*) FROM jobs
			WHERE account_id=$1 AND unique_key=ANY($2::text[]) AND kind='events.consume' AND state='succeeded'`, w.acct, keys).
			Scan(&completed)
		return completed == 2, err
	}); err != nil {
		t.Fatal(err)
	}
	var records, feedback int
	if err := w.q.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM event_deliveries WHERE account_id=$1),
		(SELECT count(*) FROM jobs WHERE account_id=$1 AND kind='vcs.report_check')`, w.acct).
		Scan(&records, &feedback); err != nil || records != 0 || feedback != 0 {
		t.Fatalf("unfinished provider targets were produced: records=%d jobs=%d, %v", records, feedback, err)
	}
}

// Only target selection is synthetic; enqueue retains VCS's real account hold
// and persistent queue write, and Consume retains the events transaction.
type eventTargets struct{ vcsapi.Feedback }

func (f eventTargets) Targets(_ context.Context, event outbox.Event) ([]vcsapi.FeedbackTarget, error) {
	payload, err := json.Marshal(map[string]string{"run": event.Subject.ID})
	if err != nil {
		return nil, err
	}
	return []vcsapi.FeedbackTarget{{
		Account: event.Account, EventID: event.ID, Key: event.Subject.ID + "/check", Job: "vcs.report_check",
		Payload: payload,
	}}, nil
}

func (w eventWorld) consume(t *testing.T, event outbox.Event) (func(context.Context) error, queue.Job) {
	t.Helper()
	sinks, err := eventsassembly.New(eventsassembly.Deps{
		DB: w.q, Feedback: eventTargets{w.d.VCS.Feedback()}, Settings: eventsassembly.Defaults(),
	})
	if err != nil {
		t.Fatal(err)
	}
	q := queue.New(w.q)
	if _, err := q.Enqueue(t.Context(), outbox.ConsumeKind, map[string]any{
		"account": event.Account, "id": event.ID, "consumer": "events.sinks",
	}, queue.Account(event.Account)); err != nil {
		t.Fatal(err)
	}
	jobs, err := q.Claim(t.Context(), queue.Events, []string{outbox.ConsumeKind.Name}, 1, time.Minute, "owner-race")
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim sinks consumption: %v, %v", jobs, err)
	}
	return func(ctx context.Context) error { return sinks.Consume(ctx, event, jobs[0]) }, jobs[0]
}

func (w eventWorld) delete(ctx context.Context) error {
	ctx = kernel.WithPrincipal(ctx, kernel.Principal{Kind: kernel.PrincipalOperator})
	_, err := w.d.Access.OperatorAPI().OrganizationDelete(ctx, opapi.OrganizationDeleteRequestObject{Organization: "hold"})
	return err
}

// race waits for PostgreSQL's actual lock contention before releasing the
// first transaction. The timeout also bounds both goroutines on a failure.
func (w eventWorld) race(t *testing.T, first, second func(context.Context) error) (error, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	ready, release := make(chan struct{}), make(chan struct{})
	a, b := make(chan error, 1), make(chan error, 1)
	var once sync.Once
	var wg sync.WaitGroup
	defer func() { once.Do(func() { close(release) }); cancel(); wg.Wait() }()
	wg.Go(func() {
		a <- tx.Do(ctx, w.q, tx.Options{}, func(ctx context.Context) error {
			if err := first(ctx); err != nil {
				return err
			}
			close(ready)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	})
	select {
	case <-ready:
	case err := <-a:
		t.Fatalf("first race participant failed before holding its rows: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	wg.Go(func() { b <- second(ctx) })
	awaitLockWait(ctx, t, w.q)
	once.Do(func() { close(release) })
	return <-a, <-b
}

func TestAccEventsAccountDeletionRace(t *testing.T) {
	for _, consumeFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("consume-first=%t", consumeFirst), func(t *testing.T) {
			w := newEventWorld(t)
			event := w.cancel(t, true)
			consume, job := w.consume(t, event)
			first, second := w.delete, consume
			if consumeFirst {
				first, second = consume, w.delete
			}
			a, b := w.race(t, first, second)
			if a != nil || consumeFirst && b != nil || !consumeFirst && !errors.Is(b, accessapi.ErrNoSpace) {
				t.Fatalf("owner race: first=%v second=%v", a, b)
			}
			var owners, records, feedback int
			var state string
			if err := w.q.QueryRow(t.Context(), `SELECT
				(SELECT count(*) FROM accounts WHERE id=$1),
				(SELECT count(*) FROM event_deliveries WHERE account_id=$1),
				(SELECT count(*) FROM jobs WHERE account_id=$1 AND kind='vcs.report_check'),
				(SELECT state FROM jobs WHERE id=$2)`, w.acct, job.ID).Scan(&owners, &records, &feedback, &state); err != nil {
				t.Fatal(err)
			}
			if owners != 0 {
				t.Fatal("the real account deletion did not commit")
			}
			if consumeFirst {
				if records != 1 || feedback != 1 || state != "succeeded" {
					t.Fatalf("commit before deletion lost its atomic effects: records=%d feedback=%d job=%s", records, feedback, state)
				}
			} else if records != 0 || feedback != 0 || state != "running" {
				t.Fatalf("deletion did not roll back late delivery: records=%d feedback=%d job=%s", records, feedback, state)
			}
			w.workers(t)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			if err := deps.Eventually(ctx, "deleted account's event delivery purge", func(ctx context.Context) (bool, error) {
				var remaining, purged int
				err := w.q.QueryRow(ctx, `SELECT
					(SELECT count(*) FROM event_deliveries WHERE account_id=$1),
					(SELECT count(*) FROM jobs WHERE kind='events.consume' AND payload->>'consumer'='events.purge' AND state='succeeded')`,
					w.acct).Scan(&remaining, &purged)
				return remaining == 0 && purged == 1, err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
