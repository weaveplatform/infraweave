package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// File is the workflow file's path in a run's project root.
//
// ref: decision 0024 "Workflow command templates"
const File = ".infraweave/workflow.yml"

// MaxSize is the largest workflow file read, in bytes: nine templates of
// expressions.max_source's default 16 KiB each, with room for comments.
const MaxSize = 256 << 10

// Surface names the expression surface of the templates.
const Surface = "workflow"

// PlanFile is the saved plan's name in the project root, which planning
// writes and applying reads, and planFileName's value.
const PlanFile = "infraweave.plan"

// DefaultWorkspace is workspaceName's value for a stack that names no
// workspace: the one both tools start a configuration in.
const DefaultWorkspace = "default"

// Key names one command of a workflow.
type Key string

// The keys of a workflow, one per command the runner may need.
//
// ref: report 20 section 3.2
const (
	Init            Key = "init"
	WorkspaceSelect Key = "workspaceSelect"
	WorkspaceNew    Key = "workspaceNew"
	Plan            Key = "plan"
	ShowState       Key = "showState"
	ShowPlan        Key = "showPlan"
	GetOutputs      Key = "getOutputs"
	Apply           Key = "apply"
	Destroy         Key = "destroy"
)

// Keys are every key a workflow file holds, in the reference's order.
//
// ref: report 20 section 3.2
var Keys = []Key{
	Init, WorkspaceSelect, WorkspaceNew, Plan, ShowState, ShowPlan, GetOutputs, Apply, Destroy,
}

// Variable is one run parameter a template may read: its name and its
// JSON Schema.
type Variable struct {
	Name   string
	Schema json.RawMessage
}

// Variables are the run parameters, the only variables a template reads.
// Each is a value the worker already holds, so rendering reads no secret
// and asks the server nothing.
//
// ref: decision 0024 "Workflow command templates"
var Variables = []Variable{
	{Name: "planFileName", Schema: json.RawMessage(`{"type":"string"}`)},
	{Name: "lock", Schema: json.RawMessage(`{"type":"boolean"}`)},
	{Name: "refresh", Schema: json.RawMessage(`{"type":"boolean"}`)},
	{Name: "targets", Schema: json.RawMessage(`{"type":"array","items":{"type":"string"}}`)},
	{Name: "workspaceName", Schema: json.RawMessage(`{"type":"string"}`)},
	{Name: "lockTimeout", Schema: json.RawMessage(`{"type":"string"}`)},
}

// Params are the values of Variables for one rendering.
type Params struct {
	PlanFileName string
	// Lock is whether planning locks the state.
	Lock bool
	// Refresh is whether planning refreshes the state first.
	Refresh bool
	// Targets are the resource addresses a targeted replan plans; none
	// plans every resource.
	Targets       []string
	WorkspaceName string
	// LockTimeout is how long each command that locks the state waits for
	// the lock, written as Go writes a duration, which both tools parse.
	LockTimeout time.Duration
}

// Values are p as the variables' values.
func (p Params) Values() map[string]any {
	targets := make([]any, len(p.Targets))
	for i, t := range p.Targets {
		targets[i] = t
	}
	return map[string]any{
		"planFileName":  p.PlanFileName,
		"lock":          p.Lock,
		"refresh":       p.Refresh,
		"targets":       targets,
		"workspaceName": p.WorkspaceName,
		"lockTimeout":   p.LockTimeout.String(),
	}
}

// Templates are a workflow's command templates by key, each a text
// template whose literal text is shell.
type Templates map[Key]string

// Defaults are the built-in templates of the Terraform family under the
// binary bin: Terraform's and OpenTofu's commands take the same
// arguments. Each command that can lock the state waits lockTimeout, and
// planning skips the lock only when lock is false; apply, which writes
// the state, always locks.
//
// ref: decision 0010 § "How a run reaches it"
// ref: report 20 section 3.2
func Defaults(bin string) Templates {
	return Templates{
		Init:            bin + " init -input=false -lock-timeout=${{ lockTimeout }}",
		WorkspaceSelect: bin + " workspace select ${{ workspaceName }}",
		WorkspaceNew:    bin + " workspace new -lock-timeout=${{ lockTimeout }} ${{ workspaceName }}",
		Plan: bin + " plan -input=false -lock-timeout=${{ lockTimeout }} -lock=${{ lock }} " +
			`${{ refresh ? [] : ["-refresh=false"] }} -out=${{ planFileName }} ` +
			`${{ targets.map(t, "-target=" + t) }}`,
		ShowState:  bin + " show -json",
		ShowPlan:   bin + " show -json ${{ planFileName }}",
		GetOutputs: bin + " output -json",
		Apply:      bin + " apply -input=false -lock-timeout=${{ lockTimeout }} ${{ planFileName }}",
		Destroy:    bin + " destroy -auto-approve -input=false -lock-timeout=${{ lockTimeout }}",
	}
}

// ErrFile is a workflow file that cannot be used.
var ErrFile = errors.New("the file is not a workflow")

// Lines are the line in the file where each key's value starts.
type Lines map[Key]int

// Parse reads a workflow file: a YAML mapping of every key to a
// non-empty string, and the line each value starts on. A scalar is its
// text as written, so a command such as true stays a command. A missing
// key, one it does not know, one given twice, or a value that is null,
// empty or not a scalar is ErrFile naming every such key, with its line
// when it is in the file.
//
// ref: report 20 section 3.3
func Parse(data []byte) (Templates, Lines, error) {
	if len(data) > MaxSize {
		return nil, nil, fmt.Errorf("%w: it is %d bytes, over %d", ErrFile, len(data), MaxSize)
	}
	var doc yaml.Node
	switch err := yaml.NewDecoder(bytes.NewReader(data)).Decode(&doc); {
	case errors.Is(err, io.EOF):
		return nil, nil, fmt.Errorf("%w: it is empty", ErrFile)
	case err != nil:
		return nil, nil, fmt.Errorf("%w: %s", ErrFile, strings.TrimPrefix(err.Error(), "yaml: "))
	}
	if len(doc.Content) == 0 {
		return nil, nil, fmt.Errorf("%w: it is empty", ErrFile)
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("%w: line %d: it is not a mapping of keys to commands",
			ErrFile, root.Line)
	}
	out, lines := Templates{}, Lines{}
	var faults []string
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		key := Key(k.Value)
		switch {
		case !slices.Contains(Keys, key):
			faults = append(faults, fmt.Sprintf("line %d: %q is not a key of a workflow",
				k.Line, k.Value))
			continue
		case lines[key] != 0:
			faults = append(faults, fmt.Sprintf("line %d: %s is given twice", k.Line, key))
		case v.Kind != yaml.ScalarNode:
			faults = append(faults, fmt.Sprintf("line %d: %s is not a string", v.Line, key))
		case v.Tag == "!!null" || strings.TrimSpace(v.Value) == "":
			faults = append(faults, fmt.Sprintf("line %d: %s is empty", v.Line, key))
		default:
			out[key] = v.Value
		}
		lines[key] = v.Line
	}
	var missing []string
	for _, k := range Keys {
		if lines[k] == 0 {
			missing = append(missing, string(k))
		}
	}
	if len(missing) > 0 {
		faults = append(faults, strings.Join(missing, ", ")+" missing")
	}
	if len(faults) > 0 {
		return nil, nil, fmt.Errorf("%w: %s", ErrFile, strings.Join(faults, "; "))
	}
	return out, lines, nil
}
