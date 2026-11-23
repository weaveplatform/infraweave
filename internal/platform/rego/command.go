package rego

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/open-policy-agent/opa/v1/ast"
	oparego "github.com/open-policy-agent/opa/v1/rego"
	"github.com/open-policy-agent/opa/v1/types"
)

// Commands runs the exec calls of one worker initialization evaluation.
// The caller supplies a fresh value with each Request; no prepared query
// holds it. Exec's errors are reported to operators, so their text must
// name no argument, output or credential.
type Commands interface {
	Exec(ctx context.Context, argv []string) (Command, error)
}

// Command is an exec call's result as the policy sees it: the command's
// exit status and its complete standard output and error.
type Command struct {
	Status int
	Stdout string
	Stderr string
}

// CommandError stops an evaluation whose exec call produced no result.
// Its text is Err's, which Commands keeps free of arguments and output.
type CommandError struct{ Err error }

func (e *CommandError) Error() string { return e.Err.Error() }
func (e *CommandError) Unwrap() error { return e.Err }

// ErrNoCommands stops a worker evaluation whose caller supplied no
// Commands, as on an executor that cannot start initialization helpers.
var ErrNoCommands = errors.New("initialization commands are unavailable")

// commandName is the worker-only built-in. Server capabilities remove it.
const commandName = "exec"

// commandScope carries one evaluation's Commands to the built-in through
// the evaluation's context, and keeps the first failure, which OPA's halt
// error does not unwrap to.
type commandScope struct {
	commands Commands
	mu       sync.Mutex
	err      error
}

type commandKey struct{}

func (s *commandScope) fail(err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		s.err = &CommandError{Err: err}
	}
	return oparego.NewHaltError(s.err) //nolint:wrapcheck // OPA's halt carries the CommandError
}

// failure is the evaluation's first failed call; a server evaluation has
// no scope and so none.
func (s *commandScope) failure() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func commandDecl() *types.Function {
	return types.NewFunction(
		types.Args(types.NewArray(nil, types.S)),
		types.NewObject([]*types.StaticProperty{
			types.NewStaticProperty("status", types.N),
			types.NewStaticProperty("stdout", types.S),
			types.NewStaticProperty("stderr", types.S),
		}, nil),
	)
}

// command is the exec built-in. It holds no state: the evaluation's
// context supplies its Commands, so a prepared query shared by many
// evaluations reaches only the calling evaluation's executor. A failed
// call halts the evaluation, which then fails closed instead of leaving
// a deny rule undefined.
func command(bctx oparego.BuiltinContext, term *ast.Term) (*ast.Term, error) {
	scope, _ := bctx.Context.Value(commandKey{}).(*commandScope)
	if scope == nil {
		return nil, oparego.NewHaltError(&CommandError{Err: ErrNoCommands}) //nolint:wrapcheck // OPA's halt carries the CommandError
	}
	if scope.commands == nil {
		return nil, scope.fail(ErrNoCommands)
	}
	list, ok := term.Value.(*ast.Array)
	if !ok {
		return nil, scope.fail(fmt.Errorf("%w: exec takes an array of strings", ErrInput))
	}
	argv := make([]string, 0, list.Len())
	for i := range list.Len() {
		word, ok := list.Elem(i).Value.(ast.String)
		if !ok {
			return nil, scope.fail(fmt.Errorf("%w: exec takes an array of strings", ErrInput))
		}
		argv = append(argv, string(word))
	}
	out, err := scope.commands.Exec(bctx.Context, argv)
	if err != nil {
		return nil, scope.fail(err)
	}
	return ast.ObjectTerm(
		ast.Item(ast.StringTerm("status"), ast.IntNumberTerm(out.Status)),
		ast.Item(ast.StringTerm("stdout"), ast.StringTerm(out.Stdout)),
		ast.Item(ast.StringTerm("stderr"), ast.StringTerm(out.Stderr)),
	), nil
}
