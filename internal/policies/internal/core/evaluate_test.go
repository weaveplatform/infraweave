package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
)

func TestUnitEvaluationReservedTargetsRefusedBeforeStorage(t *testing.T) {
	account := kernel.NewAccountID()
	for _, req := range []policyapi.EvaluationRequest{
		{},
		{Account: account, Type: policyapi.Plan, Target: policyapi.Target{Kind: policyapi.StackTarget}},
		{Account: account, Type: policyapi.Plan, Target: policyapi.Target{Kind: policyapi.ModuleTarget, ID: kernel.NewULID()}},
		{Account: account, Type: policyapi.Intent, Target: policyapi.Target{Kind: policyapi.IntentTarget, ID: kernel.NewULID()}},
		{Account: account, Type: policyapi.Type("ACCESS"), Target: policyapi.Target{Kind: policyapi.StackTarget, ID: kernel.NewULID()}},
		{Account: account, Type: policyapi.Login, Target: policyapi.Target{Kind: policyapi.SpaceTarget, ID: kernel.NewULID()}},
		{Account: account, Type: policyapi.Login, Target: policyapi.Target{Kind: policyapi.StackTarget, ID: kernel.NewULID()}},
		{Account: account, Type: policyapi.Notification, Target: policyapi.Target{Kind: policyapi.StackTarget, ID: kernel.NewULID()}},
		{Account: account, Type: policyapi.Plan, Target: policyapi.Target{Kind: policyapi.StackTarget, ID: kernel.NewULID()}, Run: new(kernel.ULID)},
	} {
		if _, err := New(Deps{}).Evaluate(t.Context(), req); !errors.Is(err, policyapi.ErrTarget) {
			t.Fatalf("unavailable target reached storage: %+v %v", req, err)
		}
	}
	req := policyapi.EvaluationRequest{
		Account:    account,
		Type:       policyapi.Plan,
		Target:     policyapi.Target{Kind: policyapi.StackTarget, ID: kernel.NewULID()},
		Input:      json.RawMessage(`{}`),
		BuildInput: func(context.Context) (json.RawMessage, error) { t.Fatal("ambiguous builder called"); return nil, nil },
	}
	if _, err := New(Deps{}).Evaluate(t.Context(), req); !errors.Is(err, policyapi.ErrInput) {
		t.Fatalf("ambiguous facts reached storage: %v", err)
	}
}

func TestUnitEvaluationFailureMetadataExcludesSourceValues(t *testing.T) {
	for _, test := range []struct {
		err  error
		code string
	}{
		{context.DeadlineExceeded, "TIMEOUT"}, {context.Canceled, "CANCELED"}, {rego.ErrInput, "INPUT"}, {rego.ErrResult, "RESULT"}, {rego.ErrLimit, "LIMIT"}, {rego.ErrPolicy, "COMPILE"}, {&rego.CompileError{}, "COMPILE"}, {errRuntime, "RUNTIME"}, {errors.New("private-source-marker"), "EXECUTION"},
	} {
		got := executionFailure(fmt.Errorf("private-source-marker: %w", test.err))
		if got.Code != test.code || got.Message == "" || strings.Contains(got.Message, "private-source-marker") {
			t.Fatalf("unsafe failure metadata: %+v", got)
		}
	}
}

func TestUnitLoginRewriteChangesOnlyPresentSessionTeams(t *testing.T) {
	for _, test := range []struct {
		input, decision string
		wantError       error
	}{
		{`{}`, `{"team":[]}`, nil},
		{`{"request":{},"slack":{}}`, `{"team":["new"]}`, nil},
		{`bad`, `{"team":["new"]}`, rego.ErrInput},
		{`{}`, `{"team":2}`, rego.ErrResult},
	} {
		out, err := rewriteLoginTeams(json.RawMessage(test.input), json.RawMessage(test.decision))
		if !errors.Is(err, test.wantError) {
			t.Fatalf("rewrite error %v, want %v", err, test.wantError)
		}
		if err == nil && string(out) != test.input {
			t.Fatal("rewrite invented absent session facts")
		}
	}
}
