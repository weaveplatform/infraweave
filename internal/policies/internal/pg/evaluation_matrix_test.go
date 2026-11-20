package pg_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
)

func fixtureEvaluationRequest(f *fixture, kind policyapi.Type, input json.RawMessage) policyapi.EvaluationRequest {
	target := policyapi.Target{Kind: policyapi.StackTarget, ID: f.stacks.stack.ID}
	if kind == policyapi.Login || kind == policyapi.Notification {
		target = policyapi.Target{Kind: policyapi.SpaceTarget, ID: f.principal.AccountID.ULID}
	}
	return policyapi.EvaluationRequest{Account: f.principal.AccountID, Type: kind, Target: target, Input: input}
}

func TestAccEvaluationNativeTimeoutCannotApproveOrAllow(t *testing.T) {
	for _, test := range []struct {
		kind       policyapi.Type
		rule, file string
		want       policyapi.Outcome
	}{
		{policyapi.Plan, "deny contains \"late\"", "plan-tracked-warn.json", policyapi.Deny},
		{policyapi.Approval, "approve", "approval-c51-queued.json", policyapi.Undecided},
	} {
		t.Run(string(test.kind), func(t *testing.T) {
			f := newFixture(t)
			body := "package infraweave\nvalues := numbers.range(0,299)\n" + test.rule + ` if {
some a in values
some b in values
some c in values
some d in values
a+b+c+d == -1
}`
			expensive, err := f.svc.Create(
				t.Context(),
				f.principal,
				core.Input{Name: "Expensive program", Type: rego.Type(test.kind), Body: body, Labels: []string{"autoattach:*"}},
			)
			if err != nil {
				t.Fatal(err)
			}
			if test.kind == policyapi.Approval {
				if _, err := f.svc.Create(
					t.Context(),
					f.principal,
					core.Input{Name: "Other approval", Type: rego.Approval, Body: "package infraweave\napprove := true", Labels: []string{"autoattach:*"}},
				); err != nil {
					t.Fatal(err)
				}
			}
			settings := rego.Defaults()
			settings.Timeouts.Plan = 25 * time.Millisecond
			settings.Timeouts.Approval = 25 * time.Millisecond
			runtime, err := rego.New(settings)
			if err != nil {
				t.Fatal(err)
			}
			f.runtime = runtime
			svc := f.service(f.events)
			started := time.Now()
			got, err := svc.Evaluate(t.Context(), fixtureEvaluationRequest(f, test.kind, evaluationInput(t, test.file)))
			if err != nil || got.Outcome != test.want || len(got.Failures) == 0 || time.Since(started) > 5*time.Second {
				t.Fatalf("actual expensive evaluation escaped its deadline: %+v %v", got, err)
			}
			found := false
			for _, receipt := range got.Receipts {
				if receipt.Policy == expensive.ID {
					found = receipt.Failure != nil && receipt.Failure.Code == "TIMEOUT"
				}
			}
			if !found {
				t.Fatalf("expensive program did not record its timeout: %+v", got.Receipts)
			}
		})
	}
}

