package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/runstest"
)

// Two real app-owned service graphs share PostgreSQL. Public approval/report
// handlers adopt this transaction contract in their owning workflow tickets.
func TestAccEvaluatorEvidenceSharesTwoReplicaTransitionFence(t *testing.T) {
	w := newHoldWorld(t)
	events := testEvents(t, w.q)
	second, err := newDomains(config.Defaults(), w.db, w.keys, w.blobs.Objects, nil, events, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	servers := []Domains{w.d, second}
	if _, err := w.d.Stacks.API().StackCreate(w.admin, api.StackCreateRequestObject{Body: &api.StackCreateRequest{Input: api.StackInput{
		Name: "Evidence target", Repository: "infra", Branch: "main", Provider: new(api.VCSProviderGIT), Space: new("kept"),
	}}}); err != nil {
		t.Fatal(err)
	}
	stack, err := w.d.Stacks.Stacks().BySlug(t.Context(), w.acct, "evidence-target")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := w.d.Stacks.Stacks().Settings(t.Context(), w.acct, stack.ID)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	var run runsapi.CreatedRun
	if err := tx.Do(t.Context(), w.q, tx.Options{}, func(ctx context.Context) error {
		var err error
		run, err = w.d.Runs.Creator().Create(ctx, runsapi.CreateRequest{Account: w.acct, Stack: stack.ID, Type: kernel.RunTypeTracked, Mode: runsapi.None,
			Commit: runsapi.Commit{Hash: "receipt-test", Branch: "main", Timestamp: at, AuthorName: "Alice", Message: "policy evidence"}, TriggeredBy: "alice"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.d.Policies.API().PolicyCreatev2(w.admin, api.PolicyCreatev2RequestObject{Body: &api.PolicyCreateInput{
		Name: "Queue gate", Type: api.PolicyTypeAPPROVAL, Space: new("kept"), Labels: &[]string{"autoattach:*"},
		Body: "package infraweave\napprove if input.run.state == \"QUEUED\"\nflag contains \"evaluated\" if input.run.flags == []\n",
	}}); err != nil {
		t.Fatal(err)
	}
	jobs := queue.New(w.q)
	kind := queue.Kind{Name: "test.real-policy-evidence", Queue: queue.Default, Policy: queue.Ignore}
	jobID, err := jobs.Enqueue(t.Context(), kind, struct{}{}, queue.Account(w.acct.ULID))
	if err != nil {
		t.Fatal(err)
	}
	claim := func(owner string) queue.Job {
		t.Helper()
		claimed, err := jobs.Claim(t.Context(), queue.Default, []string{kind.Name}, 1, time.Minute, owner)
		if err != nil || len(claimed) != 1 {
			t.Fatalf("server claim: %+v %v", claimed, err)
		}
		return claimed[0]
	}
	old := claim("server-a")
	if _, err := w.q.Exec(t.Context(), `UPDATE jobs SET lease_until=now()-interval '1 minute' WHERE id=$1`, jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	current := claim("server-b")
	count := func(table string) int {
		t.Helper()
		var n int
		if table != "events" && table != "jobs" && table != "run_policy_receipts" && table != "run_flags" {
			t.Fatal("unknown table")
		}
		if err := w.q.QueryRow(t.Context(), `SELECT count(*) FROM `+table+` WHERE account_id=$1`, w.acct).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	beforeEvents, beforeJobs := count("events"), count("jobs")
	abort := errors.New("caller rollback")
	apply := func(server Domains, job queue.Job, rollback bool) error {
		d := runstest.EvidenceDeps{DB: w.q, Stacks: server.Stacks.Stacks(), Events: events, Jobs: jobs, At: at}
		return tx.Do(t.Context(), w.q, tx.Options{}, func(ctx context.Context) error {
			got, err := server.Policies.Evaluator().Evaluate(ctx, policyapi.EvaluationRequest{
				Account: w.acct, Type: policyapi.Approval, Target: policyapi.Target{Kind: policyapi.StackTarget, ID: stack.ID}, Run: &run.ID,
				BuildInput: func(ctx context.Context) (json.RawMessage, error) {
					return runstest.Input(ctx, d, server.Policies.RunInputBuilder(), policyapi.RunInputFacts{
						Account: w.acct, Type: policyapi.Approval, At: at,
						Stack: policyapi.StackFacts{ID: stack.ID, Slug: stack.Slug, Name: stack.Name, State: kernel.StackStateNone,
							Branch: settings.Branch, Namespace: settings.Namespace, Repository: settings.Repository, Autodeploy: settings.Autodeploy},
					}, run.ID)
				},
			})
			if err != nil {
				return err
			}
			if got.Outcome != policyapi.Approve || len(got.Receipts) != 1 {
				return fmt.Errorf("real approval refused: %+v", got)
			}
			if err := runstest.Ready(ctx, d, w.acct, run.ID, 1, got.Receipts); err != nil {
				return err
			}
			if rollback {
				return abort
			}
			return jobs.CompleteTx(ctx, job)
		})
	}
	for _, attempt := range []struct {
		server   Domains
		job      queue.Job
		rollback bool
		want     error
	}{{servers[0], old, false, queue.ErrLeaseLost}, {servers[1], current, true, abort}} {
		if err := apply(attempt.server, attempt.job, attempt.rollback); !errors.Is(err, attempt.want) {
			t.Fatalf("failed attempt: %v, want %v", err, attempt.want)
		}
		var state string
		var version int64
		if err := w.q.QueryRow(t.Context(), `SELECT state,state_version FROM runs WHERE account_id=$1 AND id=$2`, w.acct, run.ID).Scan(&state, &version); err != nil || state != "QUEUED" || version != 1 || count("run_policy_receipts") != 0 || count("run_flags") != 0 || count("events") != beforeEvents || count("jobs") != beforeJobs {
			t.Fatalf("rollback escaped: state=%s/%d error=%v", state, version, err)
		}
	}
	if err := apply(servers[1], current, false); err != nil {
		t.Fatal(err)
	}
	if err := apply(servers[0], old, false); err == nil {
		t.Fatal("stale replica acknowledged a committed transition")
	}
	if count("run_policy_receipts") != 1 || count("run_flags") != 1 || count("events") != beforeEvents+2 || count("jobs") != beforeJobs+2 {
		t.Fatal("actual evaluator evidence did not commit exactly once")
	}
	if err := tx.Do(t.Context(), w.q, tx.Options{}, func(ctx context.Context) error {
		next, err := w.d.Runs.Creator().Create(ctx, runsapi.CreateRequest{Account: w.acct, Stack: stack.ID, Type: kernel.RunTypeTracked, Mode: runsapi.None,
			Commit: runsapi.Commit{Hash: "next-receipt-test", Branch: "main", Timestamp: at, AuthorName: "Alice", Message: "next"}, TriggeredBy: "policy/trigger"})
		if err != nil {
			return err
		}
		view, err := w.d.Stacks.API().Stack(w.admin, api.StackRequestObject{StackId: stack.Slug})
		if err != nil {
			return err
		}
		current, ok := view.(api.Stack200JSONResponse)
		if !ok {
			return fmt.Errorf("stack view: %T", view)
		}
		d := runstest.EvidenceDeps{DB: w.q, Stacks: w.d.Stacks.Stacks(), Events: events, Jobs: jobs, At: at}
		raw, err := runstest.Input(ctx, d, w.d.Policies.RunInputBuilder(), policyapi.RunInputFacts{Account: w.acct, Type: policyapi.Plan, At: at,
			Stack: policyapi.StackFacts{ID: stack.ID, Slug: stack.Slug, Name: stack.Name, State: kernel.StackState(current.Body.State),
				Branch: settings.Branch, Namespace: settings.Namespace, Repository: settings.Repository, Autodeploy: settings.Autodeploy}}, next.ID)
		if err != nil {
			return err
		}
		//nolint:tagliatelle // the published policy contract uses snake_case.
		var input struct {
			Infraweave struct {
				Run struct {
					Flags []string `json:"flags"`
				} `json:"run"`
				Previous struct {
					ID    string   `json:"id"`
					Flags []string `json:"flags"`
				} `json:"previous_run"`
			} `json:"infraweave"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return err
		}
		if len(input.Infraweave.Run.Flags) != 0 || input.Infraweave.Previous.ID != run.ID.String() || len(input.Infraweave.Previous.Flags) != 1 || input.Infraweave.Previous.Flags[0] != "evaluated" {
			return errors.New("next run lost previous flags or inherited owners")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
