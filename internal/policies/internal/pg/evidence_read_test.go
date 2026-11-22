package pg_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/apisearch"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
	"github.com/weaveplatform/infraweave/internal/policies/internal/pg"
)

type evidenceAuthz struct {
	accessapi.Authorizer
	child        kernel.ULID
	blockedSpace kernel.ULID
}

func (a evidenceAuthz) Authorize(_ context.Context, p kernel.Principal, action accessapi.Action, space kernel.ULID) (bool, error) {
	return (action != accessapi.ActionSpaceAdmin || space != p.AccountID.ULID || p.ActorID != a.child) && (action != accessapi.ActionSpaceRead || space != a.blockedSpace), nil
}

func evidenceService(w *sampleWorld, auth accessapi.Authorizer) *core.Service {
	f := w.f
	return core.New(
		core.Deps{
			DB:       f.db,
			Store:    f.store,
			Evidence: f.store,
			Sampling: w.samples,
			Spaces:   f.spaces,
			Authz:    auth,
			Runtime:  f.runtime,
			Events:   f.events,
			Stacks:   f.stacks,
			Targets:  f.stacks,
			Queue:    w.q,
			Now:      func() time.Time { return f.now },
		},
	)
}

func TestAccEvidenceRawReadersBindParentAndAuditFailuresAfterRollback(t *testing.T) {
	w := newSampleWorld(t)
	receipt := w.evaluate(t).Receipts[0]
	for range 2 {
		if err := w.samples.Upload(t.Context(), w.claim(t)); err != nil {
			t.Fatal(err)
		}
	}
	child := w.f.principal
	child.ActorID = kernel.NewULID()
	svc := evidenceService(w, evidenceAuthz{child: child.ActorID})
	doc, err := svc.ReadSample(t.Context(), w.f.principal, receipt.Policy, receipt.ID)
	if err != nil || !bytes.Contains(doc.Input, []byte("private-retained-input")) || !strings.Contains(doc.Print, "private-retained-print") {
		t.Fatalf("owned root sample: %v", err)
	}
	if _, err := svc.ReadSample(t.Context(), child, receipt.Policy, receipt.ID); err == nil {
		t.Fatal("child administration exposed private sample")
	} else {
		var denied *policyapi.SampleDenied
		if !errors.As(err, &denied) {
			t.Fatalf("child denial: %v", err)
		}
	}
	for _, pair := range [][2]kernel.ULID{{kernel.NewULID(), receipt.ID}, {receipt.Policy, kernel.NewULID()}} {
		if _, err := svc.ReadSample(t.Context(), w.f.principal, pair[0], pair[1]); !errors.Is(err, policyapi.ErrSampleMissing) {
			t.Fatalf("wrong parent/key: %v", err)
		}
	}
	foreign := w.f.principal
	foreign.AccountID = kernel.NewAccountID()
	if _, err := svc.ReadSample(t.Context(), foreign, receipt.Policy, receipt.ID); !errors.Is(err, policyapi.ErrSampleMissing) {
		t.Fatalf("foreign sample: %v", err)
	}
	var count int
	var trail string
	if err := w.f.db.QueryRow(t.Context(), `SELECT count(*),coalesce(jsonb_agg(body)::text,'[]') FROM audit_trail_entries WHERE account_id=$1 AND action='policy_evidence.read'`, w.f.principal.AccountID).
		Scan(&count, &trail); err != nil ||
		count != 3 ||
		strings.Contains(trail, "private-retained") {
		t.Fatalf("safe committed failure trail %d %s %v", count, trail, err)
	}
	// A caller holding another owner's fence audits only after its true outer
	// rollback, so evidenceFailure cannot accidentally commit inside that TX.
	err = tx.Do(t.Context(), w.f.db, tx.Options{NoRetry: true}, func(ctx context.Context) error {
		_, err := svc.ReadSample(ctx, child, receipt.Policy, receipt.ID)
		return err
	})
	if err == nil {
		t.Fatal("nested private read admitted")
	}
	if err := svc.ReadFailure(t.Context(), child, err); err == nil {
		t.Fatal("failure classification lost")
	}
	if _, err := w.f.db.Exec(
		t.Context(),
		`UPDATE policy_input_objects SET expires_at=$3 WHERE account_id=$1 AND id=$2`,
		w.f.principal.AccountID,
		receipt.ID,
		w.f.now.Add(-time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReadSample(t.Context(), w.f.principal, receipt.Policy, receipt.ID); !errors.Is(err, policyapi.ErrSampleMissing) {
		t.Fatalf("expired sample opened: %v", err)
	}
}

func TestAccEvidenceMetadataDoesNotOpenObjectsAndUsesCurrentPolicySpace(t *testing.T) {
	w := newSampleWorld(t)
	receipt := w.evaluate(t).Receipts[0]
	svc := evidenceService(w, evidenceAuthz{})
	got, err := svc.SearchEvaluations(t.Context(), w.f.principal, receipt.Policy.String(), search.Input{})
	if err != nil || len(got.Edges) != 1 || got.Edges[0].Node.ID != receipt.ID || got.Edges[0].Node.Outcome != "deny" {
		t.Fatalf("pending-object metadata search: %+v %v", got, err)
	}
	planFilters := []search.Predicate{
		{Field: "runType", Constraint: search.Constraint{StringEquals: []string{"TRACKED"}}},
		{Field: "runDriftDetection", Constraint: search.Constraint{BooleanEquals: []bool{false}}},
	}
	matching, err := svc.SearchEvaluations(t.Context(), w.f.principal, receipt.Policy.String(), search.Input{Predicates: planFilters})
	if err != nil || len(matching.Edges) != 1 {
		t.Fatalf("PLAN run-type/false drift filter: %+v %v", matching, err)
	}
	for _, in := range []search.Input{{FullText: "private-retained"}, {Predicates: []search.Predicate{{Field: "outcome", Constraint: search.Constraint{StringEquals: []string{"allow"}}}}}} {
		got, err := svc.SearchEvaluations(t.Context(), w.f.principal, receipt.Policy.String(), in)
		if err != nil || len(got.Edges) != 0 {
			t.Fatalf("private/nonmatching metadata surfaced: %+v %v", got, err)
		}
	}
	suggest, err := svc.SuggestEvaluations(t.Context(), w.f.principal, receipt.Policy.String(), search.SuggestionsInput{})
	raw, _ := json.Marshal(apisearch.Suggestions(suggest))
	if err != nil || bytes.Contains(raw, []byte("private-retained")) || !bytes.Contains(raw, []byte("runDriftDetection")) {
		t.Fatalf("safe typed metadata: %s %v", raw, err)
	}
	space := w.f.spaces.nodes[1].ID
	if _, err := w.f.db.Exec(t.Context(), `UPDATE policies SET space_id=$3 WHERE account_id=$1 AND id=$2`, w.f.principal.AccountID, receipt.Policy, space); err != nil {
		t.Fatal(err)
	}
	svc = evidenceService(w, evidenceAuthz{blockedSpace: space})
	if _, err := svc.SearchEvaluations(t.Context(), w.f.principal, receipt.Policy.String(), search.Input{}); err == nil {
		t.Fatal("historical root space retained metadata access")
	}
	if _, err := svc.SuggestEvaluations(t.Context(), w.f.principal, receipt.Policy.String(), search.SuggestionsInput{}); err == nil {
		t.Fatal("historical root space retained suggestion access")
	}
}

func TestAccEvidenceCaptureReplacementReturnsNullWithoutOpeningRetiredBlob(t *testing.T) {
	w := newSampleWorld(t)
	w.capturePolicy(t, "REGO_V1")
	w.captureEvaluate(t, "first-public-capture")
	if err := w.samples.Upload(t.Context(), w.claim(t)); err != nil {
		t.Fatal(err)
	}
	current, _ := w.captureHead(t)
	svc := evidenceService(w, evidenceAuthz{})
	first, err := svc.ReadCapture(t.Context(), w.f.principal, current.String())
	if err != nil || first.Input == nil || strings.Contains(*first.Input, "capture-private-command") {
		t.Fatalf("protected sanitized capture: %+v %v", first, err)
	}
	page, err := svc.Captures(t.Context(), w.f.principal, "root", "PLAN", search.Input{})
	if err != nil || len(page.Edges) != 1 || page.Edges[0].Node.ID != *current {
		t.Fatalf("current capture list: %+v %v", page, err)
	}
	if _, err := w.f.db.Exec(
		t.Context(),
		`UPDATE policy_input_capture_heads SET reserved_at=statement_timestamp()-interval '2 minutes' WHERE account_id=$1`,
		w.f.principal.AccountID,
	); err != nil {
		t.Fatal(err)
	}
	w.captureEvaluate(t, "replacement-public-capture")
	if err := w.samples.Upload(t.Context(), w.claim(t)); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), w.f.db.Config())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	replicaFixture := *w.f
	replicaFixture.db, replicaFixture.store = pool, pg.NewWithSamples(pool, w.keys)
	replica := *w
	replica.f = &replicaFixture
	readerB := evidenceService(&replica, evidenceAuthz{})
	old, err := readerB.ReadCapture(t.Context(), w.f.principal, current.String())
	if err != nil || old.Input != nil || old.ID != *current {
		t.Fatalf("list/read replacement race lost explicit null: %+v %v", old, err)
	}
	newPage, err := readerB.Captures(t.Context(), w.f.principal, "root", "PLAN", search.Input{})
	if err != nil || len(newPage.Edges) != 1 || newPage.Edges[0].Node.ID == *current {
		t.Fatalf("replaced capture listed: %+v %v", newPage, err)
	}
	child := w.f.principal
	child.ActorID = kernel.NewULID()
	svc = evidenceService(w, evidenceAuthz{child: child.ActorID})
	if _, err := svc.ReadCapture(t.Context(), child, newPage.Edges[0].Node.ID.String()); err == nil {
		t.Fatal("child administration exposed captured input")
	}
}

