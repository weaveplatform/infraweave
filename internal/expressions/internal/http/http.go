package http

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"math"
	"strconv"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/expr"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/mask"
)

// Server is the expressions domain's partial of the API's strict server.
// Each operation finds its surface in the registry and its context through
// the surface's Env, which decides whether the caller may read it.
type Server struct {
	registry *expr.Registry
	now      func() time.Time
}

// New returns the partial over registry, evaluating at the time now gives.
func New(registry *expr.Registry, now func() time.Time) Server {
	return Server{registry: registry, now: now}
}

// ExpressionCheck checks a source against its surface and context.
func (s Server) ExpressionCheck(
	ctx context.Context,
	req api.ExpressionCheckRequestObject,
) (api.ExpressionCheckResponseObject, error) {
	b := req.Body
	c, err := s.context(ctx, b.Surface, b.Ref)
	if err != nil {
		return nil, err
	}
	chk, err := s.registry.Check(request(b.Surface, c, b.Form, b.Source))
	if err != nil {
		return nil, err //nolint:wrapcheck // a surface the registry cannot compile is a fault
	}
	diags := make([]api.ExpressionDiagnostic, 0, len(chk.Diagnostics))
	for _, d := range chk.Diagnostics {
		diags = append(diags, api.ExpressionDiagnostic{
			Severity: api.ExpressionDiagnosticSeverity(d.Severity),
			Message:  d.Message,
			Range:    sourceRange(d.Range),
		})
	}
	return api.ExpressionCheck200JSONResponse{Body: api.ExpressionCheck{
		Diagnostics: diags,
		Type:        chk.Type,
		Cost:        api.ExpressionCost{Min: cost(chk.Cost.Min), Max: cost(chk.Cost.Max)},
	}}, nil
}

// ExpressionComplete lists the items an editor may insert at the cursor.
func (s Server) ExpressionComplete(
	ctx context.Context,
	req api.ExpressionCompleteRequestObject,
) (api.ExpressionCompleteResponseObject, error) {
	b := req.Body
	c, err := s.context(ctx, b.Surface, b.Ref)
	if err != nil {
		return nil, err
	}
	at := kernel.Position{Line: int(b.Position.Line), Column: int(b.Position.Column)}
	items, err := s.registry.Complete(request(b.Surface, c, b.Form, b.Source), at, c.Sample)
	if errors.Is(err, expr.ErrCursor) {
		return nil, &kernel.Invalid{
			Detail: "The position lies outside the source.",
			Errors: []kernel.FieldError{{
				Pointer: "/position",
				Reason:  kernel.ReasonRule,
				Message: fmt.Sprintf("The source has no line %d, column %d.", at.Line, at.Column),
			}},
		}
	}
	if err != nil {
		return nil, err //nolint:wrapcheck // as for a check
	}
	out := make([]api.ExpressionCompletion, 0, len(items))
	for _, i := range items {
		out = append(out, api.ExpressionCompletion{
			Label:       i.Label,
			Kind:        api.ExpressionCompletionKind(i.Kind),
			Detail:      i.Detail,
			Description: i.Description,
			Range:       sourceRange(i.Range),
		})
	}
	return api.ExpressionComplete200JSONResponse{Body: api.ExpressionCompletions{Items: out}}, nil
}

// ExpressionEvaluate evaluates a source against its context's sample
// values, each variable the request's sample names replacing the
// context's, and a secret variable's every key masked.
func (s Server) ExpressionEvaluate(
	ctx context.Context,
	req api.ExpressionEvaluateRequestObject,
) (api.ExpressionEvaluateResponseObject, error) {
	b := req.Body
	c, err := s.context(ctx, b.Surface, b.Ref)
	if err != nil {
		return nil, err
	}
	values := maps.Clone(c.Sample)
	if values == nil {
		values = map[string]any{}
	}
	if b.Sample != nil {
		maps.Copy(values, *b.Sample)
	}
	now := s.now()
	res, err := s.registry.Evaluate(ctx, request(b.Surface, c, b.Form, b.Source), expr.Input{
		Now: now,
		// The time's bits seed random.*, so the sign of a time before
		// 1970 does not matter.
		Seed:    uint64(now.UnixNano()), //nolint:gosec // as above
		Values:  values,
		Secrets: masked,
	})
	if err != nil {
		return nil, evaluateError(err)
	}
	value, err := jsonValue(res.Value)
	if err != nil {
		return nil, err
	}
	return api.ExpressionEvaluate200JSONResponse{
		Body: api.ExpressionEvaluation{Value: value, Type: res.Type},
	}, nil
}

