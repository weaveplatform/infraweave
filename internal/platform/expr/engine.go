package expr

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/cost"
	"cel.dev/cel-go/common/operators"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/interpreter"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/mask"
)

// Form is how a field holds its source.
//
// ref: decision 0024 "Two forms"
type Form string

const (
	// FormExpression is a whole value, which keeps its CEL type.
	FormExpression Form = "expression"
	// FormTemplate is text with expressions embedded as ${{ … }}, rendered
	// into a string. A template that is exactly one ${{ … }} is an
	// expression.
	FormTemplate Form = "template"
)

// Now is the variable every surface has: the evaluation's fixed time.
const Now = "now"

// Variable is one variable a surface declares: its name, its JSON Schema,
// and whether it holds secrets. A secret variable is read only with a
// literal key, so an evaluation loads only the secrets it names.
type Variable struct {
	Name   string
	Schema json.RawMessage
	Secret bool
}

// Context is what a surface knows of one of its contexts: the variables it
// declares there, which narrow the surface's own by name, and sample
// values for trying an expression out.
type Context struct {
	Variables []Variable
	Sample    map[string]any
}

// Surface is a place where users write expressions, declared by the
// domain that owns it.
type Surface struct {
	Name      string
	Variables []Variable
	// Env authorizes p's read of the context ref names and describes it. It
	// fails with the owning domain's not-found error for a context p
	// cannot read.
	Env func(ctx context.Context, p kernel.Principal, ref string) (Context, error)
}

// Request names what to compile: the surface, the variables of the
// context, which narrow the surface's by name, the form and the source.
type Request struct {
	Surface   string
	Variables []Variable
	Form      Form
	Source    string
}

// Severity is how a diagnostic bears on a save: an error refuses it, a
// warning comes back with it.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Diagnostic is one finding about a source, with where it lies.
type Diagnostic struct {
	Severity Severity
	Message  string
	Range    kernel.Range
}

// FieldError is d as an error item of the field at pointer.
//
// ref: decision 0022
func (d Diagnostic) FieldError(pointer string) kernel.FieldError {
	r := d.Range
	return kernel.FieldError{
		Pointer: pointer,
		Reason:  kernel.ReasonRule,
		Message: d.Message,
		Range:   &r,
	}
}

// Cost is cel-go's static estimate of an evaluation's cost, in the units
// expressions.cost_limit counts.
type Cost struct {
	Min uint64
	Max uint64
}

// Check is what compiling a source finds: its diagnostics, the type of its
// result and its estimated cost.
type Check struct {
	Diagnostics []Diagnostic
	// Type is the result's CEL type, such as string or list(int); a
	// template's is string unless it is one whole expression.
	Type string
	Cost Cost
}

// OK reports whether c has no error, so the source can be evaluated.
func (c Check) OK() bool {
	return !slices.ContainsFunc(c.Diagnostics, func(d Diagnostic) bool {
		return d.Severity == SeverityError
	})
}

// CheckError is a source that does not compile, as Evaluate returns it.
type CheckError struct {
	Diagnostics []Diagnostic
}

func (e *CheckError) Error() string {
	for _, d := range e.Diagnostics {
		if d.Severity == SeverityError {
			return fmt.Sprintf("%d:%d: %s", d.Range.Start.Line, d.Range.Start.Column, d.Message)
		}
	}
	return "the source does not compile"
}

// Failed is an evaluation that stopped: through fail, through an error
// such as a missing key, or over a limit, which Limit names as its
// setting.
type Failed struct {
	Message string
	// Limit is the setting the evaluation went over, such as
	// expressions.cost_limit; empty for any other failure.
	Limit string
	// Range is the part of the source that failed, when known.
	Range *kernel.Range
}

func (f *Failed) Error() string { return f.Message }