func TestAccEvidenceTypedPushAndNotificationFiltersUseActualOPAInputSnapshots(t *testing.T) {
	module := `{"account":{"name":"contract-module-account"},"webhook_endpoints":[],"module_version":{"module":{"administrative":false,"branch":"main","id":"module-a","labels":[],"name":"Module A","namespace":"org","project_root":"","repository":"modules","space":null,"terraform_provider":"aws","worker_pool":null},"version":{"commit":{"author":"private-module-author","branch":"main","created_at":1,"exist_on_tracked_branch":null,"message":"private-module-commit","pull_request_id":null},"created_at":1,"id":"version-a","latest":true,"number":"1.0.0","state":"ACTIVE","test_runs":[]}}}`
	for _, test := range []struct {
		name       string
		kind       policyapi.Type
		file, raw  string
		predicates []search.Predicate
	}{
		{name: "push pull request", kind: policyapi.Push, file: "push-pull-request-opened.json", predicates: []search.Predicate{{Field: "pullRequestId", Constraint: search.Constraint{StringEquals: []string{"3"}}}, {Field: "branch", Constraint: search.Constraint{StringEquals: []string{"branch-a"}}}}},
		{name: "notification run", kind: policyapi.Notification, file: "notification-finished.json", predicates: []search.Predicate{{Field: "runType", Constraint: search.Constraint{StringEquals: []string{"TRACKED"}}}, {Field: "module", Constraint: search.Constraint{BooleanEquals: []bool{false}}}}},
		{name: "notification contract module", kind: policyapi.Notification, raw: module, predicates: []search.Predicate{{Field: "moduleId", Constraint: search.Constraint{StringEquals: []string{"module-a"}}}, {Field: "module", Constraint: search.Constraint{BooleanEquals: []bool{true}}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := newSampleWorld(t)
			if _, err := w.f.svc.Create(
				t.Context(),
				w.f.principal,
				core.Input{Name: "Typed metadata", Type: rego.Type(test.kind), Body: "package infraweave\nsample := false\n", Labels: []string{"autoattach:*"}},
			); err != nil {
				t.Fatal(err)
			}
			input := json.RawMessage(test.raw)
			if test.file != "" {
				input = evaluationInput(t, test.file)
			}
			actual, err := w.f.svc.Evaluate(t.Context(), fixtureEvaluationRequest(w.f, test.kind, input))
			if err != nil || len(actual.Receipts) != 1 {
				t.Fatalf("actual OPA evaluation: %+v %v", actual, err)
			}
			svc := evidenceService(w, evidenceAuthz{})
			id := actual.Receipts[0].Policy.String()
			page, err := svc.SearchEvaluations(t.Context(), w.f.principal, id, search.Input{Predicates: test.predicates})
			if err != nil || len(page.Edges) != 1 || page.Edges[0].Node.ID != actual.Receipts[0].ID {
				t.Fatalf("typed snapshot filters missed actual decision: %+v %v", page, err)
			}
			suggestions, err := svc.SuggestEvaluations(t.Context(), w.f.principal, id, search.SuggestionsInput{Predicates: test.predicates})
			if err != nil || suggestions.Count != 1 {
				t.Fatalf("facet/search typed scope differs: %+v %v", suggestions, err)
			}
			encoded, _ := json.Marshal(apisearch.Suggestions(suggestions))
			if bytes.Contains(encoded, []byte("private-module")) {
				t.Fatal("module notification private input leaked into facets")
			}
			miss := append([]search.Predicate{}, test.predicates...)
			miss[0].Exclude = true
			page, err = svc.SearchEvaluations(t.Context(), w.f.principal, id, search.Input{Predicates: miss})
			if err != nil || len(page.Edges) != 0 {
				t.Fatalf("inverse typed filter failed: %+v %v", page, err)
			}
		})
	}
}
