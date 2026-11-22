package rego

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/open-policy-agent/opa/v1/ast"
	oparego "github.com/open-policy-agent/opa/v1/rego"
	"github.com/open-policy-agent/opa/v1/topdown"
	opaprint "github.com/open-policy-agent/opa/v1/topdown/print"
)

// Policy is one isolated user module, compiled beside its type's preamble.
type Policy struct {
	Type   Type
	Engine EngineType
	Body   string
}

// Request evaluates a policy over independently decoded JSON. Simulation uses
// its smaller input bound; no input or print state is retained by the cache.
type Request struct {
	Policy     Policy
	Input      json.RawMessage
	Simulation bool
	// Redact is evaluation-local and runs before diagnostic text is bounded.
	// It is never retained by a prepared query.
	Redact func(string) string
}

// Result is the preamble's bounded raw decision and captured print text.
type Result struct {
	Decision       json.RawMessage
	Print          string
	PrintTruncated bool
}

// Outcome preserves the input ordering of an attached-policy collection.
type Outcome struct {
	Result Result
	Err    error
}

// Errors distinguish limits, invalid policy metadata, malformed inputs and an
// evaluated result of the wrong shape. OPA diagnostics remain in CompileError.
var (
	ErrPolicy = errors.New("invalid policy")
	ErrInput  = errors.New("invalid policy input")
	ErrLimit  = errors.New("policy execution limit exceeded")
	ErrResult = errors.New("invalid policy result")
)

var (
	processQueries = newCache()
	processSlots   = make(chan struct{}, Concurrency)
)

// Engine binds its budgets to the bounded process cache and execution slots.
// Prepared queries contain modules alone; each Eval supplies its own input and
// print hook. Engine is safe for concurrent use.
type Engine struct {
	settings     Settings
	cache        *queryCache
	slots        chan struct{}
	capabilities func() *ast.Capabilities
	scope        string
}

// New constructs a runtime with validated budgets and the process's effective
// capability mode. It starts no background workers and performs no I/O.
func New(settings Settings) (*Engine, error) {
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	return &Engine{settings: settings, cache: processQueries, slots: processSlots, capabilities: capabilities}, nil
}

// NewWorker constructs an initialization-only engine with a distinct prepared
// query identity. It cannot enable commands in server engines or their caches.
func NewWorker(settings Settings) (*Engine, error) {
	e, err := New(settings)
	if err != nil {
		return nil, err
	}
	e.scope = "worker-initialization"
	return e, nil
}

// InputLimit lets typed input adapters enforce the configured byte budget
// before allocating their own decoded document.
func (e *Engine) InputLimit() int64 { return e.settings.MaxInputBytes }

func (e *Engine) validate(p Policy) error {
	if e.scope != "" && p.Type != Initialization {
		return fmt.Errorf("%w: worker initialization type required", ErrPolicy)
	}
	if rules(p.Type) == nil {
		return fmt.Errorf("%w: unknown type %q", ErrPolicy, p.Type)
	}
	if p.Engine != V0 && p.Engine != V1 {
		return fmt.Errorf("%w: unsupported engine %q", ErrPolicy, p.Engine)
	}
	if int64(len(p.Body)) > e.settings.MaxBodyBytes {
		return fmt.Errorf("%w: policy body exceeds %d bytes", ErrLimit, e.settings.MaxBodyBytes)
	}
	return nil
}

// Check performs the same bounded compilation used by evaluation, before a
// caller stores a policy body or starts a simulation.
func (e *Engine) Check(ctx context.Context, p Policy) error {
	if err := e.validate(p); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, e.settings.timeout(p.Type))
	defer cancel()
	if err := e.acquire(ctx); err != nil {
		return err
	}
	defer e.release()
	_, err := e.prepare(ctx, p)
	return err
}

func (e *Engine) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("policy execution: %w", err)
	}
	select {
	case e.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("policy execution: %w", ctx.Err())
	}
}
func (e *Engine) release() { <-e.slots }

func (e *Engine) prepare(ctx context.Context, p Policy) (oparego.PreparedEvalQuery, error) {
	caps := e.capabilities()
	raw, err := json.Marshal(caps)
	if err != nil {
		return oparego.PreparedEvalQuery{}, fmt.Errorf("encode policy capabilities: %w", err)
	}
	key := cacheKey{Scope: e.scope, Body: sha256.Sum256([]byte(p.Body)), Dialect: p.Engine, Type: p.Type, Preamble: preambleVersion, Capabilities: sha256.Sum256(raw)}
	if query, ok := e.cache.get(key); ok {
		return query, nil
	}
	version := ast.RegoV1
	if p.Engine == V0 {
		version = ast.RegoV0
	}
	user, err := ast.ParseModuleWithOpts("policy.rego", p.Body, ast.ParserOptions{RegoVersion: version, Capabilities: caps})
	if err != nil {
		return oparego.PreparedEvalQuery{}, compileError(err)
	}
	if err := validateModule(user); err != nil {
		return oparego.PreparedEvalQuery{}, err
	}
	pre, err := ast.ParseModuleWithOpts("preamble.rego", preamble(p.Type), ast.ParserOptions{RegoVersion: ast.RegoV1, Capabilities: caps})
	if err != nil {
		return oparego.PreparedEvalQuery{}, compileError(err)
	}
	query, err := oparego.New(
		oparego.Query("data.infraweave.result"),
		oparego.ParsedModule(pre), oparego.ParsedModule(user),
		oparego.Capabilities(caps), oparego.SetRegoVersion(ast.RegoV1),
		oparego.EnablePrintStatements(true),
		oparego.Function1(&oparego.Function{Name: "sanitized", Decl: sanitizedDecl()}, sanitized),
	).PrepareForEval(ctx)
	if err != nil {
		return oparego.PreparedEvalQuery{}, compileError(err)
	}
	if err := ctx.Err(); err != nil {
		return oparego.PreparedEvalQuery{}, fmt.Errorf("policy compilation: %w", err)
	}
	e.cache.put(key, query)
	return query, nil
}

