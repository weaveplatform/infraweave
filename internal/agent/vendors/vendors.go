package vendors

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/iac/workflow"
	"github.com/weaveplatform/infraweave/internal/platform/expr"
)

// PlanFile is the saved plan's name in the project root. A paused run's
// saved workspace carries it to the container that applies.
const PlanFile = workflow.PlanFile

// Command is one command of a phase: the workflow key it renders, its
// shell line, and Stdout, a file its standard output is written to
// instead of the log; empty for the log.
type Command struct {
	Key    workflow.Key
	Line   string
	Stdout string
}

// Options are what a phase's commands take from the run: the run
// parameters its templates read, and the file planning writes the plan's
// JSON to.
type Options struct {
	Params   workflow.Params
	PlanJSON string
}

// Errors of a workflow that cannot be had or used.
var (
	ErrTool  = errors.New("no built-in workflow for the tool")
	ErrCheck = errors.New("a template does not check")
	ErrState = errors.New("the workflow has no phase for the state")
)

// Workflow is a run's command templates, each checked, and the registry
// that renders them on the workflow surface.
type Workflow struct {
	registry  *expr.Registry
	templates workflow.Templates
}

// Surface is the workflow templates' surface, with no context: the
// runner renders against the run's own parameters.
func Surface() expr.Surface {
	vars := make([]expr.Variable, len(workflow.Variables))
	for i, v := range workflow.Variables {
		vars[i] = expr.Variable{Name: v.Name, Schema: v.Schema}
	}
	return expr.Surface{Name: workflow.Surface, Variables: vars, Shell: true}
}

// Defaults are the built-in workflow of tool, the binary a run's
// specification names.
func Defaults(tool wire.RunSpecToolName) (*Workflow, error) {
	if tool != wire.Terraform && tool != wire.Tofu {
		return nil, fmt.Errorf("%w %q", ErrTool, tool)
	}
	return New(workflow.Defaults(string(tool)), nil)
}

// Read is the workflow a workflow file's data holds.
func Read(data []byte) (*Workflow, error) {
	t, lines, err := workflow.Parse(data)
	if err != nil {
		return nil, err //nolint:wrapcheck // ErrFile names each key at fault
	}
	return New(t, lines)
}

// New checks every key of t, before any command runs, and answers the
// workflow that renders them. A key that does not compile is ErrCheck
// naming it, the line lines places it on when it has one, and each
// error's range within its template.
//
// ref: decision 0024 "Workflow command templates"
func New(t workflow.Templates, lines workflow.Lines) (*Workflow, error) {
	reg, err := expr.NewRegistry(expr.Defaults(), Surface())
	if err != nil {
		return nil, fmt.Errorf("the workflow surface: %w", err)
	}
	var faults []string
	for _, k := range workflow.Keys {
		chk, err := reg.Check(request(t[k]))
		if err != nil {
			return nil, fmt.Errorf("check %s: %w", k, err)
		}
		at := string(k)
		if n := lines[k]; n > 0 {
			at = fmt.Sprintf("%s (line %d)", k, n)
		}
		for _, d := range chk.Diagnostics {
			if d.Severity == expr.SeverityError {
				faults = append(faults, fmt.Sprintf("%s: %d:%d: %s",
					at, d.Range.Start.Line, d.Range.Start.Column, d.Message))
			}
		}
	}
	if len(faults) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrCheck, strings.Join(faults, "; "))
	}
	return &Workflow{registry: reg, templates: t}, nil
}

// phases are the keys each state's phase runs, in order.
var phases = map[wire.WorkerState][]workflow.Key{
	wire.WorkerStateINITIALIZING: {workflow.Init},
	wire.WorkerStatePLANNING:     {workflow.Plan, workflow.ShowPlan},
	wire.WorkerStateAPPLYING:     {workflow.Apply},
	wire.WorkerStateDESTROYING:   {workflow.Destroy},
}

// Commands are the commands of state's phase, in the order they run,
// each rendered with o's parameters when the phase runs: init for
// INITIALIZING; plan, then showPlan with its output to o.PlanJSON, for
// PLANNING; apply for APPLYING; and destroy for DESTROYING. A template
// whose evaluation fails is an error naming its key.
func (w *Workflow) Commands(
	ctx context.Context,
	state wire.WorkerState,
	o Options,
) ([]Command, error) {
	keys, ok := phases[state]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrState, state)
	}
	out := make([]Command, len(keys))
	for i, k := range keys {
		res, err := w.registry.Evaluate(ctx, request(w.templates[k]), expr.Input{
			Values: o.Params.Values(),
		})
		if f, ok := errors.AsType[*expr.Failed](err); ok && f.Range != nil {
			err = fmt.Errorf("%d:%d: %w", f.Range.Start.Line, f.Range.Start.Column, f)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		line, _ := res.Value.(string)
		out[i] = Command{Key: k, Line: line}
		if k == workflow.ShowPlan {
			out[i].Stdout = o.PlanJSON
		}
	}
	return out, nil
}

// request is source as a template of the workflow surface.
func request(source string) expr.Request {
	return expr.Request{Surface: workflow.Surface, Form: expr.FormTemplate, Source: source}
}