// Input is what one evaluation runs against.
type Input struct {
	// Now and Seed fix the evaluation's time and random draws, so an
	// evaluation with the same Input gives the same result.
	//
	// ref: decision 0024 "Determinism"
	Now  time.Time
	Seed uint64
	// Values holds each non-secret variable's value, as decoded from JSON
	// or built from Go maps, slices and scalars.
	Values map[string]any
	// Secrets loads the keys of a secret variable that the source reads.
	// A key it leaves out is missing from the variable.
	Secrets func(ctx context.Context, variable string, keys []string) (map[string]string, error)
}

// Result is an evaluation's value as Go: nil, bool, int64, uint64,
// float64, string, []byte, time.Time, time.Duration, []any or
// map[string]any. A template's is its text, unless it is one whole
// expression.
type Result struct {
	Value any
	// Type is the value's CEL type, as Check gives it.
	Type string
	// Secret is whether the source read a secret, so the value is masked
	// wherever it is shown.
	Secret bool
	masker *mask.Masker
}

// Mask hides, in s, the secrets the evaluation read.
//
// ref: decision 0013
func (r Result) Mask(s string) string {
	if r.masker == nil {
		return s
	}
	return r.masker.String(s)
}

// ErrUnknownSurface is a request naming a surface no domain declares.
var ErrUnknownSurface = errors.New("unknown surface")

// ErrRequest is a request the registry cannot compile at all: a variable
// its surface does not declare, a schema that is not JSON, or an unknown
// form.
var ErrRequest = errors.New("invalid expression request")

// The caches' sizes. An environment is one surface context's declared
// types, a program one compiled source within one; both are rebuilt on a
// miss, so the sizes bound memory, not correctness.
const (
	environments = 256
	programs     = 4096
)

// interruptEvery is how many comprehension iterations run between checks
// of the timeout.
const interruptEvery = 100

// Registry compiles and evaluates expressions for the surfaces the
// domains declare, caching what it compiles.
type Registry struct {
	cfg      Config
	surfaces map[string]Surface
	base     *cel.Env
	envs     *lru[[32]byte, *environment]
	programs *lru[[32]byte, *program]
}

// NewRegistry builds a registry of surfaces under cfg's limits. A surface
// name appears once, a variable once within its surface, and no surface
// declares now.
func NewRegistry(cfg Config, surfaces ...Surface) (*Registry, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	r := &Registry{
		cfg:      cfg,
		surfaces: make(map[string]Surface, len(surfaces)),
		envs:     newLRU[[32]byte, *environment](environments),
		programs: newLRU[[32]byte, *program](programs),
	}
	for _, s := range surfaces {
		if _, dup := r.surfaces[s.Name]; dup || s.Name == "" {
			return nil, fmt.Errorf(
				"%w: surface %q is declared twice or has no name",
				ErrRequest,
				s.Name,
			)
		}
		seen := map[string]bool{}
		for _, v := range s.Variables {
			if v.Name == Now || seen[v.Name] {
				return nil, fmt.Errorf(
					"%w: surface %s declares %q more than once, or declares now, which every surface has",
					ErrRequest,
					s.Name,
					v.Name,
				)
			}
			seen[v.Name] = true
		}
		r.surfaces[s.Name] = s
	}
	none, err := fingerprint(nil)
	if err != nil {
		return nil, err
	}
	base, err := r.environment(none, nil)
	if err != nil {
		return nil, err
	}
	r.base = base.env
	return r, nil
}

// Surface returns the surface named name.
func (r *Registry) Surface(name string) (Surface, bool) {
	s, ok := r.surfaces[name]
	return s, ok
}

// Surfaces lists the surfaces' names in order.
func (r *Registry) Surfaces() []string {
	return slices.Sorted(maps.Keys(r.surfaces))
}

// Functions lists every function and macro an expression may call, the
// same on every surface, built from the library itself.
func (r *Registry) Functions() []Function {
	return functions(r.base)
}

// Check compiles req and reports what it finds. Its error is a request
// that cannot be compiled at all; a source that does not compile is
// diagnostics.
func (r *Registry) Check(req Request) (Check, error) {
	p, _, err := r.compile(req)
	if err != nil {
		return Check{}, err
	}
	return p.check, nil
}

