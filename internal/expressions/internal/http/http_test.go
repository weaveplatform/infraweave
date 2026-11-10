package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/expr"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/mask"
)

// errDown is a surface whose domain cannot answer.
var errDown = errors.New("down")

// server is the partial over a test surface whose context web holds a
// variable, a masked write-only one and the stack's ID, env closed to
// their names; hidden is a context the caller cannot read, and down one
// its domain cannot answer.
func server(t *testing.T) Server {
	t.Helper()
	r, err := expr.NewRegistry(expr.Defaults(), expr.Surface{
		Name: "test",
		Variables: []expr.Variable{
			{Name: "env", Schema: json.RawMessage(
				`{"type": "object", "additionalProperties": {"type": "string"}}`)},
			{Name: "stack", Schema: json.RawMessage(
				`{"type": "object", "properties": {"id": {"type": "string"}}}`)},
			{Name: "secrets", Secret: true},
		},
		Env: func(_ context.Context, _ kernel.Principal, ref string) (expr.Context, error) {
			switch ref {
			case "web":
				return expr.Context{
					Keys: map[string][]string{"env": {"TOKEN", "ZONE"}},
					Sample: map[string]any{
						"env":   map[string]any{"ZONE": "a", "TOKEN": mask.Replacement},
						"stack": map[string]any{"id": "web"},
					},
				}, nil
			case "down":
				return expr.Context{}, errDown
			}
			return expr.Context{}, fmt.Errorf("%w: %s", expr.ErrNoContext, ref)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return New(r, func() time.Time { return time.Date(2026, 11, 9, 8, 30, 0, 0, time.UTC) })
}

// is reports whether err is a problem of type t.
func is(err error, t problem.Type) bool {
	pe, ok := errors.AsType[*problem.Error](err)
	return ok && pe.Problem.Title == t.Title && pe.Problem.Status == t.Status
}

// refused reports whether err refuses exactly the field pointer, with a
// range when ranged.
func refused(err error, pointer string, ranged bool) bool {
	inv, ok := errors.AsType[*kernel.Invalid](err)
	return ok && len(inv.Errors) == 1 && inv.Errors[0].Pointer == pointer &&
		(inv.Errors[0].Range != nil) == ranged
}

// failure is how a case's error must look, or nil for none.
type failure func(error) bool

func problemOf(t problem.Type) failure { return func(err error) bool { return is(err, t) } }

func field(pointer string, ranged bool) failure {
	return func(err error) bool { return refused(err, pointer, ranged) }
}

func down(err error) bool { return errors.Is(err, errDown) }

// TestUnitCheck checks a source on the surface's context: its type and
// cost, a misspelt field's range, an unknown surface refused by name, and
// a context the caller cannot read, or whose domain cannot answer.
func TestUnitCheck(t *testing.T) {
	s := server(t)
	ctx := kernel.WithPrincipal(t.Context(), kernel.Principal{})
	for _, c := range []struct {
		name    string
		ctx     context.Context
		body    api.ExpressionCheckRequest
		want    failure
		typ     string
		diagnum int
	}{
		{"a template", ctx, api.ExpressionCheckRequest{
			Surface: "test", Ref: "web", Form: "template", Source: "https://${{ env.ZONE }}",
		}, nil, "string", 0},
		{"an expression", ctx, api.ExpressionCheckRequest{
			Surface: "test", Ref: "web", Form: "expression", Source: "size(env)",
		}, nil, "int", 0},
		{"a misspelt field", ctx, api.ExpressionCheckRequest{
			Surface: "test", Ref: "web", Form: "expression", Source: "stack.nmae",
		}, nil, "", 1},
		{"a misspelt key", ctx, api.ExpressionCheckRequest{
			Surface: "test", Ref: "web", Form: "expression", Source: "env.ZOEN",
		}, nil, "string", 1},
		{"an unknown surface", ctx, api.ExpressionCheckRequest{
			Surface: "nope", Ref: "web", Form: "expression", Source: "1",
		}, field("/surface", false), "", 0},
		{"a hidden context", ctx, api.ExpressionCheckRequest{
			Surface: "test", Ref: "hidden", Form: "expression", Source: "1",
		}, problemOf(problem.NotFound), "", 0},
		{"a domain down", ctx, api.ExpressionCheckRequest{
			Surface: "test", Ref: "down", Form: "expression", Source: "1",
		}, down, "", 0},
		{"no principal", t.Context(), api.ExpressionCheckRequest{
			Surface: "test", Ref: "web", Form: "expression", Source: "1",
		}, problemOf(problem.Unauthenticated), "", 0},
	} {
		res, err := s.ExpressionCheck(c.ctx, api.ExpressionCheckRequestObject{Body: &c.body})
		if c.want != nil {
			if !c.want(err) {
				t.Errorf("%s: %v", c.name, err)
			}
			continue
		}
		got, ok := res.(api.ExpressionCheck200JSONResponse)
		if err != nil || !ok || got.Body.Type != c.typ || len(got.Body.Diagnostics) != c.diagnum ||
			got.Body.Cost.Max < got.Body.Cost.Min {
			t.Errorf("%s: %+v, %v", c.name, res, err)
		}
	}
	res, err := s.ExpressionCheck(ctx, api.ExpressionCheckRequestObject{
		Body: &api.ExpressionCheckRequest{
			Surface: "test", Ref: "web", Form: "template", Source: "${{ stack.nmae }}",
		},
	})
	if want := []api.ExpressionDiagnostic{{
		Severity: "error",
		Message:  res.(api.ExpressionCheck200JSONResponse).Body.Diagnostics[0].Message,
		Range: api.SourceRange{
			Start: api.SourcePosition{Line: 1, Column: 11},
			End:   api.SourcePosition{Line: 1, Column: 15},
		},
	}}; err != nil || !reflect.DeepEqual(res.(api.ExpressionCheck200JSONResponse).Body.Diagnostics, want) {
		t.Errorf("a misspelt field's diagnostics %+v, %v", res, err)
	}
	if got := cost(math.MaxUint64); got != math.MaxInt64 {
		t.Errorf("cost(MaxUint64) = %d", got)
	}
}

// TestUnitComplete completes at a cursor from the context's sample, and
// refuses a cursor outside the source.
func TestUnitComplete(t *testing.T) {
	s := server(t)
	ctx := kernel.WithPrincipal(t.Context(), kernel.Principal{})
	complete := func(ref, source string, line, column int32) (api.ExpressionCompleteResponseObject, error) {
		return s.ExpressionComplete(ctx, api.ExpressionCompleteRequestObject{
			Body: &api.ExpressionCompleteRequest{
				Surface: "test", Ref: ref, Form: "template", Source: source,
				Position: api.SourcePosition{Line: line, Column: column},
			},
		})
	}
	res, err := complete("web", "${{ env.Z }}", 1, 10)
	got, ok := res.(api.ExpressionComplete200JSONResponse)
	if want := []api.ExpressionCompletion{{
		Label: "ZONE", Kind: "field", Detail: "string",
		Range: api.SourceRange{
			Start: api.SourcePosition{Line: 1, Column: 9},
			End:   api.SourcePosition{Line: 1, Column: 10},
		},
	}}; err != nil || !ok || !reflect.DeepEqual(got.Body.Items, want) {
		t.Errorf("env.Z = %+v, %v", res, err)
	}
	if res, err := complete("web", "plain", 1, 3); err != nil ||
		len(res.(api.ExpressionComplete200JSONResponse).Body.Items) != 0 {
		t.Errorf("outside a ${{ … }} = %+v, %v", res, err)
	}
	if _, err := complete("web", "${{ env.Z }}", 2, 1); !refused(err, "/position", false) {
		t.Errorf("a cursor outside the source = %v", err)
	}
	if _, err := complete("hidden", "${{ env.Z }}", 1, 10); !is(err, problem.NotFound) {
		t.Errorf("a hidden context = %v", err)
	}
	broken, err := expr.NewRegistry(expr.Defaults(), expr.Surface{
		Name: "test",
		Env: func(context.Context, kernel.Principal, string) (expr.Context, error) {
			return expr.Context{Variables: []expr.Variable{{Name: "undeclared"}}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s = New(broken, time.Now)
	if _, err := complete("web", "${{ env.Z }}", 1, 10); !errors.Is(err, expr.ErrRequest) {
		t.Errorf("a context declaring a variable its surface lacks = %v", err)
	}
	if _, err := s.ExpressionCheck(ctx, api.ExpressionCheckRequestObject{
		Body: &api.ExpressionCheckRequest{
			Surface: "test",
			Ref:     "web",
			Form:    "expression",
			Source:  "1",
		},
	}); !errors.Is(err, expr.ErrRequest) {
		t.Errorf("checking against it = %v", err)
	}
}

// TestUnitEvaluate evaluates against the context's sample and the
// request's, each secret masked, and refuses a source that does not
// type-check or whose evaluation fails, with the fault's range.
func TestUnitEvaluate(t *testing.T) {
	s := server(t)
	ctx := kernel.WithPrincipal(t.Context(), kernel.Principal{})
	evaluate := func(ref, form, source string, sample map[string]any) (any, string, error) {
		body := api.ExpressionEvaluateRequest{
			Surface: "test", Ref: ref, Form: api.ExpressionForm(form), Source: source,
		}
		if sample != nil {
			body.Sample = &sample
		}
		res, err := s.ExpressionEvaluate(ctx, api.ExpressionEvaluateRequestObject{Body: &body})
		if err != nil {
			return nil, "", err
		}
		got := res.(api.ExpressionEvaluate200JSONResponse).Body
		return got.Value, got.Type, nil
	}
	for _, c := range []struct {
		form, source string
		sample       map[string]any
		value        any
		typ          string
	}{
		{"template", "${{ env.ZONE }}-${{ stack.id }}", nil, "a-web", "string"},
		{"template", "${{ env.ZONE }}", map[string]any{"env": map[string]any{"ZONE": "b"}}, "b", "string"},
		{"expression", "env.TOKEN", nil, mask.Replacement, "string"},
		{"expression", `"Bearer " + secrets.TOKEN`, nil, "Bearer " + mask.Replacement, "string"},
		{"expression", "now", nil, "2026-11-09T08:30:00Z", "timestamp"},
		{"expression", `duration("1.5s")`, nil, "1.5s", "duration"},
		{"expression", `b"hi"`, nil, "aGk=", "bytes"},
		{
			"expression", "[0.0 / 0.0, 1.0 / 0.0, -1.0 / 0.0, 0.5]", nil,
			[]any{"NaN", "Infinity", "-Infinity", 0.5},
			"list(double)",
		},
		{
			"expression", `{"a": [null, true, 1, 2u]}`, nil,
			map[string]any{"a": []any{nil, true, int64(1), uint64(2)}},
			"map(string, list(dyn))",
		},
	} {
		value, typ, err := evaluate("web", c.form, c.source, c.sample)
		if err != nil || !reflect.DeepEqual(value, c.value) || typ != c.typ {
			t.Errorf("%s = %#v (%s), %v", c.source, value, typ, err)
		}
	}
	for _, c := range []struct {
		ref, source string
		want        failure
	}{
		{"web", "${{ stack.nmae }}", field("/source", true)},
		{"web", "${{ env.ZOEN }}", field("/source", true)},
		{"web", `${{ fail("no") }}`, field("/source", true)},
		{"hidden", "x", problemOf(problem.NotFound)},
	} {
		if _, _, err := evaluate(c.ref, "template", c.source, nil); !c.want(err) {
			t.Errorf("%s on %s = %v", c.source, c.ref, err)
		}
	}
	if _, _, err := evaluate(
		"web",
		"expression",
		"stack.nmae",
		nil,
	); !refused(
		err,
		"/source",
		true,
	) {
		t.Errorf("a misspelt field = %v", err)
	}
}

// TestUnitJSONValue refuses a value Result does not list, wherever it
// lies.
func TestUnitJSONValue(t *testing.T) {
	for _, v := range []any{struct{}{}, []any{struct{}{}}, map[string]any{"a": struct{}{}}} {
		if _, err := jsonValue(v); !errors.Is(err, errValue) {
			t.Errorf("%#v = %v", v, err)
		}
	}
}
