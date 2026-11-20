package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policiesapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
)

func policyReceipt(w *world, policy kernel.ULID) policiesapi.Receipt {
	return policiesapi.Receipt{
		ID: kernel.NewULID(), Policy: policy, PolicyName: "Approval guard", PolicyVersion: 1,
		Type: policiesapi.Approval, Engine: "REGO_V1", Outcome: policiesapi.Approve,
		Decision: json.RawMessage(`{"approve":true,"flag":["reviewed"],"reset_flag":[]}`),
		Flags:    []string{"reviewed"}, ResetFlags: []string{}, Sample: true, CreatedAt: w.now,
	}
}

func TestAccTransitionCommitsItsPolicyEvidence(t *testing.T) {
	w := newWorld(t)
	run, _, err := w.create(t, kernel.RunTypeTracked, "policy-evidence")
	if err != nil {
		t.Fatal(err)
	}
	receipt := policyReceipt(w, kernel.NewULID())
	receipt.Decision = json.RawMessage(`{"approve":true,"private_input_marker":"keep-out-of-run-projections","exact":9007199254740993}`)
	err = w.in(t, func(ctx context.Context) error {
		_, err := w.lc.Transition(ctx, lifecycle.Request{
			Account: w.acct, Run: run.ID, Version: run.Version,
			To: kernel.RunStateReady, Driver: machine.DriverScheduler,
			Checked: []machine.Guard{
				machine.GuardStackLockFree, machine.GuardApproved,
				machine.GuardUpstreamFinished, machine.GuardExternalSettled, machine.GuardStackEnabled,
			},
			Receipts: []policiesapi.Receipt{receipt},
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var receipts int
	if err := w.db.QueryRow(t.Context(), `SELECT count(*) FROM run_policy_receipts WHERE account_id=$1 AND run_id=$2`, w.acct, run.ID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if receipts != 1 {
		stored, err := w.store.Get(t.Context(), w.acct, run.ID)
		t.Fatalf("transition committed as %s/%d without its receipt: count=%d error=%v", stored.State, stored.Version, receipts, err)
	}
	snapshot, err := w.store.PolicyEvidence(t.Context(), w.acct, []kernel.ULID{run.ID})
	if err != nil || len(snapshot[run.ID].Receipts) != 1 || !reflect.DeepEqual(snapshot[run.ID].Flags, []string{"reviewed"}) {
		t.Fatalf("committed evidence: %+v %v", snapshot, err)
	}
	metadata := snapshot[run.ID].Receipts[0]
	if !metadata.SampleRequested || metadata.SampleEvaluation != nil || metadata.StateVersion != 2 || metadata.PolicyName != receipt.PolicyName {
		t.Fatalf("sample request was confused with a stored sample: %+v", metadata)
	}
	updates := w.updates(t, run.ID)
	if len(updates) != 2 || len(updates[1].Doc.PolicyReceipts) != 1 || !reflect.DeepEqual(updates[1].Doc.Run.Flags, []string{"reviewed"}) {
		t.Fatalf("event omitted committed evidence: %+v", updates)
	}
	var decision string
	if err := w.db.QueryRow(t.Context(), `SELECT decision::text FROM run_policy_receipts WHERE account_id=$1 AND run_id=$2 AND id=$3`, w.acct, run.ID, receipt.ID).
		Scan(&decision); err != nil ||
		!strings.Contains(decision, "9007199254740993") {
		t.Fatalf("stored decision lost number precision: %s %v", decision, err)
	}
	public, err := json.Marshal(updates[1].Doc)
	if err != nil || strings.Contains(string(public), "keep-out-of-run-projections") || strings.Contains(string(public), `"decision":`) {
		t.Fatalf("raw evaluation material entered the event: %s %v", public, err)
	}
}

func readyWithReceipt(w *world, run lifecycle.Run, receipt policiesapi.Receipt) lifecycle.Request {
	return lifecycle.Request{
		Account: w.acct, Run: run.ID, Version: run.Version,
		To: kernel.RunStateReady, Driver: machine.DriverScheduler,
		Checked: []machine.Guard{
			machine.GuardStackLockFree, machine.GuardApproved,
			machine.GuardUpstreamFinished, machine.GuardExternalSettled, machine.GuardStackEnabled,
		},
		Receipts: []policiesapi.Receipt{receipt},
	}
}

func TestAccPolicyFlagOwnershipAndResetOrder(t *testing.T) {
	w := newWorld(t)
	run := mustCreate(t, w, kernel.RunTypeTracked, "owners")
	a, b := kernel.NewULID(), kernel.NewULID()
	write := func(policy kernel.ULID, add, reset []string) {
		t.Helper()
		r := policyReceipt(w, policy)
		r.Flags, r.ResetFlags = add, reset
		if err := w.in(t, func(ctx context.Context) error {
			return w.store.RecordPolicyEvidence(ctx, w.acct, run.ID, run.Version, []policiesapi.Receipt{r})
		}); err != nil {
			t.Fatal(err)
		}
	}
	flags := func(want []string) {
		t.Helper()
		snapshots, err := w.store.PolicyEvidence(t.Context(), w.acct, []kernel.ULID{run.ID})
		if err != nil || !reflect.DeepEqual(snapshots[run.ID].Flags, want) {
			t.Fatalf("visible ownership union: %+v, want %v, error %v", snapshots[run.ID].Flags, want, err)
		}
	}
	write(a, []string{"shared", "a"}, nil)
	write(b, []string{"shared", "b"}, nil)
	flags([]string{"a", "b", "shared"})
	write(a, []string{"shared", "a"}, []string{"shared", "a", "b"})
	flags([]string{"a", "b", "shared"})
	write(a, nil, []string{"shared", "a"})
	flags([]string{"b", "shared"})
	write(b, nil, []string{"shared"})
	flags([]string{"b"})
	other, err := w.store.PolicyEvidence(t.Context(), kernel.NewAccountID(), []kernel.ULID{run.ID})
	if err != nil || len(other) != 0 {
		t.Fatalf("foreign account read evidence: %+v %v", other, err)
	}
	next := mustCreate(t, w, kernel.RunTypeTracked, "next")
	newSnapshot, err := w.store.PolicyEvidence(t.Context(), w.acct, []kernel.ULID{next.ID})
	if err != nil || len(newSnapshot[next.ID].Flags) != 0 || len(newSnapshot[next.ID].Receipts) != 0 {
		t.Fatalf("new run inherited evidence ownership: %+v %v", newSnapshot, err)
	}
}

func TestAccPolicyEvidenceRollsBackWithTransition(t *testing.T) {
	for _, phase := range []string{"event append", "caller failure", "duplicate receipt"} {
		t.Run(phase, func(t *testing.T) {
			w := newWorld(t)
			run := mustCreate(t, w, kernel.RunTypeTracked, "rollback")
			r := policyReceipt(w, kernel.NewULID())
			req := readyWithReceipt(w, run, r)
			lc := w.lc
			if phase == "event append" {
				lc = w.lifecycle(w.store, failing{events: w.events, fail: "Append"}, w.jobs)
			}
			if phase == "duplicate receipt" {
				req.Receipts = append(req.Receipts, r)
			}
			err := w.in(t, func(ctx context.Context) error {
				if _, err := lc.Transition(ctx, req); err != nil {
					return err
				}
				return errInjected
			})
			if err == nil {
				t.Fatal("failure committed")
			}
			assertNoPolicyTransition(t, w, run)
		})
	}
}

func assertNoPolicyTransition(t *testing.T, w *world, run lifecycle.Run) {
	t.Helper()
	stored, err := w.store.Get(t.Context(), w.acct, run.ID)
	if err != nil || stored.State != run.State || stored.Version != run.Version {
		t.Fatalf("rolled-back state escaped: %+v %v", stored, err)
	}
	snapshots, err := w.store.PolicyEvidence(t.Context(), w.acct, []kernel.ULID{run.ID})
	if err != nil || len(snapshots[run.ID].Receipts) != 0 || len(snapshots[run.ID].Flags) != 0 || len(w.updates(t, run.ID)) != 1 || len(w.steps(t, run.ID)) != 1 {
		t.Fatalf("rolled-back receipt, flag, event or history escaped: %+v %v", snapshots, err)
	}
}

// Disconnect the actual PostgreSQL backend at the commit boundary. The test
// observer uses another connection, so the transaction cannot acknowledge a
// state or receipt that PostgreSQL rolled back when its client disappeared.
type disconnectAtCommit struct {
	db         *pgxpool.Pool
	terminated *bool
}

func (d disconnectAtCommit) BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	tx, err := d.db.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return disconnectedTransaction{Tx: tx, observer: d.db, terminated: d.terminated}, nil
}

type disconnectedTransaction struct {
	pgx.Tx
	observer   *pgxpool.Pool
	terminated *bool
}

func (d disconnectedTransaction) Commit(ctx context.Context) error {
	// A nonzero timeout waits for termination, not merely signal delivery:
	// https://www.postgresql.org/docs/18/functions-admin.html#FUNCTIONS-ADMIN-SIGNAL
	if err := d.observer.QueryRow(ctx, `SELECT pg_terminate_backend($1::integer,5000)`, int64(d.Conn().PgConn().PID())).Scan(d.terminated); err != nil {
		return err
	}
	return d.Tx.Commit(ctx)
}

func TestAccPolicyEvidenceSurvivesCommitDisconnect(t *testing.T) {
	w := newWorld(t)
	run := mustCreate(t, w, kernel.RunTypeTracked, "disconnect")
	req := readyWithReceipt(w, run, policyReceipt(w, kernel.NewULID()))
	terminated := false
	err := tx.Do(t.Context(), disconnectAtCommit{db: w.db, terminated: &terminated}, tx.Options{}, func(ctx context.Context) error {
		_, err := w.lc.Transition(ctx, req)
		return err
	})
	if err == nil || !terminated {
		t.Fatal("terminated PostgreSQL backend acknowledged a commit")
	}
	assertNoPolicyTransition(t, w, run)
}

func TestAccPolicyEvidenceAndQueueCompletionShareTheFence(t *testing.T) {
	w := newWorld(t)
	run := mustCreate(t, w, kernel.RunTypeTracked, "lease")
	req := readyWithReceipt(w, run, policyReceipt(w, kernel.NewULID()))
	kind := queue.Kind{Name: "test.policy-evidence", Queue: queue.Default, Policy: queue.Ignore}
	id, err := w.jobs.Enqueue(t.Context(), kind, lifecycle.Schedule{Stack: w.stack}, queue.Account(w.acct.ULID))
	if err != nil {
		t.Fatal(err)
	}
	claim := func(owner string) queue.Job {
		t.Helper()
		jobs, err := w.jobs.Claim(t.Context(), queue.Default, []string{kind.Name}, 1, time.Minute, owner)
		if err != nil || len(jobs) != 1 {
			t.Fatalf("claim %s: %+v %v", owner, jobs, err)
		}
		return jobs[0]
	}
	old := claim("server-a")
	if _, err := w.db.Exec(t.Context(), `UPDATE jobs SET lease_until=now()-interval '1 minute' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := w.jobs.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	current := claim("server-b")
	apply := func(job queue.Job) error {
		return w.in(t, func(ctx context.Context) error {
			if _, err := w.lc.Transition(ctx, req); err != nil {
				return err
			}
			return w.jobs.CompleteTx(ctx, job)
		})
	}
	if err := apply(old); !errors.Is(err, queue.ErrLeaseLost) {
		t.Fatalf("stale job accepted: %v", err)
	}
	assertNoPolicyTransition(t, w, run)
	if err := apply(current); err != nil {
		t.Fatal(err)
	}
	if err := apply(current); !errors.Is(err, lifecycle.ErrConflict) {
		t.Fatalf("duplicate attempt changed the committed run: %v", err)
	}
	snapshots, err := w.store.PolicyEvidence(t.Context(), w.acct, []kernel.ULID{run.ID})
	if err != nil || len(snapshots[run.ID].Receipts) != 1 || len(w.updates(t, run.ID)) != 2 {
		t.Fatalf("retry duplicated evidence: %+v %v", snapshots, err)
	}
	var state string
	if err := w.db.QueryRow(t.Context(), `SELECT state FROM jobs WHERE id=$1`, id).Scan(&state); err != nil || state != "succeeded" {
		t.Fatalf("job completion separated from evidence: %s %v", state, err)
	}
}