// Evaluate compiles req and runs it against in, under the limits. A
// source that does not compile fails with *CheckError, and a failed
// evaluation with *Failed, its message masked when the source read a
// secret. The context's own cancellation is returned as it is.
func (r *Registry) Evaluate(ctx context.Context, req Request, in Input) (Result, error) {
	p, env, err := r.compile(req)
	if err != nil {
		return Result{}, err
	}
	if !p.check.OK() {
		return Result{}, &CheckError{Diagnostics: p.check.Diagnostics}
	}
	vars, masker, err := r.activation(ctx, p, env, in)
	if err != nil {
		return Result{}, err
	}
	res := Result{Type: p.check.Type, Secret: masker != nil, masker: masker}
	val, err := r.run(ctx, p, vars)
	if err == nil {
		res.Value, err = native(val)
		if err != nil {
			err = &Failed{Message: "The result " + err.Error() + "."}
		}
	}
	if f, ok := errors.AsType[*Failed](err); ok {
		f.Message = res.Mask(f.Message)
		return Result{}, f
	}
	return res, err
}

// environment is the cel-go environment of one set of declared variables.
type environment struct {
	env     *cel.Env
	schemas map[string]*schema
	secrets map[string]bool
}

// declared is the variables a request compiles against: its surface's,
// narrowed by name by the request's own.
func (r *Registry) declared(req Request) ([]Variable, error) {
	s, ok := r.surfaces[req.Surface]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownSurface, req.Surface)
	}
	vars := slices.Clone(s.Variables)
	for _, v := range req.Variables {
		i := slices.IndexFunc(vars, func(d Variable) bool { return d.Name == v.Name })
		if i < 0 {
			return nil, fmt.Errorf(
				"%w: surface %s declares no variable %q",
				ErrRequest,
				s.Name,
				v.Name,
			)
		}
		vars[i] = v
	}
	return vars, nil
}

// fingerprint identifies a set of declared variables by their names,
// secrecy and schemas.
func fingerprint(vars []Variable) ([32]byte, error) {
	h := sha256.New()
	sorted := slices.SortedFunc(slices.Values(vars), func(a, b Variable) int {
		return strings.Compare(a.Name, b.Name)
	})
	for _, v := range sorted {
		var schema bytes.Buffer
		if len(v.Schema) > 0 {
			if err := json.Compact(&schema, v.Schema); err != nil {
				return [32]byte{}, fmt.Errorf(
					"%w: variable %s's schema: %w",
					ErrRequest,
					v.Name,
					err,
				)
			}
		}
		fmt.Fprintf(h, "%q %t %d:%s\n", v.Name, v.Secret, schema.Len(), schema.Bytes())
	}
	return [32]byte(h.Sum(nil)), nil
}

// environment builds, or finds, the cel-go environment of vars, whose
// fingerprint is key.
func (r *Registry) environment(key [32]byte, vars []Variable) (*environment, error) {
	return r.envs.get(key, func() (*environment, error) {
		p, err := newProvider()
		if err != nil {
			return nil, err
		}
		e := &environment{schemas: map[string]*schema{}, secrets: map[string]bool{}}
		a := adapter{Adapter: p.Registry}
		opts := []cel.EnvOption{
			cel.CustomTypeProvider(p),
			cel.CustomTypeAdapter(a),
			cel.EagerlyValidateDeclarations(true),
			cel.Variable(Now, cel.TimestampType),
		}
		opts = append(opts, library(r.cfg.MaxResult, a)...)
		for _, v := range vars {
			s, err := parseSchema(v.Schema)
			if err != nil {
				return nil, fmt.Errorf("%w: variable %s's %w", ErrRequest, v.Name, err)
			}
			e.schemas[v.Name] = s
			t := p.celType(v.Name, s)
			if v.Secret {
				e.secrets[v.Name] = true
				if len(v.Schema) == 0 {
					t = cel.MapType(cel.StringType, cel.StringType)
				}
			}
			opts = append(opts, cel.Variable(v.Name, t))
		}
		if e.env, err = cel.NewEnv(opts...); err != nil {
			return nil, fmt.Errorf("expression environment: %w", err)
		}
		return e, nil
	})
}