func TestAccEvaluationConcurrentInputsAndHelperIsolation(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"one", "two"} {
		body := "package infraweave\nhelper := \"" + name + "\"\nflag contains helper if true\ndeny contains concat(\":\",[helper,input.request.command]) if true"
		if _, err := f.svc.Create(t.Context(), f.principal, core.Input{Name: name, Type: rego.Task, Body: body, Labels: []string{"autoattach:*"}}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	g, ctx := errgroup.WithContext(ctx)
	for i := range 16 {
		g.Go(func() error {
			command := fmt.Sprintf("input-%d", i)
			raw := json.RawMessage(
				`{"request":{"command":"` + command + `","timestamp_ns":1795140000000000001},"session":{"machine":false,"teams":[]},"stack":{"administrative":false,"autodeploy":false,"branch":"main","id":"stack","name":"Stack","namespace":"owner","project_root":"","repository":"repo","state":"NONE"}}`,
			)
			got, err := f.svc.Evaluate(ctx, fixtureEvaluationRequest(f, policyapi.Task, raw))
			if err != nil {
				return err
			}
			if got.Outcome != policyapi.Deny || len(got.Receipts) != 2 || len(got.Negative.Deny) != 2 || got.Negative.Deny[0] != "one:"+command ||
				got.Negative.Deny[1] != "two:"+command ||
				len(got.Receipts[0].Flags) != 1 ||
				len(got.Receipts[1].Flags) != 1 ||
				got.Receipts[0].Flags[0] == got.Receipts[1].Flags[0] {
				return fmt.Errorf("independent program/input leaked: %+v", got)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestAccEvaluationCommitDisconnectRollsBackEvidence(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Create(
		t.Context(),
		f.principal,
		core.Input{Name: "Commit fence", Type: rego.Plan, Body: "package infraweave\n", Labels: []string{"autoattach:*"}},
	); err != nil {
		t.Fatal(err)
	}
	before := f.count(t, "events")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	err := tx.Do(ctx, f.db, tx.Options{NoRetry: true}, func(ctx context.Context) error {
		got, err := f.svc.Evaluate(ctx, fixtureEvaluationRequest(f, policyapi.Plan, evaluationInput(t, "plan-tracked-warn.json")))
		if err != nil {
			return err
		}
		if got.Outcome != policyapi.Allow || len(got.Receipts) != 1 {
			return errors.New("evaluation failed before commit fence")
		}
		var pid int
		if err := tx.Q(ctx, f.db).QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			return err
		}
		var stopped bool
		if err := f.db.QueryRow(ctx, "SELECT pg_terminate_backend($1)", pid).Scan(&stopped); err != nil {
			return err
		}
		if !stopped {
			return errors.New("backend was not terminated")
		}
		return nil
	})
	if err == nil || f.count(t, "events") != before {
		t.Fatalf("commit disconnect leaked evaluator evidence: %v", err)
	}
}

func TestAccEvaluationEveryServedTypeDefaultAndError(t *testing.T) {
	for _, test := range []struct {
		kind                         policyapi.Type
		file                         string
		raw                          string
		defaultOutcome, errorOutcome policyapi.Outcome
	}{
		{policyapi.Plan, "plan-tracked-warn.json", "", policyapi.Allow, policyapi.Deny},
		{policyapi.Approval, "approval-c51-queued.json", "", policyapi.Undecided, policyapi.Undecided},
		{policyapi.Push, "push-track.json", "", policyapi.Ignore, policyapi.Ignore},
		{policyapi.Trigger, "trigger-finished.json", "", policyapi.Ignore, policyapi.Ignore},
		{policyapi.Notification, "notification-finished.json", "", policyapi.Ignore, policyapi.Ignore},
		{policyapi.Login, "", `{"request":{},"session":{"machine":false,"member":true,"teams":[]}}`, policyapi.Deny, policyapi.Deny},
		{policyapi.Task, "", `{"request":{"command":"echo test","timestamp_ns":1795140000000000001},"session":{"machine":false,"teams":[]},"stack":{"administrative":false,"autodeploy":false,"state":"NONE","branch":"main","id":"stack","name":"Stack","namespace":"owner","project_root":"","repository":"repo"}}`, policyapi.Allow, policyapi.Deny},
		{policyapi.Initialization, "", `{"request":{"timestamp_ns":1795140000000000001},"commit":{"author":"actor","branch":"main","created_at":1,"message":"test"},"run":{"id":"run","created_at":1,"updated_at":1,"type":"TRACKED","state":"QUEUED","triggered_by":null},"stack":{"administrative":false,"autodeploy":false,"state":"NONE","branch":"main","id":"stack","name":"Stack","namespace":"owner","project_root":"","repository":"repo"}}`, policyapi.Allow, policyapi.Deny},
	} {
		t.Run(string(test.kind), func(t *testing.T) {
			f := newFixture(t)
			raw := json.RawMessage(test.raw)
			if test.file != "" {
				raw = evaluationInput(t, test.file)
			}
			req := fixtureEvaluationRequest(f, test.kind, raw)
			calls := 0
			req.Input = nil
			req.BuildInput = func(ctx context.Context) (json.RawMessage, error) {
				calls++
				if !tx.In(ctx) {
					return nil, errors.New("builder outside transaction")
				}
				return raw, nil
			}
			got, err := f.svc.Evaluate(t.Context(), req)
			if err != nil || got.Outcome != policyapi.NoPolicies || calls != 0 || len(got.Receipts) != 0 || f.count(t, "events") != 0 {
				t.Fatalf("policy-free path built facts or wrote evidence: %+v %v %d", got, err, calls)
			}
			labels := []string{"autoattach:*"}
			if test.kind == policyapi.Login || test.kind == policyapi.Notification {
				labels = nil
			}
			p, err := f.svc.Create(t.Context(), f.principal, core.Input{Name: string(test.kind), Type: rego.Type(test.kind), Body: "package infraweave\n", Labels: labels})
			if err != nil {
				t.Fatal(err)
			}
			got, err = f.svc.Evaluate(t.Context(), req)
			if err != nil || got.Outcome != test.defaultOutcome || len(got.Failures) != 0 || calls != 1 || len(got.Receipts) != 1 {
				t.Fatalf("attached preamble: %+v %v calls=%d", got, err, calls)
			}
			if _, err := f.db.Exec(t.Context(), "UPDATE policies SET body=$3 WHERE account_id=$1 AND id=$2", p.Account, p.ID, "package infraweave\ninvalid !"); err != nil {
				t.Fatal(err)
			}
			got, err = f.svc.Evaluate(t.Context(), req)
			if err != nil || got.Outcome != test.errorOutcome || len(got.Failures) != 1 || got.Failures[0].Code != "COMPILE" || len(got.Receipts) != 1 {
				t.Fatalf("corrupt stored program was not fail closed: %+v %v", got, err)
			}
			if test.kind == policyapi.Push && !got.Push.FailedCheck {
				t.Fatal("push error omitted failed check")
			}
			if test.kind == policyapi.Login && !got.Receipts[0].Sample {
				t.Fatal("login failure lost default sampling request")
			}
		})
	}
}

func TestAccEvaluationCurrentReachLabelsManualAndAccount(t *testing.T) {
	f := newFixture(t)
	p, err := f.svc.Create(
		t.Context(),
		f.principal,
		core.Input{Name: "Current labels", Type: rego.Plan, Body: "package infraweave\ndeny contains \"blocked\" if true", Labels: []string{"autoattach:prod"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	req := fixtureEvaluationRequest(f, policyapi.Plan, evaluationInput(t, "plan-tracked-warn.json"))
	read := func(want policyapi.Outcome) {
		t.Helper()
		got, err := f.svc.Evaluate(t.Context(), req)
		if err != nil || got.Outcome != want {
			t.Fatalf("effective selector: %+v %v want %s", got, err, want)
		}
	}
	read(policyapi.Deny)
	f.stacks.labels = []string{"dev"}
	read(policyapi.NoPolicies)
	f.stacks.labels = []string{"prod"}
	if err := f.store.DeleteStack(t.Context(), p.Account, f.stacks.stack.ID); err != nil {
		t.Fatal(err)
	}
	read(policyapi.Deny)
	f.stacks.labels = []string{"dev"}
	if _, _, err := f.svc.Attach(t.Context(), f.principal, p.ID.String(), f.stacks.stack.Slug); err != nil {
		t.Fatal(err)
	}
	read(policyapi.Deny)
	f.stacks.stack.Space = f.spaces.nodes[1].ID
	read(policyapi.Deny)
	f.spaces.nodes[1].Inherit = false
	read(policyapi.NoPolicies)
	f.spaces.nodes[1].Inherit = true
	foreign := p
	foreign.Account = kernel.NewAccountID()
	foreign.Body = "package infraweave\n"
	if err := f.store.Create(t.Context(), foreign); err != nil {
		t.Fatal(err)
	}
	read(policyapi.Deny)
	if err := f.store.Delete(t.Context(), p.Account, p.ID); err != nil {
		t.Fatal(err)
	}
	read(policyapi.NoPolicies)
}

func TestAccEvaluationLoginTeamsAreOrderedAndSnapshotsIndependent(t *testing.T) {
	f := newFixture(t)
	first, err := f.svc.Create(t.Context(), f.principal, core.Input{Name: "Rewrite", Type: rego.Login, Body: `package infraweave
team contains "new-team" if true`})
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.svc.Create(t.Context(), f.principal, core.Input{Name: "Consume rewrite", Type: rego.Login, Body: `package infraweave
allow if "new-team" in input.session.teams`})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Compare(first.ID[:], second.ID[:]) >= 0 {
		t.Fatal("test needs increasing immutable IDs")
	}
	raw := json.RawMessage(`{"request":{},"session":{"machine":false,"member":true,"teams":["original"]}}`)
	before := bytes.Clone(raw)
	got, err := f.svc.Evaluate(t.Context(), fixtureEvaluationRequest(f, policyapi.Login, raw))
	if err != nil || got.Outcome != policyapi.Allow || len(got.Samples) != 2 || !bytes.Equal(raw, before) || !bytes.Contains(got.Samples[0].Input, []byte("original")) ||
		!bytes.Contains(got.Samples[1].Input, []byte("new-team")) {
		t.Fatalf("login rewrite/snapshot isolation: %+v %v", got, err)
	}
	got.Samples[0].Input[0] = '!'
	if !json.Valid(got.Samples[1].Input) || !json.Valid(raw) {
		t.Fatal("one sampling snapshot aliases another")
	}
}

type evaluationEvents struct {
	events   *outbox.Outbox
	failKind string
	err      error
}

func (e evaluationEvents) Append(ctx context.Context, ev outbox.Event) (outbox.Event, error) {
	if ev.Kind == e.failKind {
		return outbox.Event{}, e.err
	}
	return e.events.Append(ctx, ev)
}

func TestAccEvaluationErrorsRollbackEventsAndQueue(t *testing.T) {
	f := newFixture(t)
	p, err := f.svc.Create(t.Context(), f.principal, core.Input{Name: "Unsafe error marker", Type: rego.Plan, Body: "package infraweave\n", Labels: []string{"autoattach:*"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(t.Context(), "UPDATE policies SET body=$3 WHERE account_id=$1 AND id=$2", p.Account, p.ID, "package infraweave\nsecret-source-marker !"); err != nil {
		t.Fatal(err)
	}
	req := fixtureEvaluationRequest(f, policyapi.Plan, evaluationInput(t, "plan-tracked-warn.json"))
	before := f.count(t, "events")
	jobs := func() int {
		t.Helper()
		var n int
		if err := f.db.QueryRow(t.Context(), "SELECT count(*) FROM jobs WHERE account_id=$1", p.Account).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	beforeJobs := jobs()
	injected := errors.New("injected event failure")
	for _, kind := range []string{policyapi.EvaluatedKind, policyapi.InternalErrorKind} {
		svc := f.service(evaluationEvents{events: f.events, failKind: kind, err: injected})
		got, err := svc.Evaluate(t.Context(), req)
		if !errors.Is(err, injected) || len(got.Receipts) != 0 || f.count(t, "events") != before || jobs() != beforeJobs {
			t.Fatalf("failed event left evidence: %+v %v", got, err)
		}
	}
	got, err := f.svc.Evaluate(t.Context(), req)
	if err != nil || got.Outcome != policyapi.Deny || f.count(t, "events") != before+2 || jobs() != beforeJobs+2 {
		t.Fatalf("durable error records: %+v %v", got, err)
	}
	var documents string
	if err := f.db.QueryRow(t.Context(), "SELECT string_agg(document::text,',') FROM events WHERE account_id=$1", p.Account).Scan(&documents); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(documents, "secret-source-marker") || strings.Contains(documents, "spacelift") || strings.Contains(documents, "terraform") {
		t.Fatalf("plaintext event leaked source/input: %s", documents)
	}
	for _, buildErr := range []error{policyapi.ErrInput, injected} {
		req.Input = nil
		req.BuildInput = func(context.Context) (json.RawMessage, error) { return nil, buildErr }
		got, err := f.svc.Evaluate(t.Context(), req)
		if errors.Is(buildErr, policyapi.ErrInput) {
			if err != nil || len(got.Failures) != 1 || got.Failures[0].Code != "INPUT" {
				t.Fatalf("missing facts: %+v %v", got, err)
			}
		} else if !errors.Is(err, injected) {
			t.Fatalf("builder storage failure became decision: %+v %v", got, err)
		}
	}
}
