package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/feedback"
)

func TestAccCheckMutationsRequireCompletionTransaction(t *testing.T) {
	w := newCheckWorld(t)
	before := w.checkMetadata(t)
	if _, err := w.f.store.BindCheck(t.Context(), w.b); !errors.Is(err, queue.ErrNoTransaction) {
		t.Fatal("binding outside completion transaction", err)
	}
	if after := w.checkMetadata(t); !maps.Equal(before, after) {
		t.Fatal("untransactional binding persisted metadata", before, after)
	}
	w.enqueue(t, w.b, 1)
	j := w.claim(t)
	var r feedback.CheckResource
	if err := w.f.db.QueryRow(t.Context(), `SELECT resource_key,binding_key,owner_id,owner_epoch FROM vcs_check_resources WHERE account_id=$1`, w.f.acct).
		Scan(&r.Key, &r.Binding, &r.Owner, &r.Epoch); err != nil {
		t.Fatal(err)
	}
	r.Account = w.f.acct
	before = w.checkMetadata(t)
	for name, err := range map[string]error{
		"integration hold": func() error {
			_, err := w.f.store.HoldCheckIntegration(t.Context(), w.f.acct, w.b.Integration)
			return err
		}(),
		"request": func() error {
			_, err := w.f.store.RequestCheck(t.Context(), feedback.CheckRequest{Binding: w.b, Name: "infraweave/infra", Sequence: 99})
			return err
		}(),
		"resource hold":  func() error { _, err := w.f.store.HoldCheck(t.Context(), r); return err }(),
		"reserve":        w.f.store.ReserveCheck(t.Context(), r),
		"acknowledge":    w.f.store.ReportedCheck(t.Context(), feedback.CheckResource{Account: r.Account, Key: r.Key, Binding: r.Binding, NativeID: "uncommitted-native-id", Sequence: 99}),
		"delete":         w.f.store.DeleteStackChecks(t.Context(), w.f.acct, w.b.Stack),
		"stack deletion": w.service.Deleting(t.Context(), stacksapi.Stack{Account: w.f.acct, ID: w.b.Stack}),
		"cap":            w.service.Capped(t.Context(), w.f.acct, w.b.Integration, w.b.Stack, w.b.Commit, w.b.Type, 2),
	} {
		if !errors.Is(err, queue.ErrNoTransaction) {
			t.Errorf("%s outside completion transaction: %v", name, err)
		}
	}
	if err := tx.Do(
		t.Context(),
		w.f.db,
		tx.Options{},
		func(ctx context.Context) error { return w.service.Checked(ctx, j) },
	); !errors.Is(err, feedback.ErrCheckReport) ||
		!strings.Contains(err.Error(), "inherited transaction") {
		t.Fatal("native effect inherited caller transaction", err)
	}
	// A fabricated or foreign resource acknowledgement cannot create a cursor or
	// update the genuine owner's cursor, even inside a completion transaction.
	for _, account := range []kernel.AccountID{w.f.acct, kernel.NewAccountID()} {
		missing := r
		missing.Account = account
		if account == w.f.acct {
			missing.Key = "missing-native-resource"
		}
		if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error { return w.f.store.ReportedCheck(ctx, missing) }); !errors.Is(err, feedback.ErrNoCheck) {
			t.Fatal("acknowledged absent scoped resource", err)
		}
	}
	absent := w.b
	absent.Integration = kernel.NewULID()
	absent.Run = kernel.NewULID()
	absent.Key = "run:" + absent.Run.String()
	if err := tx.Do(
		t.Context(),
		w.f.db,
		tx.Options{},
		func(ctx context.Context) error { _, err := w.f.store.BindCheck(ctx, absent); return err },
	); !errors.Is(
		err,
		feedback.ErrNoCheck,
	) {
		t.Fatal("bound check without its integration", err)
	}
	if after := w.checkMetadata(t); !maps.Equal(before, after) {
		t.Fatal("rejected mutations changed durable check state", before, after)
	}
	w.checkUnreported(t)
	if err := w.service.Checked(t.Context(), j); err != nil {
		t.Fatal("genuine completion after rejected mutations", err)
	}
	w.checkCompleted(t, j)
}