// program is a compiled source: one part for an expression, one per
// literal or ${{ … }} for a template.
type program struct {
	check   Check
	parts   []part
	whole   bool
	secrets map[string][]string
}

// part is a template's literal text, or one compiled expression.
type part struct {
	text string
	prg  cel.Program
	tree *ast.AST
	at   kernel.Position
	// span is the whole expression, which a failure of its value covers.
	span kernel.Range
}

// compile builds, or finds, req's program and its environment.
func (r *Registry) compile(req Request) (*program, *environment, error) {
	if req.Form != FormExpression && req.Form != FormTemplate {
		return nil, nil, fmt.Errorf(
			"%w: form %q is neither expression nor template",
			ErrRequest,
			req.Form,
		)
	}
	vars, err := r.declared(req)
	if err != nil {
		return nil, nil, err
	}
	fp, err := fingerprint(vars)
	if err != nil {
		return nil, nil, err
	}
	env, err := r.environment(fp, vars)
	if err != nil {
		return nil, nil, err
	}
	h := sha256.New()
	fmt.Fprintf(h, "%q %x %s %d:", req.Surface, fp, req.Form, len(req.Source))
	h.Write([]byte(req.Source))
	p, err := r.programs.get([32]byte(h.Sum(nil)), func() (*program, error) {
		return r.build(env, req), nil
	})
	return p, env, err
}

// build compiles req's source in env, recording every finding as a
// diagnostic.
func (r *Registry) build(env *environment, req Request) *program {
	p := &program{secrets: map[string][]string{}, whole: req.Form == FormExpression}
	if n := len(req.Source); n > r.cfg.MaxSource {
		p.check.Diagnostics = []Diagnostic{{
			Severity: SeverityError,
			Message: fmt.Sprintf("The source is %d bytes, over expressions.max_source (%d).",
				n, r.cfg.MaxSource),
			Range: kernel.Range{
				Start: kernel.Position{Line: 1, Column: 1},
				End:   advance(kernel.Position{Line: 1, Column: 1}, req.Source),
			},
		}}
		return p
	}
	segs := []segment{{source: req.Source, at: kernel.Position{Line: 1, Column: 1}, isExpr: true}}
	if req.Form == FormTemplate {
		var bad *scanError
		if segs, bad = scan(req.Source); bad != nil {
			p.check.Diagnostics = []Diagnostic{{
				Severity: SeverityError,
				Message:  "This ${{ is never closed by }}.",
				Range:    kernel.Range{Start: bad.at, End: advance(bad.at, open)},
			}}
			return p
		}
		p.whole = whole(segs)
	}
	for _, seg := range segs {
		if !seg.isExpr {
			p.parts = append(p.parts, part{text: seg.text})
			continue
		}
		pt, t, ok := r.compileExpr(env, p, seg)
		if !ok {
			continue
		}
		if p.whole {
			p.check.Type = t
		}
		p.parts = append(p.parts, pt)
	}
	switch {
	case !p.check.OK():
		p.parts, p.check.Type = nil, ""
	case !p.whole:
		p.check.Type = "string"
	}
	return p
}

