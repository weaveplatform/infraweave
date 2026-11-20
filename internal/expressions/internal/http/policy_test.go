package http

import (
	"context"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/expr"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

func TestUnitPolicyCheckDispatch(t *testing.T) {
	ctx := kernel.WithPrincipal(t.Context(), kernel.Principal{AccountID: kernel.AccountID{ULID: kernel.NewULID()}})
	engine := api.PolicyEngineType("REGO_V0")
	rangeWant := kernel.Range{Start: kernel.Position{Line: 2, Column: 8}, End: kernel.Position{Line: 2, Column: 12}}
	registry, err := expr.NewRegistry(
		expr.Defaults(),
		expr.Surface{Name: "policy", CheckPolicy: func(got context.Context, p kernel.Principal, in expr.PolicySource) ([]kernel.FieldError, error) {
			if got != ctx || p.AccountID.IsZero() || in != (expr.PolicySource{Type: "PLAN", Engine: "REGO_V0", Body: "source"}) {
				t.Fatalf("surface did not receive caller, context and source: %+v %+v", p, in)
			}
			return []kernel.FieldError{
				{Pointer: "/body", Reason: kernel.ReasonRule, Message: "source error", Range: &rangeWant},
				{Pointer: "/body", Reason: kernel.ReasonRule, Message: "internal error"},
			}, nil
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	s := New(registry, nil)
	out, err := s.PolicyCheck(ctx, api.PolicyCheckRequestObject{Body: &api.PolicyCheckRequest{Type: "PLAN", Body: "source", EngineType: &engine}})
	if err != nil {
		t.Fatal(err)
	}
	diagnostics := out.(api.PolicyCheck200JSONResponse).Body.Diagnostics
	if len(diagnostics) != 2 || diagnostics[0].Pointer != "/body" || diagnostics[0].Reason != "rule" || diagnostics[0].Message != "source error" || diagnostics[0].Range == nil ||
		*diagnostics[0].Range != sourceRange(rangeWant) ||
		diagnostics[1].Range != nil {
		t.Fatalf("surface diagnostics changed: %+v", diagnostics)
	}
	if _, err := s.context(ctx, "policy", ""); !refused(err, "/surface", false) {
		t.Fatalf("check-only surface accepted as CEL context: %v", err)
	}
}

func TestUnitPolicyCheckFailures(t *testing.T) {
	ctx := kernel.WithPrincipal(t.Context(), kernel.Principal{})
	request := api.PolicyCheckRequestObject{Body: &api.PolicyCheckRequest{Type: "PLAN", Body: "package infraweave"}}
	if _, err := (Server{}).PolicyCheck(t.Context(), request); !is(err, problem.Unauthenticated) {
		t.Fatalf("unauthenticated check: %v", err)
	}
	for _, surfaces := range [][]expr.Surface{nil, {{Name: "policy"}}, {{Name: "policy", CheckPolicy: func(context.Context, kernel.Principal, expr.PolicySource) ([]kernel.FieldError, error) {
		return nil, kernel.ErrUnavailable
	}}}} {
		registry, err := expr.NewRegistry(expr.Defaults(), surfaces...)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := New(registry, nil).PolicyCheck(ctx, request); !errors.Is(err, kernel.ErrUnavailable) {
			t.Fatalf("unavailable surface accepted: %v", err)
		}
	}
	if _, err := (Server{}).PolicyCheck(ctx, request); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("nil registry accepted: %v", err)
	}
	check := func(context.Context, kernel.Principal, expr.PolicySource) ([]kernel.FieldError, error) {
		return nil, nil
	}
	if _, err := expr.NewRegistry(
		expr.Defaults(),
		expr.Surface{Name: "policy", CheckPolicy: check},
		expr.Surface{Name: "policy", CheckPolicy: check},
	); !errors.Is(
		err,
		expr.ErrRequest,
	) {
		t.Fatalf("ambiguous policy owner accepted: %v", err)
	}
}