func TestAccCheckInvalidEnvelopeDiesWithoutProviderEffects(t *testing.T) {
	for _, scenario := range []string{"malformed envelope", "foreign account"} {
		t.Run(scenario, func(t *testing.T) {
			w := newCheckWorld(t)
			w.enqueue(t, w.b, 1)
			genuine := w.claim(t)
			before := w.checkMetadata(t)
			var payload any = "invalid envelope"
			if scenario == "foreign account" {
				b := w.b
				b.Account = kernel.NewAccountID()
				payload = feedback.CheckRequest{Binding: b, Name: "infraweave/infra", Sequence: 1}
			}
			id, err := w.jobs.Enqueue(t.Context(), feedback.ReportCheck, payload, queue.Account(w.f.acct.ULID))
			if err != nil {
				t.Fatal(err)
			}
			registry := queue.NewRegistry()
			if err := registry.Register(feedback.ReportCheck, w.service.Checked, queue.HandlerOptions{}); err != nil {
				t.Fatal(err)
			}
			cfg := queue.Defaults()
			cfg.PollInterval = 10 * time.Millisecond
			cfg.Queues.Deliveries.Workers = 1
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			done := make(chan struct{})
			workers := queue.NewWorkers(w.jobs, registry, cfg, queue.AllowAll{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), "invalid-check-contract")
			go func() { defer close(done); workers.Run(ctx) }()
			t.Cleanup(func() { cancel(); <-done })
			tick := time.NewTicker(10 * time.Millisecond)
			defer tick.Stop()
			for {
				dead, err := w.jobs.ReadDead(ctx, id)
				if err == nil {
					if dead.Attempt != 1 || dead.LastError != feedback.ErrCheckReport.Error() || dead.Account != w.f.acct.ULID {
						t.Fatal("invalid envelope was not permanently and safely rejected", dead)
					}
					break
				}
				if !errors.Is(err, queue.ErrNotDead) {
					t.Fatal(err)
				}
				select {
				case <-ctx.Done():
					t.Fatal("invalid envelope did not become dead", ctx.Err())
				case <-tick.C:
				}
			}
			cancel()
			<-done
			// The bad job has its own terminal row; the genuine binding, request and
			// native cursor must remain byte-for-byte unchanged.
			after := w.checkMetadata(t)
			delete(before, "jobs")
			delete(after, "jobs")
			if !maps.Equal(before, after) {
				t.Fatal("invalid envelope changed genuine metadata", before, after)
			}
			w.checkUnreported(t)
			if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error { return w.jobs.HoldTx(ctx, genuine) }); err != nil {
				t.Fatal("invalid envelope invalidated genuine claim", err)
			}
			if err := w.service.Checked(t.Context(), genuine); err != nil {
				t.Fatal("genuine claim could not recover", err)
			}
			w.checkCompleted(t, genuine)
		})
	}
}

func TestAccCheckMalformedDurableBindingRequiresRepair(t *testing.T) {
	for _, mode := range []string{"run", "cap"} {
		t.Run(mode, func(t *testing.T) {
			w := newCheckWorld(t)
			if mode == "cap" {
				if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
					return w.service.Capped(ctx, w.f.acct, w.b.Integration, w.b.Stack, w.b.Commit, w.b.Type, 2)
				}); err != nil {
					t.Fatal(err)
				}
			} else {
				w.enqueue(t, w.b, 1)
			}
			j := w.claim(t)
			var request feedback.CheckRequest
			if err := json.Unmarshal(j.Payload, &request); err != nil {
				t.Fatal(err)
			}
			b := request.Binding
			var original []byte
			if err := w.f.db.QueryRow(t.Context(), `SELECT binding FROM vcs_check_bindings WHERE account_id=$1 AND binding_key=$2`, w.f.acct, b.Key).Scan(&original); err != nil {
				t.Fatal(err)
			}
			if _, err := w.f.db.Exec(
				t.Context(),
				`UPDATE vcs_check_bindings SET binding=jsonb_set(binding,'{stackVersion}','"invalid"'::jsonb) WHERE account_id=$1 AND binding_key=$2`,
				w.f.acct,
				b.Key,
			); err != nil {
				t.Fatal(err)
			}
			before := w.checkMetadata(t)
			err := w.service.Checked(t.Context(), j)
			cause, ok := errors.AsType[*json.UnmarshalTypeError](err)
			if !ok || cause.Field != "stackVersion" || errors.Is(err, feedback.ErrNoCheck) {
				t.Fatal("durable corruption was treated as missing", err)
			}
			if mode == "cap" {
				caps, err := w.f.store.CapChecks(t.Context(), w.b, w.b.Type)
				if _, ok := errors.AsType[*json.UnmarshalTypeError](err); !ok || len(caps) != 0 {
					t.Fatal("malformed cap returned partial facts", caps, err)
				}
			} else {
				err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error { _, err := w.f.store.BindCheck(ctx, b); return err })
				if _, ok := errors.AsType[*json.UnmarshalTypeError](err); !ok {
					t.Fatal("rebinding concealed corrupt immutable metadata", err)
				}
			}
			if after := w.checkMetadata(t); !maps.Equal(before, after) {
				t.Fatal("corrupt binding changed requests/resources/claim", before, after)
			}
			w.checkUnreported(t)
			if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error { return w.jobs.HoldTx(ctx, j) }); err != nil {
				t.Fatal("corrupt binding acknowledged claim", err)
			}
			if _, err := w.f.db.Exec(t.Context(), `UPDATE vcs_check_bindings SET binding=$3 WHERE account_id=$1 AND binding_key=$2`, w.f.acct, b.Key, original); err != nil {
				t.Fatal(err)
			}
			if err := w.service.Checked(t.Context(), j); err != nil {
				t.Fatal("same claim did not recover after durable repair", err)
			}
			w.checkCompleted(t, j)
		})
	}
}