// compileExpr compiles one expression of p, adding what it finds to p's
// check. It reports false when the expression does not compile.
func (r *Registry) compileExpr(env *environment, p *program, seg segment) (part, string, bool) {
	parsed, iss := env.env.Parse(seg.source)
	if iss.Err() != nil {
		p.addIssues(iss, nil, seg.at)
		return part{}, "", false
	}
	checked, iss := env.env.Check(parsed)
	if iss.Err() != nil {
		p.addIssues(iss, parsed.NativeRep(), seg.at)
		return part{}, "", false
	}
	native := checked.NativeRep()
	span := spanOf(seg)
	if bad := secretKeys(native, env.secrets, p.secrets); len(bad) > 0 {
		for _, id := range bad {
			p.check.Diagnostics = append(p.check.Diagnostics, Diagnostic{
				Severity: SeverityError,
				Message: "A secret is read only with a literal key, as secrets.KEY or " +
					`secrets["KEY"].`,
				Range: rangeOf(native, id, seg.at),
			})
		}
		return part{}, "", false
	}
	est, err := env.env.EstimateCost(checked, estimator{})
	if err == nil {
		p.check.Cost.Min = addCost(p.check.Cost.Min, est.Min)
		p.check.Cost.Max = addCost(p.check.Cost.Max, est.Max)
		if est.Min > r.cfg.CostLimit {
			p.check.Diagnostics = append(p.check.Diagnostics, Diagnostic{
				Severity: SeverityWarning,
				Message: fmt.Sprintf("The cost is estimated at %d units or more, over "+
					"expressions.cost_limit (%d).", est.Min, r.cfg.CostLimit),
				Range: span,
			})
		}
	}
	sortRanges(native)
	prg, err := env.env.Program(checked,
		cel.CostLimit(r.cfg.CostLimit),
		cel.InterruptCheckFrequency(interruptEvery),
	)
	if err != nil {
		p.check.Diagnostics = append(p.check.Diagnostics, Diagnostic{
			Severity: SeverityError,
			Message:  err.Error(),
			Range:    span,
		})
		return part{}, "", false
	}
	typ := cel.FormatCELType(checked.OutputType())
	return part{prg: prg, tree: native, at: seg.at, span: span}, typ, true
}

// addIssues records cel-go's errors as diagnostics. With the parsed tree,
// an error on an expression covers the expression; otherwise one column.
func (p *program) addIssues(iss *cel.Issues, tree *ast.AST, at kernel.Position) {
	for _, e := range iss.Errors() {
		start := shift(at, e.Location.Line(), e.Location.Column())
		rng := kernel.Range{
			Start: start,
			End:   kernel.Position{Line: start.Line, Column: start.Column + 1},
		}
		if tree != nil && e.ExprID != 0 {
			rng = rangeOf(tree, e.ExprID, at)
		}
		p.check.Diagnostics = append(p.check.Diagnostics, Diagnostic{
			Severity: SeverityError,
			Message:  e.Message,
			Range:    rng,
		})
	}
}

// rangeOf is where node id of tree lies in the whole source, tree's
// expression starting at at. A field selection covers the field's name,
// which is what a misspelling or a missing key is about; a node with no
// recorded span covers one column.
func rangeOf(tree *ast.AST, id int64, at kernel.Position) kernel.Range {
	info := tree.SourceInfo()
	start := info.GetStartLocation(id)
	if sel := ast.MatchDescendants(ast.NavigateAST(tree), func(e ast.NavigableExpr) bool {
		return e.ID() == id && e.Kind() == ast.SelectKind
	}); len(sel) == 1 {
		// cel-go places a selection at its dot.
		from := shift(at, start.Line(), start.Column()+1)
		return kernel.Range{Start: from, End: advance(from, sel[0].AsSelect().FieldName())}
	}
	from := shift(at, start.Line(), start.Column())
	to := kernel.Position{Line: from.Line, Column: from.Column + 1}
	if span, ok := info.GetOffsetRange(id); ok && span.Stop > span.Start {
		stop := info.GetLocationByOffset(span.Stop)
		to = shift(at, stop.Line(), stop.Column())
	}
	return kernel.Range{Start: from, End: to}
}

// spanOf is seg's expression without the spaces around it.
func spanOf(seg segment) kernel.Range {
	trimmed := strings.TrimLeftFunc(seg.source, unicode.IsSpace)
	start := advance(seg.at, seg.source[:len(seg.source)-len(trimmed)])
	return kernel.Range{
		Start: start,
		End:   advance(start, strings.TrimRightFunc(trimmed, unicode.IsSpace)),
	}
}

// shift turns a cel-go location within an expression, its line counted
// from 1 and its column from 0, into a position in the whole source.
func shift(at kernel.Position, line, column int) kernel.Position {
	if line <= 1 {
		return kernel.Position{Line: at.Line, Column: at.Column + max(column, 0)}
	}
	return kernel.Position{Line: at.Line + line - 1, Column: column + 1}
}