// context is the context ref names on the surface named surface, as p
// may read it: 422 naming the surface for one no domain declares, and 404
// for a context p cannot read.
func (s Server) context(ctx context.Context, surface, ref string) (expr.Context, error) {
	p, err := principal(ctx)
	if err != nil {
		return expr.Context{}, err
	}
	sf, ok := s.registry.Surface(surface)
	if !ok {
		return expr.Context{}, &kernel.Invalid{
			Detail: fmt.Sprintf("No surface is named %q.", surface),
			Errors: []kernel.FieldError{{
				Pointer: "/surface",
				Reason:  kernel.ReasonNotFound,
				Message: fmt.Sprintf("No surface is named %q.", surface),
			}},
		}
	}
	c, err := sf.Env(ctx, p, ref)
	if errors.Is(err, expr.ErrNoContext) {
		//nolint:wrapcheck // a problem, answered as it is
		return expr.Context{}, problem.NotFound.Error(
			fmt.Sprintf("The %s surface has no context %q in this account.", surface, ref))
	}
	if err != nil {
		return expr.Context{}, err //nolint:wrapcheck // the surface's domain names the read
	}
	return c, nil
}

// request is the registry's request for source in form, against the
// context's variables on surface.
func request(surface string, c expr.Context, form api.ExpressionForm, source string) expr.Request {
	return expr.Request{
		Surface:   surface,
		Variables: c.Variables,
		Keys:      c.Keys,
		Form:      expr.Form(form),
		Source:    source,
	}
}

// masked loads every secret key a source reads as the mask, so no
// evaluation an editor asks for reads a secret.
func masked(_ context.Context, _ string, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		out[k] = mask.Replacement
	}
	return out, nil
}

// evaluateError answers a source that does not type-check, or an
// evaluation that fails, as 422 naming the source with each fault's
// range.
func evaluateError(err error) error {
	if ce, ok := errors.AsType[*expr.CheckError](err); ok {
		inv := &kernel.Invalid{Detail: "The source does not type-check."}
		for _, d := range ce.Diagnostics {
			if d.Severity == expr.SeverityError {
				inv.Errors = append(inv.Errors, d.FieldError("/source"))
			}
		}
		return inv
	}
	if f, ok := errors.AsType[*expr.Failed](err); ok {
		return &kernel.Invalid{
			Detail: "The evaluation failed.",
			Errors: []kernel.FieldError{{
				Pointer: "/source",
				Reason:  kernel.ReasonRule,
				Message: f.Message,
				Range:   f.Range,
			}},
		}
	}
	return err
}

// jsonValue is an evaluation's value as CEL writes it in JSON: bytes in
// base64, a timestamp in RFC 3339, a duration in seconds with an s, and a
// double JSON has no number for as its name.
func jsonValue(v any) (any, error) {
	switch v := v.(type) {
	case []byte:
		return base64.StdEncoding.EncodeToString(v), nil
	case time.Time:
		return v.UTC().Format(time.RFC3339Nano), nil
	case time.Duration:
		return strconv.FormatFloat(v.Seconds(), 'f', -1, 64) + "s", nil
	case float64:
		switch {
		case math.IsNaN(v):
			return "NaN", nil
		case math.IsInf(v, 1):
			return "Infinity", nil
		case math.IsInf(v, -1):
			return "-Infinity", nil
		}
		return v, nil
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			j, err := jsonValue(e)
			if err != nil {
				return nil, err
			}
			out[i] = j
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			j, err := jsonValue(e)
			if err != nil {
				return nil, err
			}
			out[k] = j
		}
		return out, nil
	case nil, bool, int64, uint64, string:
		return v, nil
	}
	return nil, fmt.Errorf("%w: %T", errValue, v)
}

// errValue is a value of a type Result does not list.
var errValue = errors.New("expressions: an evaluation's value has no JSON form")

// cost is an estimate as the API's integer, the largest standing for any
// more.
func cost(n uint64) int64 {
	return int64(min(n, math.MaxInt64)) //nolint:gosec // clamped
}

func sourceRange(r kernel.Range) api.SourceRange {
	return api.SourceRange{Start: sourcePosition(r.Start), End: sourcePosition(r.End)}
}

func sourcePosition(p kernel.Position) api.SourcePosition {
	return api.SourcePosition{
		Line:   int32(min(p.Line, math.MaxInt32)),   //nolint:gosec // clamped
		Column: int32(min(p.Column, math.MaxInt32)), //nolint:gosec // clamped
	}
}

func principal(ctx context.Context) (kernel.Principal, error) {
	p, ok := kernel.PrincipalFrom(ctx)
	if !ok {
		//nolint:wrapcheck // a problem, answered as it is
		return kernel.Principal{}, problem.Unauthenticated.Error(
			"The request carries no valid credential.")
	}
	return p, nil
}