func TestAccCheckCurrentIdentityAndSequenceFence(t *testing.T) {
	for _, scenario := range []string{"missing run", "different stack", "different commit", "different type", "future sequence"} {
		t.Run(scenario, func(t *testing.T) {
			w := newCheckWorld(t)
			w.enqueue(t, w.b, 1)
			j := w.claim(t)
			original := w.facts.runs[w.b.Run]
			fact := original
			switch scenario {
			case "missing run":
				delete(w.facts.runs, w.b.Run)
			case "different stack":
				fact.Stack = kernel.NewULID()
			case "different commit":
				fact.CommitSHA = requestHash("different-check-head")
			case "different type":
				fact.Type = kernel.RunTypeTracked
			case "future sequence":
				fact.Sequence = 0
			}
			if scenario != "missing run" {
				w.facts.runs[w.b.Run] = fact
			}
			err := w.service.Checked(t.Context(), j)
			w.checkUnreported(t)
			if scenario == "future sequence" {
				if !errors.Is(err, feedback.ErrCheckReport) {
					t.Fatal("future request acknowledged before its facts", err)
				}
				if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error { return w.jobs.HoldTx(ctx, j) }); err != nil {
					t.Fatal("future request lost its claim", err)
				}
				w.facts.runs[w.b.Run] = original
				if err := w.service.Checked(t.Context(), j); err != nil {
					t.Fatal("same claim did not recover when facts caught up", err)
				}
				w.checkCompleted(t, j)
			} else {
				if err != nil {
					t.Fatal("ineligible current identity did not settle", err)
				}
				w.checkSettled(t, j)
			}
		})
	}
}