// Evaluate cancels compilation, queue waits and evaluation at the earlier caller
// or policy deadline. Size checks run before parsing or input allocation.
func (e *Engine) Evaluate(ctx context.Context, req Request) (Result, error) {
	if err := e.validate(req.Policy); err != nil {
		return Result{}, err
	}
	limit := e.settings.MaxInputBytes
	if req.Simulation {
		limit = min(limit, e.settings.MaxSimulationBytes)
	}
	if int64(len(req.Input)) > limit {
		return Result{}, fmt.Errorf("%w: policy input exceeds %d bytes", ErrLimit, limit)
	}
	ctx, cancel := context.WithTimeout(ctx, e.settings.timeout(req.Policy.Type))
	defer cancel()
	if err := e.acquire(ctx); err != nil {
		return Result{}, err
	}
	defer e.release()
	query, err := e.prepare(ctx, req.Policy)
	if err != nil {
		return Result{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(req.Input))
	decoder.UseNumber()
	var input any
	if err := decoder.Decode(&input); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrInput, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Result{}, ErrInput
	}
	printed := &printBuffer{redact: req.Redact}
	results, err := query.Eval(ctx, oparego.EvalInput(input), oparego.EvalPrintHook(printed))
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, fmt.Errorf("policy execution: %w", ctx.Err())
		}
		if topdown.IsCancel(err) {
			return Result{}, context.Canceled
		}
		return Result{}, fmt.Errorf("evaluate policy: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("policy execution: %w", err)
	}
	if len(results) != 1 || len(results[0].Expressions) != 1 {
		return Result{}, ErrResult
	}
	decision, ok := results[0].Expressions[0].Value.(map[string]any)
	if !ok {
		return Result{}, ErrResult
	}
	if err := boundDecisionRedacted(req.Policy.Type, decision, req.Redact); err != nil {
		return Result{}, err
	}
	body, err := json.Marshal(decision)
	if err != nil {
		return Result{}, fmt.Errorf("encode policy decision: %w", err)
	}
	return Result{Decision: body, Print: printed.text.String(), PrintTruncated: printed.truncated}, nil
}

// EvaluateAll evaluates an attached collection with eight workers and the
// combined deadline. Results retain request order; decisions are not combined.
func (e *Engine) EvaluateAll(ctx context.Context, requests []Request) []Outcome {
	outcomes := make([]Outcome, len(requests))
	if len(requests) == 0 {
		return outcomes
	}
	ctx, cancel := context.WithTimeout(ctx, e.settings.Timeouts.Combined)
	defer cancel()
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(Concurrency, len(requests)) {
		workers.Go(func() {
			for i := range jobs {
				outcomes[i].Result, outcomes[i].Err = e.Evaluate(ctx, requests[i])
			}
		})
	}
	for i := range requests {
		select {
		case jobs <- i:
		case <-ctx.Done():
			for j := i; j < len(requests); j++ {
				outcomes[j].Err = ctx.Err()
			}
			close(jobs)
			workers.Wait()
			return outcomes
		}
	}
	close(jobs)
	workers.Wait()
	return outcomes
}

type printBuffer struct {
	text      strings.Builder
	truncated bool
	redact    func(string) string
}

// EvaluateSequence applies the combined budget to adapters whose output changes
// the next policy's input. Each program independently decodes its own document.
func (e *Engine) EvaluateSequence(ctx context.Context, requests []Request, advance func(int, Outcome) (json.RawMessage, error)) []Outcome {
	outcomes := make([]Outcome, len(requests))
	if len(requests) == 0 {
		return outcomes
	}
	ctx, cancel := context.WithTimeout(ctx, e.settings.Timeouts.Combined)
	defer cancel()
	input := requests[0].Input
	for i, request := range requests {
		request.Input = input
		outcomes[i].Result, outcomes[i].Err = e.Evaluate(ctx, request)
		next, err := advance(i, outcomes[i])
		if err != nil {
			outcomes[i].Err = err
			continue
		}
		input = next
	}
	return outcomes
}

func (p *printBuffer) Print(_ opaprint.Context, message string) error {
	if p.redact != nil {
		message = p.redact(message)
	}
	remaining := PrintBytes - p.text.Len()
	if remaining <= 0 {
		p.truncated = true
		return nil
	}
	message += "\n"
	if len(message) > remaining {
		message = truncate(message, remaining)
		p.truncated = true
	}
	p.text.WriteString(message)
	return nil
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit]
}