func addCost(a, b uint64) uint64 {
	if a > ^uint64(0)-b {
		return ^uint64(0)
	}
	return a + b
}

// estimator leaves every size and call to cel-go's defaults.
type estimator struct{}

func (estimator) EstimateSize(cost.AstNode) *cost.SizeEstimate { return nil }

func (estimator) EstimateCallCost(
	string,
	string,
	*cost.AstNode,
	[]cost.AstNode,
) *cost.CallEstimate {
	return nil
}

// secretKeys adds to keys every literal key the expression reads from a
// secret variable, and returns each reference to one that is not read
// with a literal key.
//
// ref: decision 0024 "Evaluation and limits"
func secretKeys(a *ast.AST, secret map[string]bool, keys map[string][]string) []int64 {
	if len(secret) == 0 {
		return nil
	}
	var bad []int64
	idents := ast.MatchDescendants(ast.NavigateAST(a), func(e ast.NavigableExpr) bool {
		return e.Kind() == ast.IdentKind && secret[e.AsIdent()]
	})
	for _, id := range idents {
		key, ok := literalKey(id)
		if !ok {
			bad = append(bad, id.ID())
			continue
		}
		if !slices.Contains(keys[id.AsIdent()], key) {
			keys[id.AsIdent()] = append(keys[id.AsIdent()], key)
		}
	}
	return bad
}

// literalKey is the key a reference to a secret variable reads, when its
// parent reads it with a literal: v.KEY, v["KEY"], v.?KEY or v[?"KEY"].
func literalKey(id ast.NavigableExpr) (string, bool) {
	parent, ok := id.Parent()
	if !ok {
		return "", false
	}
	switch parent.Kind() {
	case ast.SelectKind:
		return parent.AsSelect().FieldName(), true
	case ast.CallKind:
		call := parent.AsCall()
		switch call.FunctionName() {
		case operators.Index, operators.OptIndex, operators.OptSelect:
		default:
			return "", false
		}
		args := call.Args()
		if len(args) != 2 || args[0].ID() != id.ID() || args[1].Kind() != ast.LiteralKind {
			return "", false
		}
		key, ok := args[1].AsLiteral().(types.String)
		return string(key), ok
	}
	return "", false
}

// sortRanges wraps every comprehension's range in fnSorted, so iterating
// a map the expression built visits its keys in order. The range's own
// node becomes the call, keeping its ID and type; its content moves to a
// new node, so nothing below it is copied.
//
// ref: decision 0024 "Determinism"
func sortRanges(a *ast.AST) {
	comps := ast.MatchDescendants(ast.NavigateAST(a), ast.KindMatcher(ast.ComprehensionKind))
	if len(comps) == 0 {
		return
	}
	fac := ast.NewExprFactory()
	next := ast.MaxID(a)
	info := a.SourceInfo()
	for _, c := range comps {
		rng := c.AsComprehension().IterRange()
		next++
		moved := fac.NewIdent(next, "")
		moved.SetKindCase(rng)
		a.SetType(next, a.GetType(rng.ID()))
		if ref, ok := a.ReferenceMap()[rng.ID()]; ok {
			a.SetReference(next, ref)
		}
		if span, ok := info.GetOffsetRange(rng.ID()); ok {
			info.SetOffsetRange(next, span)
		}
		rng.SetKindCase(fac.NewCall(rng.ID(), fnSorted, moved))
		a.SetReference(rng.ID(), ast.NewFunctionReference("infraweave_sorted"))
	}
}

