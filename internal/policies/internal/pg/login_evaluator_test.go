package pg_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
)

func TestAccLoginEvaluatorKeepsOrderedDecisionsAndEvidenceAtomic(t *testing.T) {
	f := newFixture(t)
	member := true
	request := accessapi.LoginRequest{
		Account: f.principal.AccountID, At: f.now,
		Session: accessapi.LoginSession{Member: &member, TeamsKnown: true, Teams: []string{"upstream-team"}},
	}
	before := f.count(t, "events")
	got, err := f.svc.EvaluateLogin(t.Context(), request)
	if err != nil || got.HasPolicies || got.Allow || f.count(t, "events") != before {
		t.Fatalf("policy-free fallback moved out of access: %+v, %v", got, err)
	}
	for _, input := range []core.Input{
		{Name: "First V0 rewrite", Type: rego.Login, Engine: rego.V0, Body: `package infraweave
team["rewritten-team"] { true }
roles["team"]["space-reader"] { true }`},
		{Name: "Second V1 entry", Type: rego.Login, Engine: rego.V1, Body: `package infraweave
allow if "rewritten-team" in input.session.teams`},
	} {
		if _, err := f.svc.Create(t.Context(), f.principal, input); err != nil {
			t.Fatal(err)
		}
	}
	before = f.count(t, "events")
	abort := errors.New("entry transaction rolls back")
	err = tx.Do(t.Context(), f.db, tx.Options{}, func(ctx context.Context) error {
		got, err := f.svc.EvaluateLogin(ctx, request)
		if err != nil {
			return err
		}
		if !got.HasPolicies || !got.Allow || got.Admin || !slices.Equal(got.Teams, []string{"rewritten-team"}) ||
			!slices.Equal(got.Roles["team"], []string{"space-reader"}) {
			t.Fatalf("stored dialects did not produce account-entry grants: %+v", got)
		}
		return abort
	})
	if !errors.Is(err, abort) || f.count(t, "events") != before {
		t.Fatalf("entry rollback retained evaluation evidence: %v", err)
	}
	got, err = f.svc.EvaluateLogin(t.Context(), request)
	if err != nil || !got.Allow || f.count(t, "events") != before+2 || !slices.Equal(request.Session.Teams, []string{"upstream-team"}) {
		t.Fatalf("committed entry changed its original facts or lost evidence: %+v, %v", got, err)
	}
	before = f.count(t, "events")
	request.Session.Member = nil
	got, err = f.svc.EvaluateLogin(t.Context(), request)
	if err != nil || !got.HasPolicies || got.Allow || got.Admin || got.Message == "" || f.count(t, "events") <= before {
		t.Fatalf("unavailable upstream membership did not commit a safe refusal: %+v, %v", got, err)
	}
}
