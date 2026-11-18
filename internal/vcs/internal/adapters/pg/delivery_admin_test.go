package pg_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/push"
	"github.com/weaveplatform/infraweave/test/deps"
)

type refusedReplayAudit struct {
	intake.AdminStore
	err error
}

func (s refusedReplayAudit) Audit(context.Context, kernel.AuditEntry) error { return s.err }

func (w *webhookWorld) terminal(t *testing.T, name string) intake.Delivery {
	t.Helper()
	w.receive(t, name, webhookBody(128))
	j := w.claim(t, w.queue, "original")
	if err := w.queue.Kill(t.Context(), j, errors.New("unsafe raw payload diagnostic must never be public")); err != nil {
		t.Fatal(err)
	}
	return w.delivery(t, name)
}

func replayConflict(t *testing.T, err error) {
	t.Helper()
	var refused *intake.ReplayConflict
	if !errors.As(err, &refused) {
		t.Fatal("expected replay conflict", err)
	}
}

func TestAccDeliveryReplayAtomicity(t *testing.T) {
	for _, fault := range []string{"none", "audit", "enqueue"} {
		t.Run(fault, func(t *testing.T) {
			w := newWebhookWorld(t)
			d := w.terminal(t, "replay")
			if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
				return w.f.store.PutPrepared(ctx, w.f.acct, d.ID, []push.Prepared{{}})
			}); err != nil {
				t.Fatal(err)
			}
			deps := w.deps
			cause := errors.New("rollback replay")
			if fault == "audit" {
				deps.Admin = refusedReplayAudit{AdminStore: w.store, err: cause}
			}
			if fault == "enqueue" {
				deps.Jobs = failedEnqueue{Queue: w.queue, err: cause}
			}
			out, err := intake.New(deps).Replay(t.Context(), w.f.admin, w.id, d.ID, "192.0.2.41")
			kept := w.delivery(t, "replay")
			var jobs, audits int
			if err := w.f.db.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM jobs WHERE account_id=$1 AND kind=$2 AND state='pending'),(SELECT count(*) FROM audit_trail_entries WHERE account_id=$1 AND action='vcs_integration.replay_delivery')`, w.f.acct, intake.Process.Name).
				Scan(&jobs, &audits); err != nil {
				t.Fatal(err)
			}
			prepared, readErr := w.f.store.Prepared(t.Context(), w.f.acct, d.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if fault != "none" {
				if !errors.Is(err, cause) || kept.Replays != 0 || jobs != 0 || audits != 0 || len(prepared) != 1 {
					t.Fatal("replay rollback leaked state, job, audit or cleared facts", err, kept.Replays, jobs, audits, len(prepared))
				}
				return
			}
			if err != nil || out.Replays != 1 || kept.Replays != 1 || kept.State != intake.Received || kept.LastError != "" || kept.RunsCreated != 0 ||
				!kept.Deadline.After(d.Deadline) ||
				kept.ReceivedAt != d.ReceivedAt ||
				jobs != 1 ||
				audits != 1 ||
				len(prepared) != 0 {
				t.Fatal("replay transaction incomplete", out, err, kept, jobs, audits, len(prepared))
			}
			var actor, resource, ip string
			if err := w.f.db.QueryRow(t.Context(), `SELECT actor_username,affected_id,remote_ip FROM audit_trail_entries WHERE account_id=$1 AND action='vcs_integration.replay_delivery'`, w.f.acct).
				Scan(&actor, &resource, &ip); err != nil ||
				actor != w.f.admin.Login ||
				resource != w.id.String() ||
				ip != "192.0.2.41" {
				t.Fatal("replay audit lost caller", actor, resource, ip, err)
			}
			j := w.claim(t, w.queue, "replayed")
			if err := w.svc.Processed(t.Context(), j); err != nil {
				t.Fatal(err)
			}
			if w.delivery(t, "replay").State != intake.Dispatched {
				t.Fatal("replay did not process")
			}
		})
	}
}

type failedEnqueue struct {
	*queue.Queue
	err error
}

func (q failedEnqueue) EnqueueTx(context.Context, queue.Kind, any, ...queue.Option) (int64, error) {
	return 0, q.err
}

func TestAccDeliveryListingAndGeneration(t *testing.T) {
	w := newWebhookWorld(t)
	first := w.terminal(t, "first")
	second := w.terminal(t, "second")
	// Equal receipt times exercise the deterministic ID tie-breaker.
	at := time.Now().Add(-time.Minute)
	if _, err := w.f.db.Exec(t.Context(), `UPDATE vcs_deliveries SET received_at=$2 WHERE account_id=$1`, w.f.acct, at); err != nil {
		t.Fatal(err)
	}
	page, err := w.svc.List(t.Context(), w.f.acct, w.id, search.Input{First: new(int32(1))})
	if err != nil || len(page.Edges) != 1 || !page.HasNext || page.Edges[0].Node.ID != second.ID || page.Edges[0].Node.State != intake.Failed ||
		page.Edges[0].Node.LastError != intake.ProcessingFailed {
		t.Fatal("newest failed delivery page incorrect", page, err)
	}
	next, err := w.svc.List(t.Context(), w.f.acct, w.id, search.Input{First: new(int32(1)), After: page.End})
	if err != nil || len(next.Edges) != 1 || next.Edges[0].Node.ID != first.ID || !next.HasPrevious || next.HasNext {
		t.Fatal("cursor did not page", next, err)
	}
	previous, err := w.svc.List(t.Context(), w.f.acct, w.id, search.Input{First: new(int32(1)), After: next.Start, Page: search.Previous})
	if err != nil || len(previous.Edges) != 1 || previous.Edges[0].Node.ID != second.ID {
		t.Fatal("previous search page incorrect", previous, err)
	}
	if _, err := w.svc.List(t.Context(), w.f.acct, w.id, search.Input{After: "broken"}); err == nil {
		t.Fatal("invalid cursor accepted")
	}
	if _, err := w.svc.List(t.Context(), kernel.NewAccountID(), w.id, search.Input{}); !errors.Is(err, intake.ErrNotFound) {
		t.Fatal("cross-account list exposed metadata", err)
	}
	if _, err := w.store.Owned(t.Context(), w.f.acct, kernel.NewULID(), first.ID, time.Time{}, false); !errors.Is(err, intake.ErrNotFound) {
		t.Fatal("cross-parent delivery read", err)
	}
	if _, err := w.svc.Replay(t.Context(), w.f.admin, w.id, first.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := w.store.State(t.Context(), w.f.acct, first.ID, 0, intake.Failed, "old error"); err != nil {
		t.Fatal(err)
	}
	if w.delivery(t, "first").State != intake.Received {
		t.Fatal("old state writer changed replay")
	}
	j := w.claim(t, w.queue, "new-generation")
	if err := w.svc.Processed(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	// The old dead job is still retained. Only generation1 contributes state.
	page, err = w.svc.List(t.Context(), w.f.acct, w.id, search.Input{})
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range page.Edges {
		if edge.Node.ID == first.ID && edge.Node.State != intake.Dispatched {
			t.Fatal("old dead generation poisoned replay", edge.Node)
		}
	}

	// Re-enqueue the legacy key through the real operator retry path.
	var oldID int64
	if err := w.f.db.QueryRow(t.Context(), `SELECT id FROM jobs WHERE account_id=$1 AND kind=$2 AND unique_key=$3`, w.f.acct, intake.Process.Name, first.ID.String()).
		Scan(&oldID); err != nil {
		t.Fatal(err)
	}
	if err := w.queue.Retry(t.Context(), oldID); err != nil {
		t.Fatal(err)
	}
	old := w.claim(t, w.queue, "old-generation")
	if err := w.svc.Processed(t.Context(), old); err != nil {
		t.Fatal(err)
	}
	if w.delivery(t, "first").State != intake.Dispatched || w.delivery(t, "first").Replays != 1 {
		t.Fatal("retried old parent mutated new generation")
	}

	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		if err := w.f.store.EvaluationFailed(ctx, w.f.acct, first.ID, 0); err != nil {
			return err
		}
		ok, err := w.f.store.ReserveRun(ctx, w.f.acct, first.ID, 0, 1)
		if ok {
			t.Fatal("old child consumed replay budget")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if w.delivery(t, "first").LastError != "" || w.delivery(t, "first").RunsCreated != 0 {
		t.Fatal("old child changed replay metadata")
	}
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error { return w.f.store.EvaluationFailed(ctx, w.f.acct, first.ID, 1) }); err != nil {
		t.Fatal(err)
	}
	if got := w.delivery(t, "first"); got.State != intake.Dispatched || got.LastError != intake.ProcessingFailed {
		t.Fatal("current terminal child marker missing", got)
	}
}

func TestAccReplayLocksActiveParentAndChild(t *testing.T) {
	for _, kind := range []queue.Kind{intake.Process, push.Evaluate} {
		t.Run(kind.Name, func(t *testing.T) {
			w := newWebhookWorld(t)
			w.deps.RelatedKinds = []queue.Kind{push.Evaluate}
			w.svc = intake.New(w.deps)
			w.receive(t, "active", webhookBody(128))
			d := w.delivery(t, "active")
			var job queue.Job
			if kind.Name == intake.Process.Name {
				job = w.claim(t, w.queue, "active-parent")
			} else {
				parent := w.claim(t, w.queue, "parent")
				if err := w.svc.Processed(t.Context(), parent); err != nil {
					t.Fatal(err)
				}
				if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
					_, err := w.queue.EnqueueTx(
						ctx,
						push.Evaluate,
						push.Evaluation{Account: w.f.acct, Delivery: d.ID, Stack: kernel.NewULID(), Deadline: d.Deadline},
						queue.Account(w.f.acct.ULID),
						queue.UniqueKey(intake.EvaluationKey(d.ID, 0, kernel.NewULID())),
					)
					return err
				}); err != nil {
					t.Fatal(err)
				}
				jobs, err := w.queue.Claim(t.Context(), queue.VCSInbox, []string{push.Evaluate.Name}, 1, time.Minute, "active-child")
				if err != nil || len(jobs) != 1 {
					t.Fatal(err, jobs)
				}
				job = jobs[0]
			}
			connection, err := w.f.db.Acquire(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Release()
			held, release, ownerDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				ownerDone <- tx.Do(t.Context(), connection, tx.Options{}, func(ctx context.Context) error {
					if err := w.queue.HoldTx(ctx, job); err != nil {
						return err
					}
					close(held)
					<-release
					return nil
				})
			}()
			<-held
			result := make(chan error, 1)
			go func() { _, err := w.svc.Replay(t.Context(), w.f.admin, w.id, d.ID, ""); result <- err }()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			waitErr := deps.Eventually(ctx, "replay waits for actual queue-row owner", func(ctx context.Context) (bool, error) {
				var waiting bool
				err := w.f.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, connection.Conn().PgConn().PID()).Scan(&waiting)
				return waiting, err
			})
			close(release)
			if err := <-ownerDone; err != nil {
				t.Fatal(err)
			}
			replayErr := <-result
			if waitErr != nil {
				t.Fatal(waitErr)
			}
			replayConflict(t, replayErr)
			if got := w.delivery(t, "active"); got.Replays != 0 {
				t.Fatal("active work reset", got)
			}
		})
	}
}

func TestAccDeliveryChildFailurePreservesSibling(t *testing.T) {
	w := newWebhookWorld(t)
	w.deps.RelatedKinds = []queue.Kind{push.Evaluate}
	w.svc = intake.New(w.deps)
	w.receive(t, "siblings", webhookBody(128))
	parent := w.claim(t, w.queue, "parent")
	if err := w.svc.Processed(t.Context(), parent); err != nil {
		t.Fatal(err)
	}
	d := w.delivery(t, "siblings")
	for range 2 {
		stack := kernel.NewULID()
		if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
			_, err := w.queue.EnqueueTx(
				ctx,
				push.Evaluate,
				push.Evaluation{Account: w.f.acct, Delivery: d.ID, Stack: stack, Deadline: d.Deadline},
				queue.Account(w.f.acct.ULID),
				queue.UniqueKey(intake.EvaluationKey(d.ID, 0, stack)),
			)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := w.queue.Claim(t.Context(), queue.VCSInbox, []string{push.Evaluate.Name}, 2, time.Minute, "siblings")
	if err != nil || len(jobs) != 2 {
		t.Fatal(err, jobs)
	}
	if err := w.queue.Kill(t.Context(), jobs[0], errors.New("unsafe secret payload diagnostic")); err != nil {
		t.Fatal(err)
	}
	page, err := w.svc.List(t.Context(), w.f.acct, w.id, search.Input{})
	if err != nil || page.Edges[0].Node.State != intake.Failed || page.Edges[0].Node.LastError != intake.ProcessingFailed {
		t.Fatal("failed child not projected safely", page, err)
	}
	if _, err := w.svc.Replay(t.Context(), w.f.admin, w.id, d.ID, ""); err == nil {
		t.Fatal("live sibling allowed replay")
	} else {
		replayConflict(t, err)
	}
	if err := w.svc.Reconciled(t.Context(), queue.Job{}); err == nil {
		t.Fatal("unclaimed reconciler completion accepted")
	}
	// Use a real reconciliation claim, so marker+job completion commit together.
	if _, err := w.queue.Enqueue(t.Context(), intake.Reconcile, struct{}{}); err != nil {
		t.Fatal(err)
	}
	var recon queue.Job
	reconJobs, err := w.queue.Claim(t.Context(), queue.Maintenance, []string{intake.Reconcile.Name}, 1, time.Minute, "reconcile")
	if err != nil || len(reconJobs) != 1 {
		t.Fatal(err, reconJobs)
	}
	recon = reconJobs[0]
	if err := w.svc.Reconciled(t.Context(), recon); err != nil {
		t.Fatal(err)
	}
	kept := w.delivery(t, "siblings")
	if kept.State != intake.Dispatched || kept.LastError != intake.ProcessingFailed {
		t.Fatal("failed child stopped active sibling", kept)
	}
	if err := w.queue.Complete(t.Context(), jobs[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.Replay(t.Context(), w.f.admin, w.id, d.ID, ""); err != nil {
		t.Fatal(err)
	}
	kept = w.delivery(t, "siblings")
	if kept.Replays != 1 || kept.LastError != "" {
		t.Fatal("replay did not clear sticky generation failure", kept)
	}
}

func TestAccReplayExpiredBlobAndRotatedSignature(t *testing.T) {
	for _, fault := range []string{"expired", "rotated", "previous", "inline-old", "external-valid"} {
		t.Run(fault, func(t *testing.T) {
			w := newWebhookWorld(t)
			size := 128
			if fault == "expired" || fault == "external-valid" {
				size = intake.InlineBody + 1
			}
			w.receive(t, "body", webhookBody(size))
			job := w.claim(t, w.queue, "terminal")
			if err := w.queue.Kill(t.Context(), job, errors.New("failed")); err != nil {
				t.Fatal(err)
			}
			d := w.delivery(t, "body")
			switch fault {
			case "external-valid":
				if d.BodyKey == "" {
					t.Fatal("large body did not use inbox storage")
				}
			case "expired":
				if _, err := w.f.db.Exec(t.Context(), `UPDATE blob_objects SET expires_at=now()-interval '1 second' WHERE store='inbox' AND key=$1`, d.BodyKey); err != nil {
					t.Fatal(err)
				}
			case "inline-old":
				if _, err := w.f.db.Exec(
					t.Context(),
					`UPDATE vcs_deliveries SET received_at=now()-interval '3 days',deadline=now()-interval '3 days' WHERE account_id=$1 AND id=$2`,
					w.f.acct,
					d.ID,
				); err != nil {
					t.Fatal(err)
				}
			default:
				current := []byte(strings.Repeat("new", 16))
				in, err := w.f.store.Get(t.Context(), w.f.acct, w.id, false)
				if err != nil {
					t.Fatal(err)
				}
				until := time.Now().Add(time.Hour)
				if fault == "rotated" {
					until = time.Now().Add(-time.Second)
				}
				if err := w.f.store.RotateWebhookSecret(t.Context(), in, current, until); err != nil {
					t.Fatal(err)
				}
			}
			_, err := w.svc.Replay(t.Context(), w.f.admin, w.id, d.ID, "")
			if fault == "previous" || fault == "inline-old" || fault == "external-valid" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			replayConflict(t, err)
			if w.delivery(t, "body").Replays != 0 {
				t.Fatal("refused body/signature changed replay generation")
			}
		})
	}
}

func TestAccConcurrentDeliveryReplay(t *testing.T) {
	w := newWebhookWorld(t)
	d := w.terminal(t, "concurrent")
	start := make(chan struct{})
	result := make(chan error, 2)
	for range 2 {
		go func() { <-start; _, err := w.svc.Replay(t.Context(), w.f.admin, w.id, d.ID, ""); result <- err }()
	}
	close(start)
	first, second := <-result, <-result
	if first == nil && second == nil || first != nil && second != nil {
		t.Fatal("concurrent replay did not serialize to one request", first, second)
	}
	if first != nil {
		replayConflict(t, first)
	}
	if second != nil {
		replayConflict(t, second)
	}
	if w.delivery(t, "concurrent").Replays != 1 {
		t.Fatal("concurrent replay reset twice")
	}
}

func TestAccDeliveryStoreBoundaries(t *testing.T) {
	w := newWebhookWorld(t)
	d := w.terminal(t, "boundaries")
	if _, err := w.store.Owned(t.Context(), w.f.acct, w.id, d.ID, time.Time{}, true); !errors.Is(err, queue.ErrNoTransaction) {
		t.Fatal("locked read without transaction", err)
	}
	if _, err := w.store.Reset(t.Context(), d, time.Now()); !errors.Is(err, queue.ErrNoTransaction) {
		t.Fatal("reset without transaction", err)
	}
	if _, err := w.store.Hold(t.Context(), intake.Ref{Account: w.f.acct, ID: d.ID}); !errors.Is(err, queue.ErrNoTransaction) {
		t.Fatal("generation hold without transaction", err)
	}
	if _, err := w.store.Owned(t.Context(), w.f.acct, w.id, d.ID, time.Now().Add(time.Hour), false); !errors.Is(err, intake.ErrNotFound) {
		t.Fatal("expired row visible", err)
	}
	missing := d
	missing.ID = kernel.NewULID()
	err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		if held, err := w.store.Hold(ctx, intake.Ref{Account: w.f.acct, ID: missing.ID, State: intake.Received}); err != nil || held {
			t.Fatal("missing generation held", held, err)
		}
		_, err := w.store.Reset(ctx, missing, time.Now())
		return err
	})
	replayConflict(t, err)
	w.f.db.Close()
	for name, err := range map[string]error{
		"owned": func() error {
			_, err := w.store.Owned(t.Context(), w.f.acct, w.id, d.ID, time.Time{}, false)
			return err
		}(),
		"list": func() error {
			_, err := w.store.List(t.Context(), w.f.acct, w.id, time.Time{}, search.Input{})
			return err
		}(),
		"failure": w.store.Failure(t.Context(), intake.Ref{Account: w.f.acct, ID: d.ID}),
	} {
		if err == nil || errors.Is(err, intake.ErrNotFound) {
			t.Fatal(name, "masked unavailable dependency", err)
		}
	}
}