// activation is the variables of one evaluation, with random.* bound to
// its seed, and a masker of the secrets it loads, nil when it loads none.
func (r *Registry) activation(
	ctx context.Context,
	p *program,
	env *environment,
	in Input,
) (interpreter.Activation, *mask.Masker, error) {
	vars := map[string]any{Now: types.Timestamp{Time: in.Now.UTC()}}
	for name, s := range env.schemas {
		if v, ok := in.Values[name]; ok && !env.secrets[name] {
			vars[name] = coerce(v, s)
		}
	}
	var loaded [][]byte
	for _, name := range slices.Sorted(maps.Keys(p.secrets)) {
		if in.Secrets == nil {
			return nil, nil, fmt.Errorf(
				"%w: the source reads %s, and no secrets are given",
				ErrRequest,
				name,
			)
		}
		got, err := in.Secrets(ctx, name, p.secrets[name])
		if err != nil {
			return nil, nil, fmt.Errorf("load %s: %w", name, err)
		}
		vars[name] = got
		for _, v := range got {
			loaded = append(loaded, []byte(v))
		}
	}
	act, err := cel.FunctionVars(vars, randomFunctions(in.Seed, r.cfg.MaxResult))
	if err != nil {
		return nil, nil, fmt.Errorf("expression activation: %w", err)
	}
	if len(p.secrets) == 0 {
		return act, nil, nil
	}
	return act, mask.New(loaded...), nil
}

// run evaluates p's parts in order under one timeout, a template's parts
// sharing one cost budget, and returns its value.
func (r *Registry) run(
	ctx context.Context,
	p *program,
	vars interpreter.Activation,
) (ref.Val, error) {
	limited, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()
	var out strings.Builder
	var spent uint64
	for _, pt := range p.parts {
		s := pt.text
		if pt.prg != nil {
			val, det, err := pt.prg.ContextEval(limited, vars)
			if det != nil && det.ActualCost() != nil {
				spent = addCost(spent, *det.ActualCost())
			}
			if err != nil {
				return nil, r.failure(ctx, pt, val, err)
			}
			if spent > r.cfg.CostLimit {
				return nil, r.overCost()
			}
			if p.whole {
				return val, r.size(val)
			}
			if s, err = text(val); err != nil {
				return nil, &Failed{
					Message: fmt.Sprintf(
						"A %s value cannot be inserted into text.",
						val.Type().TypeName(),
					),
					Range: &pt.span,
				}
			}
		}
		out.WriteString(s)
		if out.Len() > r.cfg.MaxResult {
			return nil, r.overSize(out.Len())
		}
	}
	return types.String(out.String()), nil
}

// size fails a whole value whose text is larger than max_result.
func (r *Registry) size(v ref.Val) error {
	s, err := text(v)
	if err == nil && len(s) > r.cfg.MaxResult {
		return r.overSize(len(s))
	}
	return nil
}

func (r *Registry) overSize(n int) error {
	return &Failed{
		Message: fmt.Sprintf("The result is %d bytes or more, over expressions.max_result (%d).",
			n, r.cfg.MaxResult),
		Limit: "expressions.max_result",
	}
}

func (r *Registry) overCost() error {
	return &Failed{
		Message: fmt.Sprintf("The evaluation went over expressions.cost_limit (%d units).",
			r.cfg.CostLimit),
		Limit: "expressions.cost_limit",
	}
}

// failure turns an evaluation's error into *Failed: fail's own, a limit
// named by its setting, or any other error located in the source. The
// cancellation of ctx, the caller's context, is returned as it is.
func (r *Registry) failure(ctx context.Context, pt part, val ref.Val, err error) error {
	if c, ok := errors.AsType[interpreter.EvalCancelledError](err); ok &&
		c.Cause == interpreter.CostLimitExceeded {
		return r.overCost()
	}
	if errors.Is(err, interpreter.InterruptError{}) {
		if ctx.Err() != nil {
			return ctx.Err() //nolint:wrapcheck // the caller's own cancellation, as it is
		}
		return &Failed{
			Message: fmt.Sprintf(
				"The evaluation went over expressions.timeout (%s).",
				r.cfg.Timeout,
			),
			Limit: "expressions.timeout",
		}
	}
	f, ok := errors.AsType[*Failed](err)
	if !ok {
		f = &Failed{Message: "The evaluation failed: " + strings.TrimSuffix(err.Error(), ".") + "."}
	}
	if errVal, ok := val.(*types.Err); ok && errVal.NodeID() != 0 && f.Range == nil {
		rng := rangeOf(pt.tree, errVal.NodeID(), pt.at)
		f.Range = &rng
	}
	return f
}