func TestAccCheckPullRequestIdentityCorruptionRequiresRepair(t *testing.T) {
	for _, number := range []string{"malformed", "0", "-1"} {
		t.Run(number, func(t *testing.T) {
			w := newCheckWorld(t)
			w.enqueue(t, w.b, 1)
			j := w.claim(t)
			first := storedRequest(w.f, w.b.Integration)
			first.HeadSHA = w.b.Commit
			upsertRequest(t, w.f, first)
			second := first
			second.ID, second.Number = kernel.NewULID(), "18"
			upsertRequest(t, w.f, second)
			if _, err := w.f.db.Exec(t.Context(), `UPDATE pull_requests SET provider_id=$3 WHERE account_id=$1 AND id=$2`, w.f.acct, second.ID, number); err != nil {
				t.Fatal(err)
			}
			before := w.checkMetadata(t)
			// The malformed spelling sorts after a valid row; callers must not
			// receive that partial prefix when another persisted native ID is invalid.
			got, err := w.f.store.CheckPullRequests(t.Context(), w.b)
			if !errors.Is(err, feedback.ErrCheckReport) || len(got) != 0 {
				t.Fatal("corrupt native PR identity returned partial report targets", got, err)
			}
			foreign := w.b
			foreign.Account = kernel.NewAccountID()
			if got, err := w.f.store.CheckPullRequests(t.Context(), foreign); err != nil || len(got) != 0 {
				t.Fatal("foreign account observed corrupt PR identity", got, err)
			}
			if after := w.checkMetadata(t); !maps.Equal(before, after) {
				t.Fatal("failed identity read mutated check metadata", before, after)
			}
			w.checkUnreported(t)
			if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error { return w.jobs.HoldTx(ctx, j) }); err != nil {
				t.Fatal("corrupt identity read invalidated genuine claim", err)
			}
			var retained string
			if err := w.f.db.QueryRow(t.Context(), `SELECT provider_id FROM pull_requests WHERE account_id=$1 AND id=$2`, w.f.acct, second.ID).
				Scan(&retained); err != nil ||
				retained != number {
				t.Fatal("failed read silently changed corrupt identity", retained, err)
			}
			if _, err := w.f.db.Exec(t.Context(), `UPDATE pull_requests SET provider_id=$3 WHERE account_id=$1 AND id=$2`, w.f.acct, second.ID, second.Number); err != nil {
				t.Fatal(err)
			}
			if got, err := w.f.store.CheckPullRequests(t.Context(), w.b); err != nil || !slices.Equal(got, []int{17, 18}) {
				t.Fatal("repaired durable identity did not restore complete report targets", got, err)
			}
			if err := w.service.Checked(t.Context(), j); err != nil {
				t.Fatal("genuine claim could not complete after scoped PR read repair", err)
			}
			w.checkCompleted(t, j)
		})
	}
}

func (w *checkWorld) checkMetadata(t *testing.T) map[string]string {
	t.Helper()
	result := map[string]string{}
	for _, table := range []string{"vcs_check_bindings", "vcs_check_requests", "vcs_check_resources", "jobs"} {
		var snapshot string
		if err := w.f.db.QueryRow(t.Context(), `SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text),'[]'::jsonb)::text FROM `+table+` r WHERE account_id=$1`, w.f.acct).
			Scan(&snapshot); err != nil {
			t.Fatal(err)
		}
		result[table] = snapshot
	}
	return result
}

func (w *checkWorld) checkUnreported(t *testing.T) {
	t.Helper()
	if w.checks.calls != 0 || len(w.fake.CheckWrites("acme", "infra")) != 0 {
		t.Fatal("invalid completion reached native provider", w.checks.calls, w.fake.CheckWrites("acme", "infra"))
	}
	var sequence int64
	var native string
	if err := w.f.db.QueryRow(t.Context(), `SELECT sequence,native_id FROM vcs_check_resources WHERE account_id=$1`, w.f.acct).
		Scan(&sequence, &native); err != nil || sequence != 0 ||
		native != "" {
		t.Fatal("invalid completion advanced native cursor", sequence, native, err)
	}
}

func (w *checkWorld) checkCompleted(t *testing.T, j queue.Job) {
	t.Helper()
	if w.checks.calls != 1 || len(w.fake.CheckWrites("acme", "infra")) != 1 {
		t.Fatal("genuine claim did not report exactly once", w.checks.calls, w.fake.CheckWrites("acme", "infra"))
	}
	var sequence int64
	var native string
	if err := w.f.db.QueryRow(t.Context(), `SELECT sequence,native_id FROM vcs_check_resources WHERE account_id=$1`, w.f.acct).
		Scan(&sequence, &native); err != nil || sequence <= 0 ||
		native == "" {
		t.Fatal("completion did not persist native cursor", sequence, native, err)
	}
	w.checkSettled(t, j)
}

func (w *checkWorld) checkSettled(t *testing.T, j queue.Job) {
	t.Helper()
	var state string
	var attempt int
	if err := w.f.db.QueryRow(t.Context(), `SELECT state,attempt FROM jobs WHERE id=$1 AND account_id=$2`, j.ID, w.f.acct).
		Scan(&state, &attempt); err != nil || state != "succeeded" ||
		attempt != 1 {
		t.Fatal("claim did not settle atomically", state, attempt, err)
	}
	if err := w.jobs.Complete(t.Context(), j); !errors.Is(err, queue.ErrLeaseLost) {
		t.Fatal("completed claim remained valid", err)
	}
}
